package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/scheduler"
)

type productCounts struct {
	ProductID       string
	TotalFiles      int64
	DownloadedFiles int64
	FailedFiles     int64
}

func (s *Service) ListProducts(sourceID *string) ([]generated.Product, error) {
	var products []database.Product
	query := s.DB.DB

	if sourceID != nil {
		query = query.Where("source_id = ?", *sourceID)
	}

	if err := query.Order("name ASC").Find(&products).Error; err != nil {
		return nil, fmt.Errorf("failed to list products: %w", err)
	}

	counts, err := s.batchProductCounts()
	if err != nil {
		return nil, err
	}

	result := make([]generated.Product, 0, len(products))
	for _, p := range products {
		product := convertProduct(p)
		c := counts[p.ID]
		tf := int(c.TotalFiles)
		df := int(c.DownloadedFiles)
		ff := int(c.FailedFiles)
		product.TotalFiles = &tf
		product.DownloadedFiles = &df
		product.FailedFiles = &ff
		result = append(result, product)
	}

	return result, nil
}

func (s *Service) batchProductCounts() (map[string]productCounts, error) {
	result := make(map[string]productCounts)

	var totalRows []struct {
		ProductID string
		Count     int64
	}
	if err := s.DB.Model(&database.File{}).
		Select("product_id, COUNT(*) as count").
		Group("product_id").
		Find(&totalRows).Error; err != nil {
		return nil, fmt.Errorf("failed to count files: %w", err)
	}
	for _, r := range totalRows {
		c := result[r.ProductID]
		c.ProductID = r.ProductID
		c.TotalFiles = r.Count
		result[r.ProductID] = c
	}

	var dlRows []struct {
		ProductID string
		Count     int64
	}
	if err := s.DB.Raw(`
		SELECT f.product_id, COUNT(DISTINCT de.file_id) as count
		FROM download_entries de
		JOIN files f ON f.id = de.file_id
		WHERE de.status = 'completed'
		GROUP BY f.product_id
	`).Scan(&dlRows).Error; err != nil {
		return nil, fmt.Errorf("failed to count downloaded files: %w", err)
	}
	for _, r := range dlRows {
		c := result[r.ProductID]
		c.DownloadedFiles = r.Count
		result[r.ProductID] = c
	}

	var failRows []struct {
		ProductID string
		Count     int64
	}
	if err := s.DB.Raw(`
		SELECT f.product_id, COUNT(DISTINCT de.file_id) as count
		FROM download_entries de
		JOIN files f ON f.id = de.file_id
		WHERE de.status = 'failed'
		AND de.id = (SELECT MAX(de2.id) FROM download_entries de2 WHERE de2.file_id = de.file_id)
		GROUP BY f.product_id
	`).Scan(&failRows).Error; err != nil {
		return nil, fmt.Errorf("failed to count failed files: %w", err)
	}
	for _, r := range failRows {
		c := result[r.ProductID]
		c.FailedFiles = r.Count
		result[r.ProductID] = c
	}

	return result, nil
}

func (s *Service) GetProduct(id string) (generated.ProductWithDeliveries, error) {
	var product database.Product
	if err := s.DB.Preload("Deliveries.Files").First(&product, "id = ?", id).Error; err != nil {
		return generated.ProductWithDeliveries{}, fmt.Errorf("%w: product %s", ErrNotFound, id)
	}

	p := convertProduct(product)
	result := generated.ProductWithDeliveries{
		Id:               p.Id,
		SourceId:         p.SourceId,
		Name:             p.Name,
		AutoDownload:     p.AutoDownload,
		ExternalId:       p.ExternalId,
		Description:      p.Description,
		CheckWindowStart: p.CheckWindowStart,
		NextRun:          p.NextRun,
		LastCheckedAt:    p.LastCheckedAt,
		TotalFiles:       p.TotalFiles,
		DownloadedFiles:  p.DownloadedFiles,
		FailedFiles:      p.FailedFiles,
	}

	deliveries := make([]generated.Delivery, 0, len(product.Deliveries))
	for _, d := range product.Deliveries {
		deliveries = append(deliveries, convertDelivery(d))
	}
	result.Deliveries = &deliveries

	return result, nil
}

// TriggerProductSync starts a product sync: in the background on the server,
// synchronously otherwise.
func (s *Service) TriggerProductSync(ctx context.Context, id string) error {
	var product database.Product
	if err := s.DB.First(&product, "id = ?", id).Error; err != nil {
		return fmt.Errorf("%w: product %s", ErrNotFound, id)
	}
	if s.Scheduler != nil {
		s.goBackground(func(context.Context) { s.RunScheduledSync(id) })
		return nil
	}
	return s.SyncProductFull(ctx, id, nil)
}

