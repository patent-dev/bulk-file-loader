package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"gorm.io/gorm/clause"
)

// SyncEvent reports the outcome of syncing one product.
type SyncEvent struct {
	ProductID   string
	ProductName string
	Deliveries  int
	Files       int
	NewFiles    int
	Err         error
}

// SyncProgress receives one event per synced product. It may be nil.
type SyncProgress func(SyncEvent)

// SyncResult is the outcome of syncing one product.
type SyncResult struct {
	Deliveries int
	Files      int
	NewFileIDs []string
}

// SyncProduct refreshes one product's deliveries and files from its source.
// Deliveries whose file list cannot be fetched are reported as an error after
// the remaining deliveries were synced, so a partial catalogue never looks
// complete.
func (s *Service) SyncProduct(ctx context.Context, productID string) (SyncResult, error) {
	return s.syncProduct(ctx, productID, false)
}

// syncProduct is SyncProduct; with events it also emits file.available for
// new files and sync.completed or sync.failed. Only the server's scheduled
// and on-demand syncs send events: a first sync of a large catalogue would
// otherwise send one webhook per file, and the CLI exits before delivering.
func (s *Service) syncProduct(ctx context.Context, productID string, events bool) (SyncResult, error) {
	emit := func(e *hooks.Event) {
		if events {
			s.Hooks.Emit(ctx, e)
		}
	}
	var product database.Product
	if err := s.DB.First(&product, "id = ?", productID).Error; err != nil {
		return SyncResult{}, fmt.Errorf("%w: product %s", ErrNotFound, productID)
	}
	adapter, ok := s.Registry.Get(product.SourceID)
	if !ok {
		return SyncResult{}, fmt.Errorf("adapter not found for source: %s", product.SourceID)
	}

	deliveries, err := adapter.FetchDeliveries(ctx, product.ExternalID)
	if err != nil {
		err = fmt.Errorf("fetch deliveries for %s: %w", productID, err)
		emit(syncFailedEvent(product, err))
		return SyncResult{}, err
	}

	var res SyncResult
	var listErrs []error
	for _, d := range deliveries {
		deliveryID := productID + ":" + d.ExternalID
		delivery := database.Delivery{
			ID:          deliveryID,
			ProductID:   productID,
			ExternalID:  d.ExternalID,
			Name:        d.Name,
			PublishedAt: database.OptionalTime(d.PublishedAt),
			ExpiresAt:   d.ExpiresAt,
		}
		if err := s.DB.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"name", "published_at", "expires_at"}),
		}).Create(&delivery).Error; err != nil {
			listErrs = append(listErrs, fmt.Errorf("save delivery %s: %w", d.ExternalID, err))
			continue
		}

		files, err := adapter.FetchFiles(ctx, product.ExternalID, d.ExternalID)
		if err != nil {
			listErrs = append(listErrs, fmt.Errorf("delivery %s: %w", d.ExternalID, err))
			continue
		}
		res.Deliveries++

		for _, f := range files {
			fileID := deliveryID + ":" + f.ExternalID
			var existing int64
			s.DB.Model(&database.File{}).Where("id = ?", fileID).Count(&existing)

			file := database.File{
				ID:                fileID,
				DeliveryID:        deliveryID,
				ProductID:         productID,
				SourceID:          product.SourceID,
				ExternalID:        f.ExternalID,
				FileName:          f.FileName,
				FileSize:          f.FileSize,
				ExpectedChecksum:  f.Checksum,
				ChecksumAlgorithm: f.ChecksumAlgorithm,
				DownloadURI:       f.DownloadURI,
				ReleasedAt:        database.OptionalTime(f.ReleasedAt),
			}
			if err := s.DB.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "id"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"file_name", "file_size", "expected_checksum", "checksum_algorithm",
					"download_uri", "released_at", "updated_at",
				}),
			}).Create(&file).Error; err != nil {
				listErrs = append(listErrs, fmt.Errorf("save file %s: %w", fileID, err))
				continue
			}
			res.Files++

			if existing == 0 {
				res.NewFileIDs = append(res.NewFileIDs, fileID)
				emit(hooks.NewEvent(hooks.EventFileAvailable, product.SourceID).
					WithProduct(productID, product.Name).
					WithDelivery(deliveryID, d.Name).
					WithFile(fileID, f.FileName, f.FileSize, f.Checksum, ""))
			}
		}
	}

	// Only the timestamp: a full save would overwrite schedule changes made
	// while the sync ran.
	if err := s.DB.Model(&database.Product{}).Where("id = ?", productID).
		Update("last_checked_at", time.Now()).Error; err != nil {
		slog.Error("Failed to record sync time", "productID", productID, "error", err)
	}

	if len(listErrs) > 0 {
		err := fmt.Errorf("%s: %d of %d deliveries incomplete: %w",
			productID, len(listErrs), len(deliveries), errors.Join(listErrs...))
		emit(syncFailedEvent(product, err))
		return res, err
	}
	emit(hooks.NewEvent(hooks.EventSyncCompleted, product.SourceID).WithProduct(productID, product.Name))
	slog.Info("Sync completed", "productID", productID, "files", res.Files, "newFiles", len(res.NewFileIDs))
	return res, nil
}

