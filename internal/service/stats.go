package service

import (
	"context"
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

// HealthCheck reports whether the database answers, whether the scheduler
// runs, and how many downloads are active. An unreachable database makes
// the service unhealthy.
func (s *Service) HealthCheck(ctx context.Context) generated.HealthResponse {
	uptime := time.Since(s.startTime).String()
	version := s.Version
	if version == "" {
		version = "dev"
	}

	status := generated.Healthy
	database := generated.Ok
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if sqlDB, err := s.DB.DB.DB(); err != nil || sqlDB.PingContext(pingCtx) != nil {
		status = generated.Unhealthy
		database = generated.Unavailable
	}
	scheduler := generated.Disabled
	if s.Scheduler != nil && s.Scheduler.Running() {
		scheduler = generated.Running
	}
	active := len(s.Downloader.ActiveDownloads())

	health := generated.HealthResponse{
		Status:          status,
		Uptime:          &uptime,
		Version:         &version,
		Database:        &database,
		Scheduler:       &scheduler,
		ActiveDownloads: &active,
	}
	if status != generated.Unhealthy {
		if q, err := s.Downloader.QueueCounts(); err == nil {
			health.Queue = &generated.QueueCounts{Queued: q.Queued, Running: q.Running, Retrying: q.Retrying, Failed: q.Failed}
		}
	}
	return health
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