func (s *Service) UpdateProductSchedule(productID string, req generated.UpdateScheduleRequest) (generated.ProductSchedule, error) {
	var product database.Product
	if err := s.DB.First(&product, "id = ?", productID).Error; err != nil {
		return generated.ProductSchedule{}, fmt.Errorf("%w: product %s", ErrNotFound, productID)
	}

	wasAutoDownload := product.AutoDownload

	if req.AutoDownload != nil {
		product.AutoDownload = *req.AutoDownload
	}
	if req.CheckWindowStart != nil {
		product.CheckWindowStart = *req.CheckWindowStart
	}
	if req.CheckWindowEnd != nil {
		product.CheckWindowEnd = *req.CheckWindowEnd
	}

	// Validate a newly given schedule, and the stored one whenever it will
	// run. An old schedule that no longer parses must not block disabling
	// auto-download or clearing it.
	if (req.CheckWindowStart != nil || product.AutoDownload) && product.CheckWindowStart != "" {
		if _, err := scheduler.NextRun(product.CheckWindowStart, time.Now()); err != nil {
			return generated.ProductSchedule{}, fmt.Errorf("%w: %v", ErrInvalidSchedule, err)
		}
	}

	// Only the schedule columns: a full save would overwrite a sync's
	// last_checked_at written meanwhile.
	if err := s.DB.Model(&database.Product{}).Where("id = ?", product.ID).Updates(map[string]any{
		"auto_download":      product.AutoDownload,
		"check_window_start": product.CheckWindowStart,
		"check_window_end":   product.CheckWindowEnd,
	}).Error; err != nil {
		return generated.ProductSchedule{}, fmt.Errorf("failed to update schedule: %w", err)
	}

	// The server runs a schedule only while auto-download is on, as on
	// startup (loadSchedules).
	if s.Scheduler != nil {
		if product.AutoDownload {
			if err := s.Scheduler.ScheduleProduct(&product); err != nil {
				return generated.ProductSchedule{}, fmt.Errorf("%w: %v", ErrInvalidSchedule, err)
			}
		} else {
			s.Scheduler.UnscheduleProduct(product.ID)
		}
	}

	// If auto-download was just disabled, stop the downloads it started
	// (queued by enabling it or by scheduled syncs), not ones a user asked for.
	if wasAutoDownload && !product.AutoDownload {
		s.stopAutoDownloads(product.ID)
	}

	// If auto-download was just enabled, queue the pending files: a running
	// server downloads them, or the CLI's download all.
	if product.AutoDownload && !wasAutoDownload {
		if err := s.DownloadPendingFiles(product.ID); err != nil {
			slog.Error("Failed to queue pending downloads", "productID", product.ID, "error", err)
		}
	}

	schedule := generated.ProductSchedule{
		ProductId:    product.ID,
		ProductName:  product.Name,
		AutoDownload: product.AutoDownload,
	}
	if product.CheckWindowStart != "" {
		schedule.CheckWindowStart = &product.CheckWindowStart
	}
	schedule.NextRun = nextRun(product)

	return schedule, nil
}

// nextRun is the product's next scheduled sync, or nil when it has none.
func nextRun(p database.Product) *time.Time {
	if !p.AutoDownload || p.CheckWindowStart == "" {
		return nil
	}
	next, err := scheduler.NextRun(p.CheckWindowStart, time.Now())
	if err != nil {
		return nil
	}
	return &next
}

// PendingFileCount counts a product's files that are neither skipped nor
// downloaded.
func (s *Service) PendingFileCount(productID string) (int64, error) {
	var n int64
	err := s.DB.Model(&database.File{}).
		Where("product_id = ? AND skipped = ?", productID, false).
		Where("id NOT IN (SELECT file_id FROM download_entries WHERE status = ?)", database.DownloadStatusCompleted).
		Count(&n).Error
	return n, err
}

// DownloadPendingFiles queues every file of a product that is neither
// skipped nor downloaded.
func (s *Service) DownloadPendingFiles(productID string) error {
	ids, err := s.pendingFileIDs(productID)
	if err != nil {
		return err
	}
	n, err := s.Downloader.Enqueue(ids, "auto")
	if err != nil {
		return err
	}
	slog.Info("Queued pending downloads", "productID", productID, "count", n)
	return nil
}

func (s *Service) stopAutoDownloads(productID string) {
	var ids []string
	if err := s.DB.Model(&database.DownloadJob{}).
		Joins("JOIN files ON files.id = download_jobs.file_id").
		Where("files.product_id = ? AND download_jobs.requested_by IN ?", productID, []string{"auto", "schedule"}).
		Pluck("download_jobs.file_id", &ids).Error; err != nil {
		slog.Error("Failed to find auto-download jobs", "productID", productID, "error", err)
		return
	}
	for _, id := range ids {
		if err := s.CancelDownload(id); err != nil && !errors.Is(err, ErrNotFound) {
			slog.Error("Failed to stop auto-download", "fileID", id, "error", err)
		}
	}
	if len(ids) > 0 {
		slog.Info("Stopped auto-downloads", "productID", productID, "count", len(ids))
	}
}

func (s *Service) pendingFileIDs(productID string) ([]string, error) {
	var ids []string
	err := s.DB.Model(&database.File{}).
		Where("product_id = ? AND skipped = ?", productID, false).
		Where("id NOT IN (SELECT file_id FROM download_entries WHERE status = ?)", database.DownloadStatusCompleted).
		Pluck("id", &ids).Error
	return ids, err
}

func convertProduct(p database.Product) generated.Product {
	result := generated.Product{
		Id:           p.ID,
		SourceId:     p.SourceID,
		Name:         p.Name,
		AutoDownload: p.AutoDownload,
	}
	if p.ExternalID != "" {
		result.ExternalId = &p.ExternalID
	}
	if p.Description != "" {
		result.Description = &p.Description
	}
	if p.CheckWindowStart != "" {
		result.CheckWindowStart = &p.CheckWindowStart
	}
	result.NextRun = nextRun(p)
	if p.LastCheckedAt != nil {
		result.LastCheckedAt = p.LastCheckedAt
	}
	return result
}

func convertDelivery(d database.Delivery) generated.Delivery {
	result := generated.Delivery{
		Id:        d.ID,
		ProductId: d.ProductID,
		Name:      d.Name,
	}
	if d.ExternalID != "" {
		result.ExternalId = &d.ExternalID
	}
	if d.PublishedAt != nil {
		result.PublishedAt = d.PublishedAt
	}
	if d.ExpiresAt != nil {
		result.ExpiresAt = d.ExpiresAt
	}
	return result
}
