package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/downloader"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

type Scheduler struct {
	db         *database.DB
	registry   *sources.Registry
	downloader *downloader.Downloader
	hooks      *hooks.Manager
	cron       *cron.Cron
	entryIDs   map[string]cron.EntryID
	mu         sync.Mutex
}

func New(db *database.DB, registry *sources.Registry, dl *downloader.Downloader, hooks *hooks.Manager) *Scheduler {
	s := &Scheduler{
		db:         db,
		registry:   registry,
		downloader: dl,
		hooks:      hooks,
		cron:       cron.New(),
		entryIDs:   make(map[string]cron.EntryID),
	}
	s.loadSchedules()
	s.cron.Start()
	return s
}

func (s *Scheduler) Stop() {
	<-s.cron.Stop().Done()
}

func (s *Scheduler) ScheduleProduct(product *database.Product) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entryID, ok := s.entryIDs[product.ID]; ok {
		s.cron.Remove(entryID)
		delete(s.entryIDs, product.ID)
	}

	if product.CheckWindowStart == "" {
		return nil
	}

	entryID, err := s.cron.AddFunc(product.CheckWindowStart, func() {
		s.syncProduct(product.ID)
	})
	if err != nil {
		return err
	}

	s.entryIDs[product.ID] = entryID
	slog.Info("Scheduled product", "productID", product.ID, "schedule", product.CheckWindowStart)
	return nil
}

func (s *Scheduler) UnscheduleProduct(productID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if entryID, ok := s.entryIDs[productID]; ok {
		s.cron.Remove(entryID)
		delete(s.entryIDs, productID)
	}
}

func (s *Scheduler) loadSchedules() {
	var products []database.Product
	if err := s.db.Where("auto_download = ? AND check_window_start != ?", true, "").Find(&products).Error; err != nil {
		slog.Error("Failed to load scheduled products", "error", err)
		return
	}

	for i := range products {
		if err := s.ScheduleProduct(&products[i]); err != nil {
			slog.Error("Failed to schedule product", "productID", products[i].ID, "error", err)
		}
	}
	slog.Info("Loaded product schedules", "count", len(products))
}

func (s *Scheduler) syncProduct(productID string) {
	ctx := context.Background()
	slog.Info("Starting sync", "productID", productID)

	var product database.Product
	if err := s.db.First(&product, "id = ?", productID).Error; err != nil {
		slog.Error("Product not found", "productID", productID)
		return
	}

	adapter, ok := s.registry.Get(product.SourceID)
	if !ok {
		slog.Error("Source adapter not found", "sourceID", product.SourceID)
		return
	}

	deliveries, err := adapter.FetchDeliveries(ctx, product.ExternalID)
	if err != nil {
		slog.Error("Failed to fetch deliveries", "productID", productID, "error", err)
		s.emitSyncFailed(product.SourceID, productID, err)
		return
	}

	newFilesCount := 0
	for _, delivery := range deliveries {
		files, err := adapter.FetchFiles(ctx, product.ExternalID, delivery.ExternalID)
		if err != nil {
			slog.Error("Failed to fetch files", "deliveryID", delivery.ExternalID, "error", err)
			continue
		}

		for _, fileInfo := range files {
			fileID := buildFileID(productID, delivery.ExternalID, fileInfo.ExternalID)
			var count int64
			s.db.Model(&database.File{}).Where("id = ?", fileID).Count(&count)
			if count > 0 {
				continue
			}

			deliveryID := buildDeliveryID(productID, delivery.ExternalID)
			file := &database.File{
				ID:                fileID,
				DeliveryID:        deliveryID,
				ProductID:         productID,
				SourceID:          product.SourceID,
				ExternalID:        fileInfo.ExternalID,
				FileName:          fileInfo.FileName,
				FileSize:          fileInfo.FileSize,
				ExpectedChecksum:  fileInfo.Checksum,
				ChecksumAlgorithm: fileInfo.ChecksumAlgorithm,
				DownloadURI:       fileInfo.DownloadURI,
				ReleasedAt:        &fileInfo.ReleasedAt,
			}

			s.ensureDelivery(deliveryID, productID, &delivery)

			if err := s.db.Create(file).Error; err != nil {
				slog.Error("Failed to create file", "fileID", fileID, "error", err)
				continue
			}

			newFilesCount++

			event := hooks.NewEvent(hooks.EventFileAvailable, product.SourceID).
				WithProduct(productID, product.Name).
				WithDelivery(deliveryID, delivery.Name).
				WithFile(fileID, fileInfo.FileName, fileInfo.FileSize, fileInfo.Checksum, "")
			s.hooks.Emit(ctx, event)

			if product.AutoDownload && !file.Skipped {
				go func(fID string) {
					if err := s.downloader.Download(context.Background(), fID); err != nil {
						slog.Error("Auto-download failed", "fileID", fID, "error", err)
					}
				}(fileID)
			}
		}
	}

	now := time.Now()
	product.LastCheckedAt = &now
	s.db.Save(&product)

	s.hooks.Emit(ctx, hooks.NewEvent(hooks.EventSyncCompleted, product.SourceID).WithProduct(productID, product.Name))
	slog.Info("Sync completed", "productID", productID, "newFiles", newFilesCount)
}

func (s *Scheduler) ensureDelivery(deliveryID, productID string, info *sources.DeliveryInfo) {
	var count int64
	s.db.Model(&database.Delivery{}).Where("id = ?", deliveryID).Count(&count)
	if count > 0 {
		return
	}

	delivery := &database.Delivery{
		ID:          deliveryID,
		ProductID:   productID,
		ExternalID:  info.ExternalID,
		Name:        info.Name,
		PublishedAt: &info.PublishedAt,
		ExpiresAt:   info.ExpiresAt,
	}
	s.db.Create(delivery)
}

func (s *Scheduler) emitSyncFailed(sourceID, productID string, err error) {
	event := hooks.NewEvent(hooks.EventSyncFailed, sourceID).
		WithError("SYNC_ERROR", err.Error())
	s.hooks.Emit(context.Background(), event)
}

func buildDeliveryID(productID, deliveryExternalID string) string {
	return productID + ":" + deliveryExternalID
}

func buildFileID(productID, deliveryExternalID, fileExternalID string) string {
	return productID + ":" + deliveryExternalID + ":" + fileExternalID
}

func (s *Scheduler) SyncNow(_ context.Context, productID string) error {
	go s.syncProduct(productID)
	return nil
}

func (s *Scheduler) GetNextRun(productID string) *time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()

	entryID, ok := s.entryIDs[productID]
	if !ok {
		return nil
	}
	next := s.cron.Entry(entryID).Next
	return &next
}
