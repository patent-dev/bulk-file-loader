package downloader

import (
	"sync"
	"time"
)

// ProgressTracker tracks download progress for multiple files
type ProgressTracker struct {
	downloads map[string]*DownloadProgress
	mu        sync.RWMutex
}

// DownloadProgress represents the progress of a single download
type DownloadProgress struct {
	FileID       string    `json:"fileId"`
	FileName     string    `json:"fileName"`
	BytesWritten int64     `json:"bytesWritten"`
	TotalBytes   int64     `json:"totalBytes"`
	StartedAt    time.Time `json:"startedAt"`
	Speed        float64   `json:"speed"` // bytes per second
}

// NewProgressTracker creates a new progress tracker
func NewProgressTracker() *ProgressTracker {
	return &ProgressTracker{
		downloads: make(map[string]*DownloadProgress),
	}
}

// Start registers a new download
func (pt *ProgressTracker) Start(fileID, fileName string, totalBytes int64) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.downloads[fileID] = &DownloadProgress{
		FileID:     fileID,
		FileName:   fileName,
		TotalBytes: totalBytes,
		StartedAt:  time.Now(),
	}
}

// Update updates progress for a download
func (pt *ProgressTracker) Update(fileID string, bytesWritten, totalBytes int64) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	p, ok := pt.downloads[fileID]
	if !ok {
		return
	}

	p.BytesWritten = bytesWritten
	if totalBytes > 0 {
		p.TotalBytes = totalBytes
	}

	// Calculate speed
	elapsed := time.Since(p.StartedAt).Seconds()
	if elapsed > 0 {
		p.Speed = float64(bytesWritten) / elapsed
	}
}

// Complete removes a download from tracking
func (pt *ProgressTracker) Complete(fileID string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	delete(pt.downloads, fileID)
}

// Get returns progress for a specific download
func (pt *ProgressTracker) Get(fileID string) *DownloadProgress {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	if p, ok := pt.downloads[fileID]; ok {
		// Return a copy
		copy := *p
		return &copy
	}
	return nil
}

// GetAll returns progress for all active downloads
func (pt *ProgressTracker) GetAll() []DownloadProgress {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	result := make([]DownloadProgress, 0, len(pt.downloads))
	for _, p := range pt.downloads {
		result = append(result, *p)
	}
	return result
}

// Percent returns the download progress as a percentage
func (p *DownloadProgress) Percent() float64 {
	if p.TotalBytes == 0 {
		return 0
	}
	return float64(p.BytesWritten) * 100 / float64(p.TotalBytes)
}

// ETA returns the estimated time remaining
func (p *DownloadProgress) ETA() time.Duration {
	if p.Speed == 0 {
		return 0
	}
	remaining := p.TotalBytes - p.BytesWritten
	return time.Duration(float64(remaining)/p.Speed) * time.Second
}
