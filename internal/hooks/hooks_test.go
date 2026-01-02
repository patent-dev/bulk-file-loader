package hooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	gormDB.AutoMigrate(&database.Webhook{})
	return &database.DB{DB: gormDB}
}

func TestCreateWebhook(t *testing.T) {
	db := setupTestDB(t)
	manager := New(db)

	webhook, err := manager.CreateWebhook("Test Hook", "https://example.com/hook", []string{"download.completed", "download.failed"})
	if err != nil {
		t.Fatal(err)
	}

	if webhook.ID == 0 {
		t.Error("Webhook ID should be set")
	}
	if webhook.Name != "Test Hook" {
		t.Errorf("Name = %q, want Test Hook", webhook.Name)
	}
	if !webhook.Enabled {
		t.Error("Webhook should be enabled by default")
	}
}

func TestListWebhooks(t *testing.T) {
	db := setupTestDB(t)
	manager := New(db)

	manager.CreateWebhook("Hook 1", "https://example.com/1", []string{"*"})
	manager.CreateWebhook("Hook 2", "https://example.com/2", []string{"download.completed"})

	webhooks, err := manager.ListWebhooks()
	if err != nil {
		t.Fatal(err)
	}
	if len(webhooks) != 2 {
		t.Errorf("ListWebhooks() returned %d, want 2", len(webhooks))
	}
}

func TestGetWebhook(t *testing.T) {
	db := setupTestDB(t)
	manager := New(db)

	created, _ := manager.CreateWebhook("Test", "https://example.com", []string{"*"})
	retrieved, err := manager.GetWebhook(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retrieved.Name != "Test" {
		t.Errorf("Name = %q, want Test", retrieved.Name)
	}
}

func TestUpdateWebhook(t *testing.T) {
	db := setupTestDB(t)
	manager := New(db)

	webhook, _ := manager.CreateWebhook("Original", "https://original.com", []string{"*"})

	err := manager.UpdateWebhook(webhook.ID, "Updated", "https://updated.com", []string{"download.completed"}, false)
	if err != nil {
		t.Fatal(err)
	}

	updated, _ := manager.GetWebhook(webhook.ID)
	if updated.Name != "Updated" {
		t.Errorf("Name = %q, want Updated", updated.Name)
	}
	if updated.URL != "https://updated.com" {
		t.Errorf("URL = %q, want https://updated.com", updated.URL)
	}
	if updated.Enabled {
		t.Error("Enabled = true, want false")
	}
}

func TestDeleteWebhook(t *testing.T) {
	db := setupTestDB(t)
	manager := New(db)

	webhook, _ := manager.CreateWebhook("ToDelete", "https://example.com", []string{"*"})
	if err := manager.DeleteWebhook(webhook.ID); err != nil {
		t.Fatal(err)
	}

	_, err := manager.GetWebhook(webhook.ID)
	if err == nil {
		t.Error("GetWebhook after delete should return error")
	}
}

func TestEmitDelivers(t *testing.T) {
	db := setupTestDB(t)
	manager := New(db)

	var received atomic.Bool
	var receivedEvent Event

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedEvent)
		received.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	manager.CreateWebhook("Test", server.URL, []string{"download.completed"})

	event := NewEvent(EventDownloadCompleted, "source-1").
		WithFile("file-1", "test.zip", 1024, "sha256:abc", "/downloads/test.zip")

	manager.Emit(context.Background(), event)

	// Wait for async delivery
	time.Sleep(100 * time.Millisecond)

	if !received.Load() {
		t.Error("Webhook was not delivered")
	}
	if receivedEvent.Type != EventDownloadCompleted {
		t.Errorf("Event type = %q, want %q", receivedEvent.Type, EventDownloadCompleted)
	}
	if receivedEvent.Source != "source-1" {
		t.Errorf("Source = %q, want source-1", receivedEvent.Source)
	}
}

