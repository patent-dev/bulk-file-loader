package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

func (s *Service) ListSources() ([]generated.Source, error) {
	sourceInfos, err := s.Registry.ListSources()
	if err != nil {
		return nil, fmt.Errorf("failed to list sources: %w", err)
	}

	result := make([]generated.Source, 0, len(sourceInfos))
	for _, si := range sourceInfos {
		result = append(result, convertSourceInfo(si))
	}
	return result, nil
}

func (s *Service) GetSource(id string) (generated.Source, error) {
	si, err := s.Registry.GetSource(id)
	if err != nil {
		return generated.Source{}, fmt.Errorf("source not found: %w", err)
	}
	return convertSourceInfo(*si), nil
}

func (s *Service) UpdateSource(ctx context.Context, id string, enabled *bool, credentials *map[string]string) (generated.Source, error) {
	var creds map[string]string
	if credentials != nil {
		creds = *credentials
	}

	// Resolve the effective enabled state: use the provided value, or fall
	// back to the current persisted state so we never silently flip the flag.
	currentSource, err := s.Registry.GetSource(id)
	if err != nil {
		return generated.Source{}, fmt.Errorf("source not found: %w", err)
	}
	enabledVal := currentSource.Enabled
	if enabled != nil {
		enabledVal = *enabled
	}

	if enabledVal && creds != nil {
		if err := s.Registry.TestCredentials(ctx, id, creds); err != nil {
			return generated.Source{}, fmt.Errorf("%w: %v", ErrInvalidCredentials, err)
		}
	}

	if err := s.Registry.UpdateSource(id, enabledVal, creds, s.Auth); err != nil {
		return generated.Source{}, err
	}

	// Enabling refreshes the product list (one request). The file catalogue
	// can take minutes: the server syncs it in the background, the CLI only
	// on request (product sync, pull, source enable --sync).
	if enabledVal {
		if err := s.SyncSourceProducts(id); err != nil {
			slog.Error("Failed to sync products on enable", "source", id, "error", err)
		}
		if s.Scheduler != nil {
			s.goBackground(func(ctx context.Context) {
				if err := s.SyncSourceFiles(ctx, id, nil); err != nil {
					slog.Error("Failed to sync files on enable", "source", id, "error", err)
				}
			})
		}
	}

	return s.GetSource(id)
}

func (s *Service) TestCredentials(ctx context.Context, id string, credentials map[string]string) error {
	return s.Registry.TestCredentials(ctx, id, credentials)
}

// TestStoredCredentials validates the credentials stored for a source.
func (s *Service) TestStoredCredentials(ctx context.Context, id string) error {
	return s.Registry.TestStoredCredentials(ctx, id)
}

func (s *Service) SyncSourceProducts(sourceID string) error {
	ctx := context.Background()
	slog.Info("Syncing products", "source", sourceID)

	adapter, ok := s.Registry.Get(sourceID)
	if !ok {
		return fmt.Errorf("adapter not found for source: %s", sourceID)
	}

	products, err := adapter.FetchProducts(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch products for source %s: %w", sourceID, err)
	}

	slog.Info("Found products", "source", sourceID, "count", len(products))
	for _, p := range products {
		productID := fmt.Sprintf("%s:%s", sourceID, p.ExternalID)
		var existing database.Product
		if err := s.DB.First(&existing, "id = ?", productID).Error; err != nil {
			// Product does not exist yet: create with adapter defaults
			newProduct := database.Product{
				ID:               productID,
				SourceID:         sourceID,
				ExternalID:       p.ExternalID,
				Name:             p.Name,
				Description:      p.Description,
				CheckWindowStart: p.CheckSchedule,
			}
			if err := s.DB.Create(&newProduct).Error; err != nil {
				slog.Error("Failed to create product", "productID", productID, "error", err)
			}
		} else {
			// Product exists: only update Name and Description, never overwrite
			// user-configured fields like AutoDownload, CheckWindowStart, CheckWindowEnd.
			if err := s.DB.Model(&existing).Updates(map[string]interface{}{
				"name":        p.Name,
				"description": p.Description,
			}).Error; err != nil {
				slog.Error("Failed to update product", "productID", productID, "error", err)
			}
		}
	}
	return nil
}

func convertSourceInfo(si sources.SourceInfo) generated.Source {
	source := generated.Source{
		Id:             si.ID,
		Name:           si.Name,
		Enabled:        si.Enabled,
		HasCredentials: si.HasCredentials,
		LastSyncAt:     si.LastSyncAt,
		// Required by the spec: an empty list, never null, for public sources.
		CredentialFields: []generated.CredentialField{},
	}
	for _, cf := range si.CredentialFields {
		helpText := cf.HelpText
		source.CredentialFields = append(source.CredentialFields, generated.CredentialField{
			Key:      cf.Key,
			Label:    cf.Label,
			Type:     generated.CredentialFieldType(cf.Type),
			Required: cf.Required,
			HelpText: &helpText,
		})
	}
	return source
}
