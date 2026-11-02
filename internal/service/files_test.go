package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/database"
)

func seedFile(t *testing.T, svc *Service, id string) {
	t.Helper()
	svc.DB.Create(&database.Source{ID: "src1", Name: "Test Source"})
	svc.DB.Create(&database.Product{ID: "prod1", SourceID: "src1", Name: "Test Product"})
	svc.DB.Create(&database.Delivery{ID: "del1", ProductID: "prod1", Name: "Delivery 1"})
	svc.DB.Create(&database.File{
		ID:         id,
		DeliveryID: "del1",
		ProductID:  "prod1",
		SourceID:   "src1",
		FileName:   id + ".zip",
		FileSize:   1024,
	})
}

func TestDeriveFileStatus(t *testing.T) {
	svc := setupTestService(t)
	seedFile(t, svc, "f1")

	// No download entry -> available
	fs := deriveFileStatus(database.File{ID: "f1"}, svc.DB)
	status, errMsg := fs.status, fs.errorMsg
	if status != "available" {
		t.Errorf("expected available, got %s", status)
	}
	if errMsg != "" {
		t.Errorf("expected empty error, got %q", errMsg)
	}

	// Downloading
	svc.DB.Create(&database.DownloadEntry{FileID: "f1", Status: database.DownloadStatusDownloading})
	status = deriveFileStatus(database.File{ID: "f1"}, svc.DB).status
	if status != "downloading" {
		t.Errorf("expected downloading, got %s", status)
	}

	// Completed + file exists -> downloaded
	tmpFile := filepath.Join(t.TempDir(), "f1.zip")
	if err := os.WriteFile(tmpFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	svc.DB.Create(&database.DownloadEntry{FileID: "f1", Status: database.DownloadStatusCompleted, LocalPath: tmpFile})
	fs = deriveFileStatus(database.File{ID: "f1"}, svc.DB)
	if fs.status != "downloaded" {
		t.Errorf("expected downloaded, got %s", fs.status)
	}
	if fs.downloadedBytes != 4 {
		t.Errorf("downloadedBytes = %d, want the 4 bytes on disk", fs.downloadedBytes)
	}

	// Completed + file missing -> deleted
	svc.DB.Where("1 = 1").Delete(&database.DownloadEntry{})
	svc.DB.Create(&database.DownloadEntry{FileID: "f1", Status: database.DownloadStatusCompleted, LocalPath: "/nonexistent/path"})
	status = deriveFileStatus(database.File{ID: "f1"}, svc.DB).status
	if status != "deleted" {
		t.Errorf("expected deleted, got %s", status)
	}

	// Failed
	svc.DB.Where("1 = 1").Delete(&database.DownloadEntry{})
	svc.DB.Create(&database.DownloadEntry{FileID: "f1", Status: database.DownloadStatusFailed, ErrorMessage: "timeout"})
	fs = deriveFileStatus(database.File{ID: "f1"}, svc.DB)
	status, errMsg = fs.status, fs.errorMsg
	if status != "failed" {
		t.Errorf("expected failed, got %s", status)
	}
	if errMsg != "timeout" {
		t.Errorf("expected error message 'timeout', got %q", errMsg)
	}

	// Cancelled
	svc.DB.Where("1 = 1").Delete(&database.DownloadEntry{})
	svc.DB.Create(&database.DownloadEntry{FileID: "f1", Status: database.DownloadStatusCancelled})
	status = deriveFileStatus(database.File{ID: "f1"}, svc.DB).status
	if status != "cancelled" {
		t.Errorf("expected cancelled, got %s", status)
	}

	// Skipped (no download entry, skipped flag set)
	svc.DB.Where("1 = 1").Delete(&database.DownloadEntry{})
	status = deriveFileStatus(database.File{ID: "f1", Skipped: true}, svc.DB).status
	if status != "skipped" {
		t.Errorf("expected skipped, got %s", status)
	}
}

func TestListFilesWithStatusFilter(t *testing.T) {
	svc := setupTestService(t)

	svc.DB.Create(&database.Source{ID: "src1", Name: "Source"})
	svc.DB.Create(&database.Product{ID: "prod1", SourceID: "src1", Name: "Product"})
	svc.DB.Create(&database.Delivery{ID: "del1", ProductID: "prod1", Name: "Delivery"})

	// Create 5 files: 2 will be "available", 3 will have completed downloads
	for i := range 5 {
		fid := "file" + string(rune('A'+i))
		svc.DB.Create(&database.File{
			ID:         fid,
			DeliveryID: "del1",
			ProductID:  "prod1",
			SourceID:   "src1",
			FileName:   fid + ".zip",
			FileSize:   100,
		})
	}

	// Mark 3 files as completed (will show as "deleted" since no real file on disk)
	for _, fid := range []string{"fileC", "fileD", "fileE"} {
		svc.DB.Create(&database.DownloadEntry{
			FileID:    fid,
			Status:    database.DownloadStatusCompleted,
			LocalPath: "/nonexistent",
		})
	}

	// Filter by "available" status: should only get 2 files
	statusFilter := generated.ListFilesParamsStatus("available")
	result, err := svc.ListFiles(nil, nil, &statusFilter, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 {
		t.Errorf("expected total=2 for available filter, got %d", result.Total)
	}
	if len(result.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(result.Files))
	}

	// No filter: should get all 5
	result, err = svc.ListFiles(nil, nil, nil, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 5 {
		t.Errorf("expected total=5 without filter, got %d", result.Total)
	}

	// Test pagination with status filter
	result, err = svc.ListFiles(nil, nil, &statusFilter, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 {
		t.Errorf("expected total=2 with pagination, got %d", result.Total)
	}
	if len(result.Files) != 1 {
		t.Errorf("expected 1 file in page, got %d", len(result.Files))
	}
}

func TestSkipFileNotFound(t *testing.T) {
	svc := setupTestService(t)
	err := svc.SkipFile("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUnskipFileNotFound(t *testing.T) {
	svc := setupTestService(t)
	err := svc.UnskipFile("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestResetFile(t *testing.T) {
	svc := setupTestService(t)
	seedFile(t, svc, "f1")

	// Create a download entry so reset has something to delete
	svc.DB.Create(&database.DownloadEntry{FileID: "f1", Status: database.DownloadStatusFailed, ErrorMessage: "err"})

	// Mark file as skipped so we can verify reset clears it
	svc.DB.Model(&database.File{}).Where("id = ?", "f1").Update("skipped", true)

	err := svc.ResetFile("f1")
	if err != nil {
		t.Fatalf("ResetFile returned error: %v", err)
	}

	// Verify download entries removed
	var count int64
	svc.DB.Model(&database.DownloadEntry{}).Where("file_id = ?", "f1").Count(&count)
	if count != 0 {
		t.Errorf("expected 0 download entries after reset, got %d", count)
	}

	// Verify skipped flag cleared
	var file database.File
	svc.DB.First(&file, "id = ?", "f1")
	if file.Skipped {
		t.Error("expected skipped to be false after reset")
	}
}

func TestResetFileNotFound(t *testing.T) {
	svc := setupTestService(t)
	seedFile(t, svc, "f1")

	// No download history exists for this file
	err := svc.ResetFile("f1")
	if err == nil {
		t.Fatal("expected error when no download history exists")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestListFilesSizeFallsBackToDownload verifies a file whose source lists no
// size reports its downloaded size, while a listed size is kept.
func TestListFilesSizeFallsBackToDownload(t *testing.T) {
	svc := setupTestService(t)
	seedFile(t, svc, "listed")
	svc.DB.Create(&database.File{ID: "unlisted", DeliveryID: "del1", ProductID: "prod1", SourceID: "src1", FileName: "unlisted.zip"})

	dir := t.TempDir()
	for _, id := range []string{"listed", "unlisted"} {
		path := filepath.Join(dir, id+".zip")
		if err := os.WriteFile(path, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
		svc.DB.Create(&database.DownloadEntry{FileID: id, Status: database.DownloadStatusCompleted, LocalPath: path})
	}

	result, err := svc.ListFiles(nil, nil, nil, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"listed": 1024, "unlisted": 4}
	for _, f := range result.Files {
		if f.FileSize == nil || *f.FileSize != want[f.Id] {
			t.Errorf("%s: fileSize = %v, want %d", f.Id, f.FileSize, want[f.Id])
		}
	}
	if len(result.Files) != 2 {
		t.Errorf("got %d files, want 2", len(result.Files))
	}
}
