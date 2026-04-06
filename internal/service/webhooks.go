package service

import (
	"fmt"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
)

func (s *Service) ListWebhooks() ([]generated.Webhook, error) {
	webhooks, err := s.Hooks.ListWebhooks()
	if err != nil {
		return nil, fmt.Errorf("failed to list webhooks: %w", err)
	}

	result := make([]generated.Webhook, 0, len(webhooks))
	for _, wh := range webhooks {
		result = append(result, convertWebhook(wh))
	}
	return result, nil
}

func (s *Service) CreateWebhook(name, url string, events []string) (generated.Webhook, error) {
	webhook, err := s.Hooks.CreateWebhook(name, url, events)
	if err != nil {
		return generated.Webhook{}, fmt.Errorf("failed to create webhook: %w", err)
	}
	return convertWebhook(*webhook), nil
}

func (s *Service) UpdateWebhook(id int, req generated.UpdateWebhookRequest) (generated.Webhook, error) {
	webhook, err := s.Hooks.GetWebhook(uint(id))
	if err != nil {
		return generated.Webhook{}, fmt.Errorf("%w: webhook %d", ErrNotFound, id)
	}

	name := webhook.Name
	url := webhook.URL
	events := hooks.ParseEvents(webhook.Events)
	enabled := webhook.Enabled

	if req.Name != nil {
		name = *req.Name
	}
	if req.Url != nil {
		url = *req.Url
	}
	if req.Events != nil {
		events = *req.Events
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	if err := s.Hooks.UpdateWebhook(uint(id), name, url, events, enabled); err != nil {
		return generated.Webhook{}, fmt.Errorf("failed to update webhook: %w", err)
	}

	updated, err := s.Hooks.GetWebhook(uint(id))
	if err != nil {
		return generated.Webhook{}, fmt.Errorf("failed to retrieve updated webhook: %w", err)
	}
	return convertWebhook(*updated), nil
}

func (s *Service) DeleteWebhook(id int) error {
	return s.Hooks.DeleteWebhook(uint(id))
}

func convertWebhook(wh database.Webhook) generated.Webhook {
	return generated.Webhook{
		Id:        int(wh.ID),
		Name:      wh.Name,
		Url:       wh.URL,
		Events:    hooks.ParseEvents(wh.Events),
		Enabled:   wh.Enabled,
		CreatedAt: &wh.CreatedAt,
	}
}
