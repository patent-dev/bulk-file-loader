package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"gorm.io/gorm"
)

type FileListResult struct {
	Files []generated.File
	Total int
}

func (s *Service) ListFiles(sourceID, productID *string, status *generated.ListFilesParamsStatus, offset, limit int) (FileListResult, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	var files []database.File
	query := s.DB.Model(&database.File{})

	if sourceID != nil {
		query = query.Where("source_id = ?", *sourceID)
	}
	if productID != nil {
		query = query.Where("product_id = ?", *productID)
	}

	if status == nil {
		var total int64
		if err := query.Count(&total).Error; err != nil {
			return FileListResult{}, fmt.Errorf("failed to count files: %w", err)
		}
		if err := query.Offset(offset).Limit(limit).Order("created_at DESC").Find(&files).Error; err != nil {
			return FileListResult{}, fmt.Errorf("failed to list files: %w", err)
		}
		statuses := batchDeriveFileStatuses(files, s.DB)
		result := make([]generated.File, 0, len(files))
		for _, f := range files {
			result = append(result, convertFileWithStatus(f, statuses[f.ID]))
		}
		return FileListResult{Files: result, Total: int(total)}, nil
	}

	// Status is derived (not a DB column), so we must fetch all files, derive
	// statuses in batch, filter, then apply pagination manually.
	if err := query.Order("created_at DESC").Find(&files).Error; err != nil {
		return FileListResult{}, fmt.Errorf("failed to list files: %w", err)
	}

	statuses := batchDeriveFileStatuses(files, s.DB)
	var filtered []generated.File
	for _, f := range files {
		cf := convertFileWithStatus(f, statuses[f.ID])
		if string(cf.Status) == string(*status) {
			filtered = append(filtered, cf)
		}
	}

	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return FileListResult{Files: filtered[offset:end], Total: total}, nil
}

func (s *Service) GetFile(id string) (generated.FileWithHistory, error) {
	var file database.File
	if err := s.DB.Preload("DownloadEntries").First(&file, "id = ?", id).Error; err != nil {
		return generated.FileWithHistory{}, fmt.Errorf("file not found: %w", err)
	}

	f := convertFile(file, s.DB)
	result := generated.FileWithHistory{
		Id:               f.Id,
		FileName:         f.FileName,
		FileSize:         f.FileSize,
		Status:           generated.FileWithHistoryStatus(f.Status),
		ErrorMessage:     f.ErrorMessage,
		DeliveryId:       f.DeliveryId,
		ProductId:        f.ProductId,
		SourceId:         f.SourceId,
		ExpectedChecksum: f.ExpectedChecksum,
		ReleasedAt:       f.ReleasedAt,
		Skipped:          f.Skipped,
	}

	history := make([]generated.DownloadEntry, 0, len(file.DownloadEntries))
	for _, e := range file.DownloadEntries {
		history = append(history, convertDownloadEntry(e))
	}
	result.DownloadHistory = &history

	return result, nil
}

func (s *Service) DeleteFile(id string) error {
	var entry database.DownloadEntry
	if err := s.DB.Where("file_id = ? AND status = ?", id, "completed").Order("completed_at DESC").First(&entry).Error; err != nil {
		return fmt.Errorf("%w: no downloaded file found", ErrNotFound)
	}

	if entry.LocalPath != "" {
		if err := os.Remove(entry.LocalPath); err != nil && !os.IsNotExist(err) {
			slog.Error("Failed to delete file", "path", entry.LocalPath, "error", err)
			return fmt.Errorf("failed to delete file")
		}
	}

	if err := s.DB.Model(&entry).Update("status", "deleted").Error; err != nil {
		return fmt.Errorf("failed to update file status to deleted: %w", err)
	}

	slog.Info("File deleted", "fileID", id, "path", entry.LocalPath)
	return nil
}

func (s *Service) DownloadFile(id string) {
	go func() {
		if err := s.Downloader.Download(context.Background(), id); err != nil {
			slog.Error("Async download failed", "fileID", id, "error", err)
		}
	}()
}

func (s *Service) CancelDownload(id string) error {
	return s.Downloader.Cancel(id)
}

func (s *Service) SkipFile(id string) error {
	result := s.DB.Model(&database.File{}).Where("id = ?", id).Update("skipped", true)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: file %s", ErrNotFound, id)
	}
	return nil
}

func (s *Service) UnskipFile(id string) error {
	result := s.DB.Model(&database.File{}).Where("id = ?", id).Update("skipped", false)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: file %s", ErrNotFound, id)
	}
	return nil
}

