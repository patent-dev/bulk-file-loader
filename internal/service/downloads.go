package service

import (
	"fmt"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/downloader"
)

type DownloadListResult struct {
	Downloads []generated.DownloadEntry
	Total     int
}

func (s *Service) ListDownloads(status *string, offset, limit int) (DownloadListResult, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	var entries []database.DownloadEntry
	var total int64

	query := s.DB.Model(&database.DownloadEntry{})

	if status != nil {
		query = query.Where("status = ?", *status)
	}

	if err := query.Count(&total).Error; err != nil {
		return DownloadListResult{}, fmt.Errorf("failed to count downloads: %w", err)
	}

	if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&entries).Error; err != nil {
		return DownloadListResult{}, fmt.Errorf("failed to list downloads: %w", err)
	}

	result := make([]generated.DownloadEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, convertDownloadEntry(e))
	}

	return DownloadListResult{Downloads: result, Total: int(total)}, nil
}

func (s *Service) ActiveDownloads() []downloader.DownloadProgress {
	return s.Downloader.ActiveDownloads()
}

func (s *Service) StatusVersion() uint64 {
	return s.Downloader.StatusVersion()
}
