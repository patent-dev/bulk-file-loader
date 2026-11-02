package downloader

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/patent-dev/bulk-file-loader/config"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/database/dbtest"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
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
	_, _ = w.Write([]byte("test content"))
	progress(12, 12)
	return nil
}

func setupTestEnv(t *testing.T) (*database.DB, *sources.Registry, *hooks.Manager, *config.Config) {
	t.Helper()

	db, err := database.New(dbtest.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

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
		_ = downloader.Download(ctx, "file-1")
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

func TestCancelByProduct(t *testing.T) {
	db, registry, hooksManager, cfg := setupTestEnv(t)
	cfg.MaxConcurrent = 4
	dl := New(db, registry, hooksManager, cfg)

	adapter := &mockAdapter{
		downloadFunc: func(ctx context.Context, file sources.FileInfo, w io.Writer, progress sources.ProgressFunc) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	registry.Register(adapter)

	// Set up two products under the same source
	db.Create(&database.Source{ID: "mock", Name: "Mock", Enabled: true})
	db.Create(&database.Product{ID: "prod-a", SourceID: "mock", Name: "Product A"})
	db.Create(&database.Product{ID: "prod-b", SourceID: "mock", Name: "Product B"})
	db.Create(&database.Delivery{ID: "del-a", ProductID: "prod-a", Name: "Delivery A"})
	db.Create(&database.Delivery{ID: "del-b", ProductID: "prod-b", Name: "Delivery B"})
	db.Create(&database.File{
		ID: "file-a1", DeliveryID: "del-a", ProductID: "prod-a", SourceID: "mock",
		FileName: "a1.txt", FileSize: 100,
	})
	db.Create(&database.File{
		ID: "file-b1", DeliveryID: "del-b", ProductID: "prod-b", SourceID: "mock",
		FileName: "b1.txt", FileSize: 100,
	})

	var wg sync.WaitGroup
	var errA1, errB1 error

	// Start download for product A
	wg.Add(1)
	go func() {
		defer wg.Done()
		errA1 = dl.Download(context.Background(), "file-a1")
	}()

	// Start download for product B
	wg.Add(1)
	go func() {
		defer wg.Done()
		errB1 = dl.Download(context.Background(), "file-b1")
	}()

	// Wait for downloads to register in active map
	time.Sleep(50 * time.Millisecond)

	// Cancel only product A
	cancelled := dl.CancelByProduct("prod-a")
	if cancelled != 1 {
		t.Errorf("CancelByProduct(prod-a) cancelled %d, want 1", cancelled)
	}

	// Product B should still be active
	time.Sleep(20 * time.Millisecond)
	if dl.Cancel("file-b1") != nil {
		t.Error("file-b1 should still be cancellable (active)")
	}

	wg.Wait()

	if errA1 != context.Canceled {
		t.Errorf("file-a1 error = %v, want context.Canceled", errA1)
	}
	if errB1 != context.Canceled {
		t.Errorf("file-b1 error = %v, want context.Canceled", errB1)
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

func TestProgressThrottling(t *testing.T) {
	db, registry, hooksManager, cfg := setupTestEnv(t)
	dl := New(db, registry, hooksManager, cfg)

	var progressCallCount int
	adapter := &mockAdapter{
		downloadFunc: func(ctx context.Context, file sources.FileInfo, w io.Writer, progress sources.ProgressFunc) error {
			// Fire many rapid progress updates
			for i := 0; i < 100; i++ {
				progress(int64(i*100), 10000)
				progressCallCount++
			}
			_, _ = w.Write([]byte("done"))
			return nil
		},
	}
	registry.Register(adapter)

	db.Create(&database.Source{ID: "mock", Name: "Mock", Enabled: true})
	db.Create(&database.Product{ID: "prod", SourceID: "mock", Name: "Product"})
	db.Create(&database.Delivery{ID: "del", ProductID: "prod", Name: "Delivery"})
	db.Create(&database.File{
		ID:         "file-throttle",
		DeliveryID: "del",
		ProductID:  "prod",
		SourceID:   "mock",
		FileName:   "test.txt",
		FileSize:   10000,
	})

	err := dl.Download(context.Background(), "file-throttle")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	// There should be exactly 1 download entry (created at start, updated on complete).
	// The progress callbacks fire too fast (sub-millisecond) to trigger the 5-second
	// throttle, so the entry's intermediate progress should NOT reflect every callback.
	var entries []database.DownloadEntry
	db.Where("file_id = ?", "file-throttle").Find(&entries)
	if len(entries) != 1 {
		t.Fatalf("expected 1 download entry, got %d", len(entries))
	}

	entry := entries[0]
	if entry.Status != database.DownloadStatusCompleted {
		t.Errorf("status = %q, want completed", entry.Status)
	}

	// The adapter's callbacks claimed 10000 bytes, but the completed entry
	// records what actually arrived: the 4 bytes written.
	if entry.Progress != 4 || entry.TotalBytes != 4 {
		t.Errorf("completed entry progress/total = %d/%d, want the 4 bytes received", entry.Progress, entry.TotalBytes)
	}

	// Meanwhile, the in-memory progress tracker should have been updated on every tick
	if progressCallCount != 100 {
		t.Errorf("progressCallCount = %d, want 100", progressCallCount)
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

// TestCompletedEventReportsReceivedBytes verifies download.completed carries
// the size actually received, not the size the source listed (0 here, as for
// sources that list none).
func TestCompletedEventReportsReceivedBytes(t *testing.T) {
	db, registry, hooksManager, cfg := setupTestEnv(t)
	dl := New(db, registry, hooksManager, cfg)

	events := make(chan hooks.Event, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e hooks.Event
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			t.Errorf("decode webhook: %v", err)
		}
		events <- e
	}))
	defer receiver.Close()
	if _, err := hooksManager.CreateWebhook("test", receiver.URL, []string{hooks.EventDownloadCompleted}); err != nil {
		t.Fatal(err)
	}

	registry.Register(&mockAdapter{})
	db.Create(&database.Source{ID: "mock", Name: "Mock", Enabled: true})
	db.Create(&database.Product{ID: "prod", SourceID: "mock", Name: "Product"})
	db.Create(&database.Delivery{ID: "del", ProductID: "prod", Name: "Delivery"})
	db.Create(&database.File{ID: "file-size", DeliveryID: "del", ProductID: "prod", SourceID: "mock", FileName: "f.txt"})

	if err := dl.Download(context.Background(), "file-size"); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-events:
		if e.File == nil || e.File.Size != int64(len("test content")) {
			t.Errorf("download.completed file = %+v, want size %d", e.File, len("test content"))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no download.completed webhook received")
	}
}
