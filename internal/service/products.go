package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/database"
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

func (s *Service) SyncProduct(ctx context.Context, id string) error {
	var product database.Product
	if err := s.DB.First(&product, "id = ?", id).Error; err != nil {
		return fmt.Errorf("%w: product %s", ErrNotFound, id)
	}
	if s.Scheduler != nil {
		return s.Scheduler.SyncNow(ctx, id)
	}
	return s.SyncProductFull(id)
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

	// Validate schedule before saving
	if s.Scheduler != nil {
		if err := s.Scheduler.ScheduleProduct(&product); err != nil {
			return generated.ProductSchedule{}, fmt.Errorf("%w: %v", ErrInvalidSchedule, err)
		}
	}

	if err := s.DB.Save(&product).Error; err != nil {
		return generated.ProductSchedule{}, fmt.Errorf("failed to update schedule: %w", err)
	}

	// If auto-download was just disabled, cancel in-progress downloads for this product
	if wasAutoDownload && !product.AutoDownload {
		if cancelled := s.Downloader.CancelByProduct(product.ID); cancelled > 0 {
			slog.Info("Cancelled active downloads", "productID", product.ID, "count", cancelled)
		}
	}

	// If auto-download was just enabled, trigger immediate download of pending files
	if product.AutoDownload && !wasAutoDownload {
		go s.DownloadPendingFiles(product.ID)
	}

	schedule := generated.ProductSchedule{
		ProductId:    product.ID,
		ProductName:  product.Name,
		AutoDownload: product.AutoDownload,
	}
	if product.CheckWindowStart != "" {
		schedule.CheckWindowStart = &product.CheckWindowStart
	}
	if s.Scheduler != nil {
		if nextRun := s.Scheduler.GetNextRun(product.ID); nextRun != nil {
			schedule.NextRun = nextRun
		}
	}

	return schedule, nil
}

func (s *Service) DownloadPendingFiles(productID string) {
	var files []database.File
	if err := s.DB.Where("product_id = ? AND skipped = ?", productID, false).Find(&files).Error; err != nil {
		slog.Error("Failed to query pending files", "productID", productID, "error", err)
		return
	}

	var pending []string
	for _, file := range files {
		var entry database.DownloadEntry
		err := s.DB.Where("file_id = ? AND status = ?", file.ID, database.DownloadStatusCompleted).First(&entry).Error
		if err == nil {
			continue
		}
		pending = append(pending, file.ID)
	}

	if len(pending) == 0 {
		return
	}

	ch := make(chan string, len(pending))
	for _, id := range pending {
		ch <- id
	}
	close(ch)

	workers := s.Downloader.MaxConcurrent()
	if workers <= 0 {
		workers = 3
	}

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range ch {
				if err := s.Downloader.Download(context.Background(), id); err != nil {
					slog.Error("Auto-download failed", "fileID", id, "error", err)
				}
			}
		}()
	}
	wg.Wait()
	slog.Info("Pending downloads completed", "productID", productID, "count", len(pending))
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
