package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/patent-dev/bulk-file-loader/config"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

var (
	ErrDownloadInProgress = errors.New("download already in progress")
	ErrFileNotFound       = errors.New("file not found")
	ErrSourceNotFound     = errors.New("source not found")
)

// activeDownload tracks a running download for cancellation
type activeDownload struct {
	cancel    context.CancelFunc
	productID string
}

// Downloader manages file downloads
type Downloader struct {
	db       *database.DB
	registry *sources.Registry
	hooks    *hooks.Manager
	cfg      *config.Config

	semaphore chan struct{}
	progress  *ProgressTracker
	active    sync.Map // fileID -> *activeDownload
}

// New creates a new downloader
func New(db *database.DB, registry *sources.Registry, hooks *hooks.Manager, cfg *config.Config) *Downloader {
	return &Downloader{
		db:        db,
		registry:  registry,
		hooks:     hooks,
		cfg:       cfg,
		semaphore: make(chan struct{}, cfg.MaxConcurrent),
		progress:  NewProgressTracker(),
	}
}

// Download starts downloading a file
func (d *Downloader) Download(ctx context.Context, fileID string) error {
	// Check if already downloading
	if _, exists := d.active.Load(fileID); exists {
		return ErrDownloadInProgress
	}

	// Get file from database
	var file database.File
	if err := d.db.Preload("Delivery.Product").First(&file, "id = ?", fileID).Error; err != nil {
		return ErrFileNotFound
	}

	// Get source adapter
	adapter, ok := d.registry.Get(file.SourceID)
	if !ok {
		return ErrSourceNotFound
	}

	// Create cancellable context
	ctx, cancel := context.WithTimeout(ctx, time.Duration(d.cfg.DownloadTimeout)*time.Second)

	// Store active download for cancellation
	d.active.Store(fileID, &activeDownload{cancel: cancel, productID: file.ProductID})
	defer func() {
		d.active.Delete(fileID)
		cancel()
	}()

	// Acquire semaphore
	select {
	case d.semaphore <- struct{}{}:
		defer func() { <-d.semaphore }()
	case <-ctx.Done():
		return ctx.Err()
	}

	// Create download entry
	now := time.Now()
	entry := &database.DownloadEntry{
		FileID:    fileID,
		Status:    database.DownloadStatusDownloading,
		StartedAt: &now,
	}
	if err := d.db.Create(entry).Error; err != nil {
		return fmt.Errorf("failed to create download entry: %w", err)
	}
	d.progress.IncrementStatusVersion()

	// Emit download started event
	d.emitEvent(hooks.EventDownloadStarted, &file, nil)

	// Prepare download path
	downloadPath := d.getDownloadPath(&file)
	if err := os.MkdirAll(filepath.Dir(downloadPath), 0755); err != nil {
		return d.handleError(entry, &file, "FILESYSTEM_ERROR", "Failed to create directory", err)
	}

	// Create temp file
	tempPath := downloadPath + ".tmp"
	tempFile, err := os.Create(tempPath)
	if err != nil {
		return d.handleError(entry, &file, "FILESYSTEM_ERROR", "Failed to create temp file", err)
	}

	// Track progress
	d.progress.Start(fileID, file.FileName, file.FileSize)
	defer d.progress.Complete(fileID)

	// Create hash writer for checksum
	hasher := sha256.New()
	writer := io.MultiWriter(tempFile, hasher)

	// Download file
	fileInfo := sources.FileInfo{
		ExternalID:        file.ExternalID,
		FileName:          file.FileName,
		FileSize:          file.FileSize,
		Checksum:          file.ExpectedChecksum,
		ChecksumAlgorithm: file.ChecksumAlgorithm,
		DownloadURI:       file.DownloadURI,
	}

	var entryMu sync.Mutex
	var lastDBUpdate time.Time
	err = adapter.DownloadFile(ctx, fileInfo, writer, func(bytesWritten, totalBytes int64) {
		d.progress.Update(fileID, bytesWritten, totalBytes)

		// Throttle database updates to reduce SQLite write contention
		entryMu.Lock()
		if time.Since(lastDBUpdate) >= 5*time.Second {
			entry.Progress = bytesWritten
			entry.TotalBytes = totalBytes
			d.db.Save(entry)
			lastDBUpdate = time.Now()
		}
		entryMu.Unlock()
	})

	_ = tempFile.Close()

	if err != nil {
		_ = os.Remove(tempPath)
		if ctx.Err() == context.Canceled {
			return d.handleCancelled(entry, &file)
		}
		return d.handleError(entry, &file, "DOWNLOAD_ERROR", "Download failed", err)
	}

	// Move temp file to final location
	if err := os.Rename(tempPath, downloadPath); err != nil {
		_ = os.Remove(tempPath)
		return d.handleError(entry, &file, "FILESYSTEM_ERROR", "Failed to move file", err)
	}

	// Calculate checksum
	localChecksum := "sha256:" + hex.EncodeToString(hasher.Sum(nil))

	// Update download entry
	completedAt := time.Now()
	entry.Status = database.DownloadStatusCompleted
	entry.LocalPath = downloadPath
	entry.LocalChecksum = localChecksum
	entry.CompletedAt = &completedAt
	if err := d.db.Save(entry).Error; err != nil {
		slog.Error("Failed to update download entry", "error", err)
	}
	d.progress.IncrementStatusVersion()

	d.emitCompletedEvent(&file, downloadPath, localChecksum, nil)

	slog.Info("Download completed", "fileID", fileID, "path", downloadPath)
	return nil
}

