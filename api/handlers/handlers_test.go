package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/config"
	"github.com/patent-dev/bulk-file-loader/internal/auth"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/downloader"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"github.com/patent-dev/bulk-file-loader/internal/scheduler"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type mockAdapter struct {
	id   string
	name string
}

func (m *mockAdapter) ID() string                                  { return m.id }
func (m *mockAdapter) Name() string                                { return m.name }
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
	w.Write([]byte("content"))
	return nil
}

func setupTestHandler(t *testing.T) (*Handler, *database.DB) {
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
		&database.Setting{},
	)

	db := &database.DB{DB: gormDB}
	cfg := &config.Config{
		DataDir:         t.TempDir(),
		MaxConcurrent:   2,
		DownloadTimeout: 60,
		DevMode:         true,
	}

	authService := auth.New(db, cfg)
	registry := sources.NewRegistry(db, cfg)
	hooksManager := hooks.New(db)
	dl := downloader.New(db, registry, hooksManager, cfg)
	sched := scheduler.New(db, registry, dl, hooksManager)

	// Register mock adapter
	registry.Register(&mockAdapter{id: "mock", name: "Mock Source"})

	handler := New(db, authService, registry, dl, sched, hooksManager)
	return handler, db
}

func TestHealthCheck(t *testing.T) {
	handler, _ := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()

	handler.HealthCheck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("HealthCheck status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp generated.HealthResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Status != "healthy" {
		t.Errorf("Status = %q, want healthy", resp.Status)
	}
	if resp.Version == nil || *resp.Version == "" {
		t.Error("Version should be set")
	}
}

func TestGetAuthStatusNotConfigured(t *testing.T) {
	handler, _ := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
	w := httptest.NewRecorder()

	handler.GetAuthStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GetAuthStatus status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp generated.AuthStatus
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Configured {
		t.Error("Configured = true, want false")
	}
	if resp.Authenticated {
		t.Error("Authenticated = true, want false")
	}
}

