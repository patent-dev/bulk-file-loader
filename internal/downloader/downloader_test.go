package downloader

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/patent-dev/bulk-file-loader/config"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type mockAdapter struct {
	downloadFunc func(ctx context.Context, file sources.FileInfo, w io.Writer, progress sources.ProgressFunc) error
}

func (m *mockAdapter) ID() string                                  { return "mock" }
func (m *mockAdapter) Name() string                                { return "Mock Source" }
func (m *mockAdapter) CredentialFields() []sources.CredentialField { return nil }
func (m *mockAdapter) SetCredentials(creds map[string]string)      {}
func (m *mockAdapter) ValidateCredentials(context.Context) error   { return nil }
func (m *mockAdapter) FetchProducts(context.Context) ([]sources.ProductInfo, error) {
	return nil, nil
}
func (m *mockAdapter) FetchDeliveries(context.Context, string) ([]sources.DeliveryInfo, error) {
	return nil, nil
}
func (m *mockAdapter) FetchFiles(context.Context, string, string) ([]sources.FileInfo, error) {
	return nil, nil
}
func (m *mockAdapter) DownloadFile(ctx context.Context, file sources.FileInfo, w io.Writer, progress sources.ProgressFunc) error {
	if m.downloadFunc != nil {
		return m.downloadFunc(ctx, file, w, progress)
	}
	// Default: write some bytes
	w.Write([]byte("test content"))
	progress(12, 12)
	return nil
}

func setupTestEnv(t *testing.T) (*database.DB, *sources.Registry, *hooks.Manager, *config.Config) {
	t.Helper()

	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	gormDB.AutoMigrate(
		&database.Source{},
		&database.Product{},
		&database.Delivery{},
		&database.File{},
		&database.DownloadEntry{},
		&database.Webhook{},
	)

	db := &database.DB{DB: gormDB}
	cfg := &config.Config{
		DataDir:         t.TempDir(),
		MaxConcurrent:   2,
		DownloadTimeout: 60,
	}
	registry := sources.NewRegistry(db, cfg)
	hooksManager := hooks.New(db)

	return db, registry, hooksManager, cfg
}

func TestNew(t *testing.T) {
	db, registry, hooksManager, cfg := setupTestEnv(t)

	downloader := New(db, registry, hooksManager, cfg)
	if downloader == nil {
		t.Fatal("New() returned nil")
	}
	if cap(downloader.semaphore) != cfg.MaxConcurrent {
		t.Errorf("semaphore capacity = %d, want %d", cap(downloader.semaphore), cfg.MaxConcurrent)
	}
}

func TestDownloadFileNotFound(t *testing.T) {
	db, registry, hooksManager, cfg := setupTestEnv(t)
	downloader := New(db, registry, hooksManager, cfg)

	err := downloader.Download(context.Background(), "nonexistent-file-id")
	if err != ErrFileNotFound {
		t.Errorf("Download() error = %v, want ErrFileNotFound", err)
	}
}

func TestDownloadSourceNotFound(t *testing.T) {
	db, registry, hooksManager, cfg := setupTestEnv(t)
	downloader := New(db, registry, hooksManager, cfg)

	// Create file without registering source adapter
	db.Create(&database.Source{ID: "unregistered", Name: "Unregistered"})
	db.Create(&database.Product{ID: "prod", SourceID: "unregistered", Name: "Product"})
	db.Create(&database.Delivery{ID: "del", ProductID: "prod", Name: "Delivery"})
	db.Create(&database.File{
		ID:         "file-1",
		DeliveryID: "del",
		ProductID:  "prod",
		SourceID:   "unregistered",
		FileName:   "test.txt",
		FileSize:   100,
	})

	err := downloader.Download(context.Background(), "file-1")
	if err != ErrSourceNotFound {
		t.Errorf("Download() error = %v, want ErrSourceNotFound", err)
	}
}

func TestDownloadInProgress(t *testing.T) {
	db, registry, hooksManager, cfg := setupTestEnv(t)
	downloader := New(db, registry, hooksManager, cfg)

	// Create source and file
	adapter := &mockAdapter{
		downloadFunc: func(ctx context.Context, file sources.FileInfo, w io.Writer, progress sources.ProgressFunc) error {
			// Simulate slow download
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		},
	}
	registry.Register(adapter)

	db.Create(&database.Source{ID: "mock", Name: "Mock", Enabled: true})
	db.Create(&database.Product{ID: "prod", SourceID: "mock", Name: "Product"})
	db.Create(&database.Delivery{ID: "del", ProductID: "prod", Name: "Delivery"})
	db.Create(&database.File{
		ID:         "file-1",
		DeliveryID: "del",
		ProductID:  "prod",
		SourceID:   "mock",
		FileName:   "test.txt",
		FileSize:   100,
	})

	// Start first download in background
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		downloader.Download(ctx, "file-1")
	}()

	// Give first download time to start
	time.Sleep(20 * time.Millisecond)

	// Try second download
	err := downloader.Download(context.Background(), "file-1")
	if err != ErrDownloadInProgress {
		t.Errorf("Second Download() error = %v, want ErrDownloadInProgress", err)
	}

	wg.Wait()
}

func TestCancel(t *testing.T) {
	db, registry, hooksManager, cfg := setupTestEnv(t)
	downloader := New(db, registry, hooksManager, cfg)

	adapter := &mockAdapter{
		downloadFunc: func(ctx context.Context, file sources.FileInfo, w io.Writer, progress sources.ProgressFunc) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	registry.Register(adapter)

	db.Create(&database.Source{ID: "mock", Name: "Mock", Enabled: true})
	db.Create(&database.Product{ID: "prod", SourceID: "mock", Name: "Product"})
	db.Create(&database.Delivery{ID: "del", ProductID: "prod", Name: "Delivery"})
	db.Create(&database.File{
		ID:         "file-1",
		DeliveryID: "del",
		ProductID:  "prod",
		SourceID:   "mock",
		FileName:   "test.txt",
		FileSize:   100,
	})

	var downloadErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		downloadErr = downloader.Download(context.Background(), "file-1")
	}()

	time.Sleep(20 * time.Millisecond)

	err := downloader.Cancel("file-1")
	if err != nil {
		t.Errorf("Cancel() error = %v", err)
	}

	wg.Wait()

	if downloadErr != context.Canceled {
		t.Errorf("Download error after cancel = %v, want context.Canceled", downloadErr)
	}
}

func TestCancelNonexistent(t *testing.T) {
	db, registry, hooksManager, cfg := setupTestEnv(t)
	downloader := New(db, registry, hooksManager, cfg)

	err := downloader.Cancel("nonexistent")
	if err != ErrFileNotFound {
		t.Errorf("Cancel() error = %v, want ErrFileNotFound", err)
	}
}

func TestActiveDownloads(t *testing.T) {
	db, registry, hooksManager, cfg := setupTestEnv(t)
	downloader := New(db, registry, hooksManager, cfg)

	// Initially empty
	active := downloader.ActiveDownloads()
	if len(active) != 0 {
		t.Errorf("ActiveDownloads() = %d, want 0", len(active))
	}
}

func TestGetProgress(t *testing.T) {
	db, registry, hooksManager, cfg := setupTestEnv(t)
	downloader := New(db, registry, hooksManager, cfg)

	progress := downloader.GetProgress("nonexistent")
	if progress != nil {
		t.Error("GetProgress for nonexistent file should return nil")
	}
}
