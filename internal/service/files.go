package service

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"time"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"gorm.io/gorm"
)

type FileListResult struct {
	Files []generated.File
	Total int
}

// FileFilter selects files. Empty fields do not filter.
type FileFilter struct {
	SourceID       string
	ProductID      string
	Status         string     // derived file status, e.g. available or downloaded
	ReleasedAfter  *time.Time // inclusive
	ReleasedBefore *time.Time // exclusive
	NameGlob       string     // path.Match pattern matched against the file name
}

// ListFiles lists files for the API: at most 500 per page, newest first.
func (s *Service) ListFiles(sourceID, productID *string, status *generated.ListFilesParamsStatus, offset, limit int) (FileListResult, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	var f FileFilter
	if sourceID != nil {
		f.SourceID = *sourceID
	}
	if productID != nil {
		f.ProductID = *productID
	}
	if status != nil {
		f.Status = string(*status)
	}
	return s.ListFilesFiltered(f, offset, limit)
}

// ListFilesFiltered lists the files matching f, newest first. A limit of 0
// or less returns every match.
func (s *Service) ListFilesFiltered(f FileFilter, offset, limit int) (FileListResult, error) {
	if offset < 0 {
		offset = 0
	}
	if f.NameGlob != "" {
		if _, err := path.Match(f.NameGlob, ""); err != nil {
			return FileListResult{}, fmt.Errorf("invalid name pattern %q: %w", f.NameGlob, err)
		}
	}

	query := s.DB.Model(&database.File{})
	if f.SourceID != "" {
		query = query.Where("source_id = ?", f.SourceID)
	}
	if f.ProductID != "" {
		query = query.Where("product_id = ?", f.ProductID)
	}
	// UTC on both sides: release dates are stored in UTC, and SQLite compares
	// times as text.
	if f.ReleasedAfter != nil {
		query = query.Where("released_at >= ?", f.ReleasedAfter.UTC())
	}
	if f.ReleasedBefore != nil {
		query = query.Where("released_at < ?", f.ReleasedBefore.UTC())
	}
	query = query.Order("created_at DESC").Order("id")

	var files []database.File
	if f.Status == "" && f.NameGlob == "" {
		var total int64
		if err := query.Count(&total).Error; err != nil {
			return FileListResult{}, fmt.Errorf("failed to count files: %w", err)
		}
		page := query.Offset(offset)
		if limit > 0 {
			page = page.Limit(limit)
		}
		if err := page.Find(&files).Error; err != nil {
			return FileListResult{}, fmt.Errorf("failed to list files: %w", err)
		}
		statuses, err := batchDeriveFileStatuses(files, s.DB)
		if err != nil {
			return FileListResult{}, err
		}
		result := make([]generated.File, 0, len(files))
		for _, file := range files {
			result = append(result, convertFileWithStatus(file, statuses[file.ID]))
		}
		return FileListResult{Files: result, Total: int(total)}, nil
	}

	// Status is derived (not a DB column), so we must fetch all files, derive
	// statuses in batch, filter, then apply pagination manually.
	if err := query.Find(&files).Error; err != nil {
		return FileListResult{}, fmt.Errorf("failed to list files: %w", err)
	}
	statuses, err := batchDeriveFileStatuses(files, s.DB)
	if err != nil {
		return FileListResult{}, err
	}
	var filtered []generated.File
	for _, file := range files {
		if f.NameGlob != "" {
			if ok, _ := path.Match(f.NameGlob, file.FileName); !ok {
				continue
			}
		}
		cf := convertFileWithStatus(file, statuses[file.ID])
		if f.Status != "" && string(cf.Status) != f.Status {
			continue
		}
		filtered = append(filtered, cf)
	}

	total := len(filtered)
	offset = min(offset, total)
	end := total
	if limit > 0 {
		end = min(offset+limit, total)
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
		Attempts:         f.Attempts,
		MaxAttempts:      f.MaxAttempts,
		NextAttemptAt:    f.NextAttemptAt,
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

// DownloadFile queues a file for download; a given-up file starts over.
func (s *Service) DownloadFile(id string) error {
	_, err := s.Downloader.Enqueue([]string{id}, "api")
	return err
}

// CancelDownload stops a file's download wherever it runs (in this process
// directly, in another one through a request its worker sees within 30
// seconds), or removes its waiting job.
func (s *Service) CancelDownload(id string) error {
	if err := s.Downloader.Cancel(id); err == nil {
		return nil
	}
	running, err := s.Downloader.RequestCancel(id)
	if err != nil || running {
		return err
	}
	removed, err := s.Downloader.RemoveJobs([]string{id})
	if err != nil {
		return err
	}
	if removed == 0 {
		return fmt.Errorf("%w: no queued or running download for %s", ErrNotFound, id)
	}
	return nil
}

// SkipFile marks a file as skipped and drops its waiting job.
func (s *Service) SkipFile(id string) error {
	result := s.DB.Model(&database.File{}).Where("id = ?", id).Update("skipped", true)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: file %s", ErrNotFound, id)
	}
	// A skipped file is not downloaded: drop its job, stop a running one.
	if err := s.CancelDownload(id); err != nil && !errors.Is(err, ErrNotFound) {
		return err
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
	var running int64
	s.DB.Model(&database.DownloadJob{}).Where("file_id = ? AND state = ?", id, database.JobRunning).Count(&running)
	if s.Downloader.IsActive(id) || running > 0 {
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
	if _, err := s.Downloader.RemoveJobs([]string{id}); err != nil {
		return err
	}

	slog.Info("File reset", "fileID", id, "entriesRemoved", rowsAffected)
	return nil
}

func convertFile(f database.File, db *database.DB) generated.File {
	return convertFileWithStatus(f, deriveFileStatus(f, db))
}

func convertFileWithStatus(f database.File, fs fileStatus) generated.File {
	// Sources that do not list sizes (crawled manuals, directory listings)
	// report 0; once downloaded, the received size is known.
	size := f.FileSize
	if size == 0 {
		size = fs.downloadedBytes
	}
	result := generated.File{
		Id:       f.ID,
		FileName: f.FileName,
		FileSize: &size,
		Status:   generated.FileStatus(fs.status),
	}
	if fs.errorMsg != "" {
		result.ErrorMessage = &fs.errorMsg
	}
	if fs.job != nil {
		result.Attempts = &fs.job.Attempts
		result.MaxAttempts = &fs.job.MaxAttempts
		if fs.job.State == database.JobRetrying {
			result.NextAttemptAt = &fs.job.NextAttemptAt
		}
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
	status          string
	errorMsg        string
	downloadedBytes int64                 // size of the downloaded file on disk, 0 otherwise
	job             *database.DownloadJob // the file's download job, if any
}

// withJob lets a download job decide the status: a file in the queue is
// queued, downloading or retrying, and a given-up file is failed, whatever
// its last attempt left behind.
func (fs fileStatus) withJob(job *database.DownloadJob) fileStatus {
	if job == nil {
		return fs
	}
	fs.job = job
	switch job.State {
	case database.JobQueued:
		fs.status, fs.errorMsg = "queued", ""
	case database.JobRunning:
		fs.status, fs.errorMsg = "downloading", ""
		if job.LeaseUntil == nil || job.LeaseUntil.Before(time.Now()) {
			// Its process died; the next worker takes it over.
			fs.status = "queued"
		}
	case database.JobRetrying:
		fs.status, fs.errorMsg = "retrying", job.LastError
	case database.JobFailed:
		fs.status, fs.errorMsg = "failed", job.LastError
	}
	return fs
}

func statusFromEntry(entry database.DownloadEntry, skipped bool) fileStatus {
	s, msg, size := resolveEntryStatus(entry, skipped)
	return fileStatus{status: s, errorMsg: msg, downloadedBytes: size}
}

func statusWithoutEntry(skipped bool) fileStatus {
	if skipped {
		return fileStatus{status: "skipped"}
	}
	return fileStatus{status: "available"}
}

// deriveFileStatus resolves a file's status from its latest download entry.
func deriveFileStatus(f database.File, db *database.DB) fileStatus {
	var job *database.DownloadJob
	var j database.DownloadJob
	if db.Where("file_id = ?", f.ID).Limit(1).Find(&j).RowsAffected == 1 {
		job = &j
	}
	var entry database.DownloadEntry
	if err := db.Where("file_id = ?", f.ID).Order("created_at DESC").First(&entry).Error; err != nil {
		return statusWithoutEntry(f.Skipped).withJob(job)
	}
	return statusFromEntry(entry, f.Skipped).withJob(job)
}

// resolveEntryStatus maps a download entry to a file status, its error
// message and, for a file still on disk, its size there.
func resolveEntryStatus(entry database.DownloadEntry, skipped bool) (status, errMsg string, size int64) {
	switch entry.Status {
	case database.DownloadStatusDownloading:
		return "downloading", "", 0
	case database.DownloadStatusCompleted:
		if entry.LocalPath != "" {
			if fi, err := os.Stat(entry.LocalPath); err == nil {
				return "downloaded", "", fi.Size()
			}
		}
		return "deleted", "", 0
	case database.DownloadStatusFailed:
		return "failed", entry.ErrorMessage, 0
	case database.DownloadStatusCancelled:
		return "cancelled", "", 0
	}

	if skipped {
		return "skipped", "", 0
	}
	return "available", "", 0
}

// statusQueryChunk bounds the IDs per status query: SQLite allows 32766
// parameters per statement, PostgreSQL and MySQL 65535.
const statusQueryChunk = 1000

// batchDeriveFileStatuses resolves the status of many files with one query
// per chunk of files. A failed query is an error: guessing "available" would
// make bulk commands act on downloaded files.
func batchDeriveFileStatuses(files []database.File, db *database.DB) (map[string]fileStatus, error) {
	result := make(map[string]fileStatus, len(files))
	entryMap := make(map[string]database.DownloadEntry, len(files))
	jobMap := make(map[string]*database.DownloadJob)
	for start := 0; start < len(files); start += statusQueryChunk {
		chunk := files[start:min(start+statusQueryChunk, len(files))]
		fileIDs := make([]string, len(chunk))
		for i, f := range chunk {
			fileIDs[i] = f.ID
		}
		// The latest download entry per file, using a subquery.
		var entries []database.DownloadEntry
		if err := db.Where("id IN (SELECT MAX(id) FROM download_entries WHERE file_id IN ? GROUP BY file_id)", fileIDs).
			Find(&entries).Error; err != nil {
			return nil, fmt.Errorf("failed to query file statuses: %w", err)
		}
		for _, e := range entries {
			entryMap[e.FileID] = e
		}
		var jobs []database.DownloadJob
		if err := db.Where("file_id IN ?", fileIDs).Find(&jobs).Error; err != nil {
			return nil, fmt.Errorf("failed to query download jobs: %w", err)
		}
		for i := range jobs {
			jobMap[jobs[i].FileID] = &jobs[i]
		}
	}

	for _, f := range files {
		if entry, ok := entryMap[f.ID]; ok {
			result[f.ID] = statusFromEntry(entry, f.Skipped).withJob(jobMap[f.ID])
		} else {
			result[f.ID] = statusWithoutEntry(f.Skipped).withJob(jobMap[f.ID])
		}
	}
	return result, nil
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

// FileStatuses resolves the status of the given files, a chunk at a time.
// Unknown IDs are missing from the result.
func (s *Service) FileStatuses(ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	for start := 0; start < len(ids); start += statusQueryChunk {
		var files []database.File
		if err := s.DB.Where("id IN ?", ids[start:min(start+statusQueryChunk, len(ids))]).Find(&files).Error; err != nil {
			return nil, err
		}
		statuses, err := batchDeriveFileStatuses(files, s.DB)
		if err != nil {
			return nil, err
		}
		for id, st := range statuses {
			out[id] = st.status
		}
	}
	return out, nil
}