// NOTE: There is a small TOCTOU race between the IsActive check and the delete.
// In the worst case, a download that starts between the check and delete will have
// its entry removed, causing it to appear as "available" again. This is acceptable
// since the window is very narrow and the consequence is a re-download, not data loss.
func (s *Service) ResetFile(id string) error {
	if s.Downloader.IsActive(id) {
		return fmt.Errorf("%w: cannot reset file while download is in progress", ErrConflict)
	}

	var rowsAffected int64
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("file_id = ?", id).Delete(&database.DownloadEntry{})
		if result.Error != nil {
			return fmt.Errorf("failed to reset file: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("%w: no download history found", ErrNotFound)
		}
		rowsAffected = result.RowsAffected

		if err := tx.Model(&database.File{}).Where("id = ?", id).Update("skipped", false).Error; err != nil {
			return fmt.Errorf("failed to unskip file: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	slog.Info("File reset", "fileID", id, "entriesRemoved", rowsAffected)
	return nil
}

func convertFile(f database.File, db *database.DB) generated.File {
	s, errorMsg := deriveFileStatusAndError(f, db)
	return convertFileWithStatus(f, fileStatus{status: s, errorMsg: errorMsg})
}

func convertFileWithStatus(f database.File, fs fileStatus) generated.File {
	result := generated.File{
		Id:       f.ID,
		FileName: f.FileName,
		FileSize: &f.FileSize,
		Status:   generated.FileStatus(fs.status),
	}
	if fs.errorMsg != "" {
		result.ErrorMessage = &fs.errorMsg
	}
	if f.DeliveryID != "" {
		result.DeliveryId = &f.DeliveryID
	}
	if f.ProductID != "" {
		result.ProductId = &f.ProductID
	}
	if f.SourceID != "" {
		result.SourceId = &f.SourceID
	}
	if f.ExpectedChecksum != "" {
		result.ExpectedChecksum = &f.ExpectedChecksum
	}
	if f.ReleasedAt != nil {
		result.ReleasedAt = f.ReleasedAt
	}
	result.Skipped = &f.Skipped
	return result
}

type fileStatus struct {
	status   string
	errorMsg string
}

func deriveFileStatusAndError(f database.File, db *database.DB) (string, string) {
	var entry database.DownloadEntry
	err := db.Where("file_id = ?", f.ID).Order("created_at DESC").First(&entry).Error
	if err == nil {
		return resolveEntryStatus(entry, f.Skipped)
	}

	if f.Skipped {
		return "skipped", ""
	}

	return "available", ""
}

func resolveEntryStatus(entry database.DownloadEntry, skipped bool) (string, string) {
	switch entry.Status {
	case database.DownloadStatusDownloading:
		return "downloading", ""
	case database.DownloadStatusCompleted:
		if entry.LocalPath != "" {
			if _, err := os.Stat(entry.LocalPath); err == nil {
				return "downloaded", ""
			}
		}
		return "deleted", ""
	case database.DownloadStatusFailed:
		return "failed", entry.ErrorMessage
	case database.DownloadStatusCancelled:
		return "cancelled", ""
	}

	if skipped {
		return "skipped", ""
	}
	return "available", ""
}

func batchDeriveFileStatuses(files []database.File, db *database.DB) map[string]fileStatus {
	result := make(map[string]fileStatus, len(files))
	if len(files) == 0 {
		return result
	}

	fileIDs := make([]string, len(files))
	skippedMap := make(map[string]bool, len(files))
	for i, f := range files {
		fileIDs[i] = f.ID
		skippedMap[f.ID] = f.Skipped
	}

	// Fetch the latest download entry per file in one query using a subquery
	var entries []database.DownloadEntry
	if err := db.Where("id IN (SELECT MAX(id) FROM download_entries WHERE file_id IN ? GROUP BY file_id)", fileIDs).
		Find(&entries).Error; err != nil {
		slog.Error("Failed to batch query file statuses", "error", err)
	}

	entryMap := make(map[string]database.DownloadEntry, len(entries))
	for _, e := range entries {
		entryMap[e.FileID] = e
	}

	for _, f := range files {
		if entry, ok := entryMap[f.ID]; ok {
			s, msg := resolveEntryStatus(entry, f.Skipped)
			result[f.ID] = fileStatus{status: s, errorMsg: msg}
		} else if f.Skipped {
			result[f.ID] = fileStatus{status: "skipped"}
		} else {
			result[f.ID] = fileStatus{status: "available"}
		}
	}
	return result
}

func convertDownloadEntry(e database.DownloadEntry) generated.DownloadEntry {
	result := generated.DownloadEntry{
		Id:     int(e.ID),
		FileId: e.FileID,
		Status: generated.DownloadEntryStatus(e.Status),
	}
	if e.Progress > 0 {
		result.Progress = &e.Progress
	}
	if e.TotalBytes > 0 {
		result.TotalBytes = &e.TotalBytes
	}
	if e.LocalPath != "" {
		result.LocalPath = &e.LocalPath
	}
	if e.LocalChecksum != "" {
		result.LocalChecksum = &e.LocalChecksum
	}
	if e.ErrorMessage != "" {
		result.ErrorMessage = &e.ErrorMessage
	}
	if e.StartedAt != nil {
		result.StartedAt = e.StartedAt
	}
	if e.CompletedAt != nil {
		result.CompletedAt = e.CompletedAt
	}
	return result
}