func TestSetupAuth(t *testing.T) {
	handler, _ := setupTestHandler(t)

	body := bytes.NewBufferString(`{"passphrase":"testpassphrase123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.SetupAuth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("SetupAuth status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify cookie is set
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "bulk_loader_session" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Session cookie should be set after setup")
	}
}

func TestSetupAuthShortPassphrase(t *testing.T) {
	handler, _ := setupTestHandler(t)

	body := bytes.NewBufferString(`{"passphrase":"short"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.SetupAuth(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("SetupAuth with short passphrase status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestSetupAuthAlreadyConfigured(t *testing.T) {
	handler, db := setupTestHandler(t)

	// Configure first
	db.SetSetting("passphrase_hash", "somehash")
	db.SetSetting("passphrase_salt", "somesalt")

	body := bytes.NewBufferString(`{"passphrase":"testpassphrase123"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.SetupAuth(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("SetupAuth when already configured status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestListSources(t *testing.T) {
	handler, db := setupTestHandler(t)

	// Create source
	db.Create(&database.Source{ID: "mock", Name: "Mock Source", Enabled: true})

	req := httptest.NewRequest(http.MethodGet, "/api/sources", nil)
	w := httptest.NewRecorder()

	handler.ListSources(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ListSources status = %d, want %d", w.Code, http.StatusOK)
	}

	var sources []generated.Source
	json.NewDecoder(w.Body).Decode(&sources)

	if len(sources) != 1 {
		t.Errorf("ListSources returned %d sources, want 1", len(sources))
	}
}

func TestGetSource(t *testing.T) {
	handler, db := setupTestHandler(t)
	db.Create(&database.Source{ID: "mock", Name: "Mock Source", Enabled: true})

	req := httptest.NewRequest(http.MethodGet, "/api/sources/mock", nil)
	w := httptest.NewRecorder()

	handler.GetSource(w, req, "mock")

	if w.Code != http.StatusOK {
		t.Errorf("GetSource status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestGetSourceNotFound(t *testing.T) {
	handler, _ := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/sources/nonexistent", nil)
	w := httptest.NewRecorder()

	handler.GetSource(w, req, "nonexistent")

	if w.Code != http.StatusNotFound {
		t.Errorf("GetSource nonexistent status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestListProducts(t *testing.T) {
	handler, db := setupTestHandler(t)

	db.Create(&database.Source{ID: "mock", Name: "Mock"})
	db.Create(&database.Product{ID: "p1", SourceID: "mock", Name: "Product 1"})
	db.Create(&database.Product{ID: "p2", SourceID: "mock", Name: "Product 2"})

	req := httptest.NewRequest(http.MethodGet, "/api/products", nil)
	w := httptest.NewRecorder()

	handler.ListProducts(w, req, generated.ListProductsParams{})

	if w.Code != http.StatusOK {
		t.Errorf("ListProducts status = %d, want %d", w.Code, http.StatusOK)
	}

	var products []generated.Product
	json.NewDecoder(w.Body).Decode(&products)

	if len(products) != 2 {
		t.Errorf("ListProducts returned %d products, want 2", len(products))
	}
}

func TestListProductsFilterBySource(t *testing.T) {
	handler, db := setupTestHandler(t)

	db.Create(&database.Source{ID: "s1", Name: "Source 1"})
	db.Create(&database.Source{ID: "s2", Name: "Source 2"})
	db.Create(&database.Product{ID: "p1", SourceID: "s1", Name: "Product 1"})
	db.Create(&database.Product{ID: "p2", SourceID: "s2", Name: "Product 2"})

	sourceID := "s1"
	req := httptest.NewRequest(http.MethodGet, "/api/products?sourceId=s1", nil)
	w := httptest.NewRecorder()

	handler.ListProducts(w, req, generated.ListProductsParams{SourceId: &sourceID})

	var products []generated.Product
	json.NewDecoder(w.Body).Decode(&products)

	if len(products) != 1 {
		t.Errorf("ListProducts with filter returned %d products, want 1", len(products))
	}
	if products[0].Id != "p1" {
		t.Errorf("Product ID = %q, want p1", products[0].Id)
	}
}

func TestListFiles(t *testing.T) {
	handler, db := setupTestHandler(t)

	db.Create(&database.Source{ID: "s1", Name: "Source"})
	db.Create(&database.Product{ID: "p1", SourceID: "s1", Name: "Product"})
	db.Create(&database.Delivery{ID: "d1", ProductID: "p1", Name: "Delivery"})
	db.Create(&database.File{ID: "f1", DeliveryID: "d1", ProductID: "p1", SourceID: "s1", FileName: "test.txt", FileSize: 100})

	req := httptest.NewRequest(http.MethodGet, "/api/files", nil)
	w := httptest.NewRecorder()

	handler.ListFiles(w, req, generated.ListFilesParams{})

	if w.Code != http.StatusOK {
		t.Errorf("ListFiles status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp generated.FileListResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Files) != 1 {
		t.Errorf("ListFiles returned %d files, want 1", len(resp.Files))
	}
	if resp.Total != 1 {
		t.Errorf("Total = %d, want 1", resp.Total)
	}
}

func TestGetStats(t *testing.T) {
	handler, db := setupTestHandler(t)

	db.Create(&database.Source{ID: "s1", Name: "Source", Enabled: true})
	db.Create(&database.Product{ID: "p1", SourceID: "s1", Name: "Product"})
	db.Create(&database.Delivery{ID: "d1", ProductID: "p1", Name: "Delivery"})
	db.Create(&database.File{ID: "f1", DeliveryID: "d1", ProductID: "p1", SourceID: "s1", FileName: "test.txt"})
	db.Create(&database.File{ID: "f2", DeliveryID: "d1", ProductID: "p1", SourceID: "s1", FileName: "test2.txt"})

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	w := httptest.NewRecorder()

	handler.GetStats(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GetStats status = %d, want %d", w.Code, http.StatusOK)
	}

	var stats generated.StatsResponse
	json.NewDecoder(w.Body).Decode(&stats)

	if stats.TotalFiles == nil || *stats.TotalFiles != 2 {
		t.Errorf("TotalFiles = %v, want 2", stats.TotalFiles)
	}
	if stats.EnabledSources == nil || *stats.EnabledSources != 1 {
		t.Errorf("EnabledSources = %v, want 1", stats.EnabledSources)
	}
}

func TestListWebhooks(t *testing.T) {
	handler, db := setupTestHandler(t)

	db.Create(&database.Webhook{Name: "Hook 1", URL: "https://example.com/1", Events: `["*"]`, Enabled: true})
	db.Create(&database.Webhook{Name: "Hook 2", URL: "https://example.com/2", Events: `["download.completed"]`, Enabled: false})

	req := httptest.NewRequest(http.MethodGet, "/api/hooks", nil)
	w := httptest.NewRecorder()

	handler.ListWebhooks(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ListWebhooks status = %d, want %d", w.Code, http.StatusOK)
	}

	var webhooks []generated.Webhook
	json.NewDecoder(w.Body).Decode(&webhooks)

	if len(webhooks) != 2 {
		t.Errorf("ListWebhooks returned %d webhooks, want 2", len(webhooks))
	}
}

func TestCreateWebhook(t *testing.T) {
	handler, _ := setupTestHandler(t)

	body := bytes.NewBufferString(`{"name":"New Hook","url":"https://example.com/hook","events":["download.completed"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/hooks", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.CreateWebhook(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("CreateWebhook status = %d, want %d", w.Code, http.StatusCreated)
	}

	var webhook generated.Webhook
	json.NewDecoder(w.Body).Decode(&webhook)

	if webhook.Name != "New Hook" {
		t.Errorf("Name = %q, want New Hook", webhook.Name)
	}
	if !webhook.Enabled {
		t.Error("Enabled = false, want true")
	}
}

func TestDeleteWebhook(t *testing.T) {
	handler, db := setupTestHandler(t)

	webhook := &database.Webhook{Name: "To Delete", URL: "https://example.com", Events: `["*"]`}
	db.Create(webhook)

	req := httptest.NewRequest(http.MethodDelete, "/api/hooks/1", nil)
	w := httptest.NewRecorder()

	handler.DeleteWebhook(w, req, int(webhook.ID))

	if w.Code != http.StatusNoContent {
		t.Errorf("DeleteWebhook status = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestLoginInvalidPassphrase(t *testing.T) {
	handler, _ := setupTestHandler(t)

	body := bytes.NewBufferString(`{"passphrase":"wrongpassphrase"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.Login(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Login with wrong passphrase status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestLogout(t *testing.T) {
	handler, _ := setupTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	w := httptest.NewRecorder()

	handler.Logout(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Logout status = %d, want %d", w.Code, http.StatusOK)
	}

	// Verify cookie is cleared
	cookies := w.Result().Cookies()
	for _, c := range cookies {
		if c.Name == "bulk_loader_session" && c.MaxAge == -1 {
			return // Cookie properly cleared
		}
	}
	t.Error("Session cookie should be cleared after logout")
}

func TestDownloadFile(t *testing.T) {
	handler, db := setupTestHandler(t)

	db.Create(&database.Source{ID: "mock", Name: "Mock", Enabled: true})
	db.Create(&database.Product{ID: "p1", SourceID: "mock", Name: "Product"})
	db.Create(&database.Delivery{ID: "d1", ProductID: "p1", Name: "Delivery"})
	db.Create(&database.File{ID: "f1", DeliveryID: "d1", ProductID: "p1", SourceID: "mock", FileName: "test.txt"})

	req := httptest.NewRequest(http.MethodPost, "/api/files/f1/download", nil)
	w := httptest.NewRecorder()

	handler.DownloadFile(w, req, "f1")

	if w.Code != http.StatusAccepted {
		t.Errorf("DownloadFile status = %d, want %d", w.Code, http.StatusAccepted)
	}

	// Wait for async download to complete to avoid temp dir cleanup race
	for i := 0; i < 50; i++ {
		var entry database.DownloadEntry
		if err := db.Where("file_id = ?", "f1").First(&entry).Error; err == nil {
			if entry.Status == database.DownloadStatusCompleted || entry.Status == database.DownloadStatusFailed {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSkipAndUnskipFile(t *testing.T) {
	handler, db := setupTestHandler(t)

	db.Create(&database.Source{ID: "s1", Name: "Source"})
	db.Create(&database.Product{ID: "p1", SourceID: "s1", Name: "Product"})
	db.Create(&database.Delivery{ID: "d1", ProductID: "p1", Name: "Delivery"})
	db.Create(&database.File{ID: "f1", DeliveryID: "d1", ProductID: "p1", SourceID: "s1", FileName: "test.txt", Skipped: false})

	// Skip
	req := httptest.NewRequest(http.MethodPut, "/api/files/f1/skip", nil)
	w := httptest.NewRecorder()
	handler.SkipFile(w, req, "f1")

	if w.Code != http.StatusOK {
		t.Errorf("SkipFile status = %d, want %d", w.Code, http.StatusOK)
	}

	var file database.File
	db.First(&file, "id = ?", "f1")
	if !file.Skipped {
		t.Error("File should be skipped")
	}

	// Unskip
	req = httptest.NewRequest(http.MethodDelete, "/api/files/f1/skip", nil)
	w = httptest.NewRecorder()
	handler.UnskipFile(w, req, "f1")

	if w.Code != http.StatusOK {
		t.Errorf("UnskipFile status = %d, want %d", w.Code, http.StatusOK)
	}

	db.First(&file, "id = ?", "f1")
	if file.Skipped {
		t.Error("File should not be skipped")
	}
}
