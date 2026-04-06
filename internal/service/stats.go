package service

import (
	"fmt"
	"time"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/database"
)

func (s *Service) GetStats() (generated.StatsResponse, error) {
	var totalFiles, downloadedFiles, pendingFiles int64
	var enabledSources int64

	if err := s.DB.Model(&database.File{}).Count(&totalFiles).Error; err != nil {
		return generated.StatsResponse{}, fmt.Errorf("failed to count files: %w", err)
	}
	if err := s.DB.Model(&database.Source{}).Where("enabled = ?", true).Count(&enabledSources).Error; err != nil {
		return generated.StatsResponse{}, fmt.Errorf("failed to count sources: %w", err)
	}
	if err := s.DB.Model(&database.DownloadEntry{}).Where("status = ?", "completed").
		Distinct("file_id").Count(&downloadedFiles).Error; err != nil {
		return generated.StatsResponse{}, fmt.Errorf("failed to count downloaded files: %w", err)
	}
	if err := s.DB.Model(&database.File{}).
		Joins("JOIN products ON products.id = files.product_id").
		Where("products.auto_download = ?", true).
		Where("files.skipped = ?", false).
		Where("files.id NOT IN (SELECT DISTINCT file_id FROM download_entries)").
		Count(&pendingFiles).Error; err != nil {
		return generated.StatsResponse{}, fmt.Errorf("failed to count pending files: %w", err)
	}

	activeDownloads := len(s.Downloader.ActiveDownloads())

	tf := int(totalFiles)
	df := int(downloadedFiles)
	pf := int(pendingFiles)
	ad := activeDownloads
	es := int(enabledSources)

	return generated.StatsResponse{
		TotalFiles:      &tf,
		DownloadedFiles: &df,
		PendingFiles:    &pf,
		ActiveDownloads: &ad,
		EnabledSources:  &es,
	}, nil
}

func (s *Service) HealthCheck() generated.HealthResponse {
	uptime := time.Since(s.startTime).String()
	version := s.Version
	if version == "" {
		version = "dev"
	}

	return generated.HealthResponse{
		Status:  "healthy",
		Uptime:  &uptime,
		Version: &version,
	}
}

func (s *Service) GetSchedule() ([]generated.ProductSchedule, error) {
	var products []database.Product
	if err := s.DB.Find(&products).Error; err != nil {
		return nil, err
	}

	result := make([]generated.ProductSchedule, 0, len(products))
	for _, p := range products {
		schedule := generated.ProductSchedule{
			ProductId:    p.ID,
			ProductName:  p.Name,
			AutoDownload: p.AutoDownload,
		}
		if p.CheckWindowStart != "" {
			schedule.CheckWindowStart = &p.CheckWindowStart
		}
		if p.CheckWindowEnd != "" {
			schedule.CheckWindowEnd = &p.CheckWindowEnd
		}
		if s.Scheduler != nil {
			if nextRun := s.Scheduler.GetNextRun(p.ID); nextRun != nil {
				schedule.NextRun = nextRun
			}
		}
		result = append(result, schedule)
	}

	return result, nil
}