func TestEmitMatchesEvents(t *testing.T) {
	db := setupTestDB(t)
	manager := New(db)

	var completedCount, failedCount atomic.Int32

	completedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		completedCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer completedServer.Close()

	failedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failedCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer failedServer.Close()

	manager.CreateWebhook("Completed Only", completedServer.URL, []string{"download.completed"})
	manager.CreateWebhook("Failed Only", failedServer.URL, []string{"download.failed"})

	// Emit completed event
	manager.Emit(context.Background(), NewEvent(EventDownloadCompleted, "s1"))
	time.Sleep(100 * time.Millisecond)

	if completedCount.Load() != 1 {
		t.Errorf("completedCount = %d, want 1", completedCount.Load())
	}
	if failedCount.Load() != 0 {
		t.Errorf("failedCount = %d, want 0", failedCount.Load())
	}

	// Emit failed event
	manager.Emit(context.Background(), NewEvent(EventDownloadFailed, "s1"))
	time.Sleep(100 * time.Millisecond)

	if completedCount.Load() != 1 {
		t.Errorf("completedCount = %d, want 1", completedCount.Load())
	}
	if failedCount.Load() != 1 {
		t.Errorf("failedCount = %d, want 1", failedCount.Load())
	}
}

func TestEmitWildcard(t *testing.T) {
	db := setupTestDB(t)
	manager := New(db)

	var count atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	manager.CreateWebhook("All Events", server.URL, []string{"*"})

	manager.Emit(context.Background(), NewEvent(EventDownloadCompleted, "s1"))
	manager.Emit(context.Background(), NewEvent(EventDownloadFailed, "s1"))
	manager.Emit(context.Background(), NewEvent(EventFileAvailable, "s1"))

	time.Sleep(200 * time.Millisecond)

	if count.Load() != 3 {
		t.Errorf("count = %d, want 3", count.Load())
	}
}

func TestDisabledWebhookNotDelivered(t *testing.T) {
	db := setupTestDB(t)
	manager := New(db)

	var received atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	webhook, _ := manager.CreateWebhook("Disabled", server.URL, []string{"*"})
	manager.UpdateWebhook(webhook.ID, webhook.Name, webhook.URL, []string{"*"}, false)

	manager.Emit(context.Background(), NewEvent(EventDownloadCompleted, "s1"))
	time.Sleep(100 * time.Millisecond)

	if received.Load() {
		t.Error("Disabled webhook should not be delivered")
	}
}

func TestParseEvents(t *testing.T) {
	events := ParseEvents(`["download.completed","download.failed"]`)
	if len(events) != 2 {
		t.Errorf("ParseEvents returned %d events, want 2", len(events))
	}
	if events[0] != "download.completed" {
		t.Errorf("events[0] = %q, want download.completed", events[0])
	}
}

func TestAllEvents(t *testing.T) {
	events := AllEvents()
	if len(events) == 0 {
		t.Error("AllEvents() should not be empty")
	}

	// Verify expected events exist
	expected := []string{
		EventFileAvailable,
		EventDownloadStarted,
		EventDownloadCompleted,
		EventDownloadFailed,
	}
	for _, e := range expected {
		found := false
		for _, event := range events {
			if event == e {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("AllEvents() missing %q", e)
		}
	}
}

func TestIsValidEvent(t *testing.T) {
	tests := []struct {
		event string
		valid bool
	}{
		{"download.completed", true},
		{"download.failed", true},
		{"file.available", true},
		{"*", true},
		{"invalid.event", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := IsValidEvent(tt.event); got != tt.valid {
			t.Errorf("IsValidEvent(%q) = %v, want %v", tt.event, got, tt.valid)
		}
	}
}

func TestEventBuilder(t *testing.T) {
	event := NewEvent(EventDownloadCompleted, "source-1").
		WithProduct("prod-1", "Product Name").
		WithDelivery("del-1", "Delivery Name").
		WithFile("file-1", "test.zip", 2048, "sha256:xyz", "/path/to/file").
		WithError("ERR_CODE", "Error message").
		WithAlert("checksum_mismatch", "Checksum mismatch", "warning")

	if event.Type != EventDownloadCompleted {
		t.Errorf("Type = %q, want %q", event.Type, EventDownloadCompleted)
	}
	if event.Source != "source-1" {
		t.Errorf("Source = %q, want source-1", event.Source)
	}
	if event.Product == nil || event.Product.ID != "prod-1" {
		t.Error("Product not set correctly")
	}
	if event.Delivery == nil || event.Delivery.ID != "del-1" {
		t.Error("Delivery not set correctly")
	}
	if event.File == nil || event.File.Name != "test.zip" {
		t.Error("File not set correctly")
	}
	if event.Error == nil || event.Error.Code != "ERR_CODE" {
		t.Error("Error not set correctly")
	}
	if len(event.Alerts) != 1 || event.Alerts[0].Type != "checksum_mismatch" {
		t.Error("Alerts not set correctly")
	}
}