func syncFailedEvent(product database.Product, err error) *hooks.Event {
	return hooks.NewEvent(hooks.EventSyncFailed, product.SourceID).
		WithProduct(product.ID, product.Name).
		WithError("SYNC_ERROR", err.Error())
}

// SyncSourceFiles syncs every product of a source, reporting each product to
// progress. Products that fail are reported and skipped; the joined errors
// are returned at the end.
func (s *Service) SyncSourceFiles(ctx context.Context, sourceID string, progress SyncProgress) error {
	if _, ok := s.Registry.Get(sourceID); !ok {
		return fmt.Errorf("adapter not found for source: %s", sourceID)
	}
	var products []database.Product
	if err := s.DB.Where("source_id = ?", sourceID).Order("name ASC").Find(&products).Error; err != nil {
		return fmt.Errorf("failed to get products for source %s: %w", sourceID, err)
	}

	var errs []error
	for _, p := range products {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := s.SyncProduct(ctx, p.ID)
		if err != nil {
			errs = append(errs, err)
		}
		if progress != nil {
			progress(SyncEvent{
				ProductID: p.ID, ProductName: p.Name,
				Deliveries: res.Deliveries, Files: res.Files, NewFiles: len(res.NewFileIDs),
				Err: err,
			})
		}
	}
	return errors.Join(errs...)
}

// SyncProductFull refreshes the source's product list and then one product.
func (s *Service) SyncProductFull(ctx context.Context, productID string, progress SyncProgress) error {
	var product database.Product
	if err := s.DB.First(&product, "id = ?", productID).Error; err != nil {
		return fmt.Errorf("%w: product %s", ErrNotFound, productID)
	}
	if err := s.SyncSourceProducts(product.SourceID); err != nil {
		return fmt.Errorf("failed to sync products for source %s: %w", product.SourceID, err)
	}
	res, err := s.SyncProduct(ctx, productID)
	if progress != nil {
		progress(SyncEvent{
			ProductID: product.ID, ProductName: product.Name,
			Deliveries: res.Deliveries, Files: res.Files, NewFiles: len(res.NewFileIDs),
			Err: err,
		})
	}
	return err
}

// RunScheduledSync is the scheduler's job: sync a product and, when
// auto-download is on, download its new files with the bounded worker pool.
// It stops when the service context is cancelled on shutdown.
func (s *Service) RunScheduledSync(productID string) {
	ctx := s.ctx
	res, err := s.syncProduct(ctx, productID, true)
	if err != nil {
		slog.Error("Scheduled sync failed", "productID", productID, "error", err)
	}

	var product database.Product
	if err := s.DB.First(&product, "id = ?", productID).Error; err != nil || !product.AutoDownload || len(res.NewFileIDs) == 0 {
		return
	}
	if _, err := s.Downloader.Enqueue(res.NewFileIDs, "schedule"); err != nil {
		slog.Error("Failed to queue new files", "productID", productID, "error", err)
	}
}