// Cancel cancels an in-progress download
func (d *Downloader) Cancel(fileID string) error {
	if val, ok := d.active.Load(fileID); ok {
		val.(*activeDownload).cancel()
		return nil
	}
	return ErrFileNotFound
}

// CancelByProduct cancels all in-progress downloads for a product.
// Returns the number of downloads cancelled.
func (d *Downloader) CancelByProduct(productID string) int {
	var cancelled int
	d.active.Range(func(key, value any) bool {
		ad := value.(*activeDownload)
		if ad.productID == productID {
			ad.cancel()
			cancelled++
		}
		return true
	})
	return cancelled
}

// ActiveDownloads returns progress for all active downloads
func (d *Downloader) ActiveDownloads() []DownloadProgress {
	return d.progress.GetAll()
}

// IsActive returns true if a download is currently in progress for the given file.
func (d *Downloader) IsActive(fileID string) bool {
	_, ok := d.active.Load(fileID)
	return ok
}

// GetProgress returns progress for a specific download
func (d *Downloader) GetProgress(fileID string) *DownloadProgress {
	return d.progress.Get(fileID)
}

// StatusVersion returns a counter that increments on every download status change.
func (d *Downloader) StatusVersion() uint64 {
	return d.progress.StatusVersion()
}

func (d *Downloader) getDownloadPath(file *database.File) string {
	// Structure: {data_dir}/downloads/{source}/{product}/{filename}
	return filepath.Join(
		d.cfg.DownloadsPath(),
		file.SourceID,
		file.ProductID,
		file.FileName,
	)
}

func (d *Downloader) handleError(entry *database.DownloadEntry, file *database.File, code, message string, err error) error {
	entry.Status = database.DownloadStatusFailed
	entry.ErrorMessage = fmt.Sprintf("%s: %v", message, err)
	d.db.Save(entry)
	d.progress.IncrementStatusVersion()

	event := hooks.NewEvent(hooks.EventDownloadFailed, file.SourceID).
		WithFile(file.ID, file.FileName, file.FileSize, "", "").
		WithError(code, entry.ErrorMessage)
	d.hooks.Emit(context.Background(), event)

	return fmt.Errorf("%s: %w", message, err)
}

func (d *Downloader) handleCancelled(entry *database.DownloadEntry, file *database.File) error {
	entry.Status = database.DownloadStatusCancelled
	d.db.Save(entry)
	d.progress.IncrementStatusVersion()

	event := hooks.NewEvent(hooks.EventDownloadCancelled, file.SourceID).
		WithFile(file.ID, file.FileName, file.FileSize, "", "")
	d.hooks.Emit(context.Background(), event)

	return context.Canceled
}

func (d *Downloader) emitEvent(eventType string, file *database.File, alerts []hooks.Alert) {
	event := hooks.NewEvent(eventType, file.SourceID).
		WithFile(file.ID, file.FileName, file.FileSize, "", "")

	for _, alert := range alerts {
		event.WithAlert(alert.Type, alert.Message, alert.Severity)
	}

	d.hooks.Emit(context.Background(), event)
}

func (d *Downloader) emitCompletedEvent(file *database.File, path, checksum string, alerts []hooks.Alert) {
	event := hooks.NewEvent(hooks.EventDownloadCompleted, file.SourceID).
		WithFile(file.ID, file.FileName, file.FileSize, checksum, path)

	for _, alert := range alerts {
		event.WithAlert(alert.Type, alert.Message, alert.Severity)
	}

	d.hooks.Emit(context.Background(), event)
}
