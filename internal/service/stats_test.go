package service

import (
	"testing"

	"github.com/patent-dev/bulk-file-loader/internal/database"
)

func TestGetStats(t *testing.T) {
	svc := setupTestService(t)

	svc.DB.Create(&database.Source{ID: "src1", Name: "Enabled Source", Enabled: true})
	svc.DB.Create(&database.Source{ID: "src2", Name: "Disabled Source", Enabled: false})
	svc.DB.Create(&database.Product{ID: "prod1", SourceID: "src1", Name: "Product", AutoDownload: true})
	svc.DB.Create(&database.Delivery{ID: "del1", ProductID: "prod1", Name: "Delivery"})

	// Create 4 files
	for _, fid := range []string{"f1", "f2", "f3", "f4"} {
		svc.DB.Create(&database.File{
			ID:         fid,
			DeliveryID: "del1",
			ProductID:  "prod1",
			SourceID:   "src1",
			FileName:   fid + ".zip",
		})
	}

	// f1: completed download
	svc.DB.Create(&database.DownloadEntry{FileID: "f1", Status: database.DownloadStatusCompleted})
	// f2: failed download
	svc.DB.Create(&database.DownloadEntry{FileID: "f2", Status: database.DownloadStatusFailed})
	// f3, f4: no entries (pending, since auto_download=true and not skipped)

	stats, err := svc.GetStats()
	if err != nil {
		t.Fatal(err)
	}

	if stats.TotalFiles == nil || *stats.TotalFiles != 4 {
		t.Errorf("expected TotalFiles=4, got %v", stats.TotalFiles)
	}
	if stats.DownloadedFiles == nil || *stats.DownloadedFiles != 1 {
		t.Errorf("expected DownloadedFiles=1, got %v", stats.DownloadedFiles)
	}
	if stats.EnabledSources == nil || *stats.EnabledSources != 1 {
		t.Errorf("expected EnabledSources=1, got %v", stats.EnabledSources)
	}
	// f2 has a download entry so it is not "pending"; f3 and f4 have none
	if stats.PendingFiles == nil || *stats.PendingFiles != 2 {
		t.Errorf("expected PendingFiles=2, got %v", stats.PendingFiles)
	}
	if stats.ActiveDownloads == nil || *stats.ActiveDownloads != 0 {
		t.Errorf("expected ActiveDownloads=0, got %v", stats.ActiveDownloads)
	}
}
