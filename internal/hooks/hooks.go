package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/database"
)

type Manager struct {
	db         *database.DB
	httpClient *http.Client
}

func New(db *database.DB) *Manager {
	return &Manager{
		db:         db,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (m *Manager) Emit(ctx context.Context, event *Event) {
	webhooks, err := m.getWebhooksForEvent(event.Type)
	if err != nil {
		slog.Error("Failed to get webhooks", "error", err)
		return
	}
	for _, webhook := range webhooks {
		go m.deliverWebhook(ctx, webhook, event)
	}
}

func (m *Manager) getWebhooksForEvent(eventType string) ([]database.Webhook, error) {
	var webhooks []database.Webhook
	if err := m.db.Where("enabled = ?", true).Find(&webhooks).Error; err != nil {
		return nil, err
	}

	var matching []database.Webhook
	for _, wh := range webhooks {
		var events []string
		if json.Unmarshal([]byte(wh.Events), &events) != nil {
			continue
		}
		for _, e := range events {
			if e == eventType || e == "*" {
				matching = append(matching, wh)
				break
			}
		}
	}
	return matching, nil
}

func (m *Manager) deliverWebhook(ctx context.Context, webhook database.Webhook, event *Event) {
	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("Failed to marshal event", "error", err, "webhookID", webhook.ID)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook.URL, bytes.NewReader(payload))
	if err != nil {
		slog.Error("Failed to create request", "error", err, "webhookID", webhook.ID)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "BulkFileLoader/1.0")

	if len(webhook.Headers) > 0 {
		var headers map[string]string
		if json.Unmarshal(webhook.Headers, &headers) == nil {
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		slog.Error("Webhook delivery failed", "error", err, "webhookID", webhook.ID)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		slog.Warn("Webhook error", "status", resp.StatusCode, "webhookID", webhook.ID)
	}
}

func (m *Manager) CreateWebhook(name, url string, events []string) (*database.Webhook, error) {
	eventsJSON, err := json.Marshal(events)
	if err != nil {
		return nil, err
	}
	webhook := &database.Webhook{
		Name:    name,
		URL:     url,
		Events:  string(eventsJSON),
		Enabled: true,
	}
	if err := m.db.Create(webhook).Error; err != nil {
		return nil, err
	}
	return webhook, nil
}

func (m *Manager) UpdateWebhook(id uint, name, url string, events []string, enabled bool) error {
	eventsJSON, err := json.Marshal(events)
	if err != nil {
		return err
	}
	return m.db.Model(&database.Webhook{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":    name,
		"url":     url,
		"events":  string(eventsJSON),
		"enabled": enabled,
	}).Error
}

func (m *Manager) DeleteWebhook(id uint) error {
	return m.db.Delete(&database.Webhook{}, id).Error
}

func (m *Manager) ListWebhooks() ([]database.Webhook, error) {
	var webhooks []database.Webhook
	return webhooks, m.db.Find(&webhooks).Error
}

func (m *Manager) GetWebhook(id uint) (*database.Webhook, error) {
	var webhook database.Webhook
	if err := m.db.First(&webhook, id).Error; err != nil {
		return nil, err
	}
	return &webhook, nil
}

func ParseEvents(eventsJSON string) []string {
	var events []string
	json.Unmarshal([]byte(eventsJSON), &events)
	return events
}

func AllEvents() []string {
	return []string{
		EventFileAvailable,
		EventDownloadStarted,
		EventDownloadCompleted,
		EventDownloadFailed,
		EventDownloadCancelled,
		EventChecksumMismatch,
		EventSyncCompleted,
		EventSyncFailed,
	}
}

func IsValidEvent(event string) bool {
	if event == "*" {
		return true
	}
	for _, e := range AllEvents() {
		if strings.EqualFold(e, event) {
			return true
		}
	}
	return false
}
