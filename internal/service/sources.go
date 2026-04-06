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

	if enabledVal {
		if err := s.SyncSourceProducts(id); err != nil {
			slog.Error("Failed to sync products on enable", "source", id, "error", err)
		}
		if s.Scheduler != nil {
			go func() {
				if err := s.SyncSourceFiles(id); err != nil {
					slog.Error("Failed to sync files on enable", "source", id, "error", err)
				}
			}()
		} else {
			if err := s.SyncSourceFiles(id); err != nil {
				slog.Error("Failed to sync files on enable", "source", id, "error", err)
			}
		}
	}

	return s.GetSource(id)
}

func (s *Service) TestCredentials(ctx context.Context, id string, credentials map[string]string) error {
	return s.Registry.TestCredentials(ctx, id, credentials)
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

func (s *Service) SyncSourceFiles(sourceID string) error {
	ctx := context.Background()
	slog.Info("Syncing files", "source", sourceID)

	adapter, ok := s.Registry.Get(sourceID)
	if !ok {
		return fmt.Errorf("adapter not found for source: %s", sourceID)
	}

	var products []database.Product
	if err := s.DB.Where("source_id = ?", sourceID).Find(&products).Error; err != nil {
		return fmt.Errorf("failed to get products for source %s: %w", sourceID, err)
	}

	for _, p := range products {
		s.syncProductDeliveriesAndFiles(ctx, adapter, sourceID, p.ID, p.ExternalID)
	}
	slog.Info("File sync completed", "source", sourceID)
	return nil
}

func (s *Service) syncProductDeliveriesAndFiles(ctx context.Context, adapter sources.Adapter, sourceID, productID, externalProductID string) {
	deliveries, err := adapter.FetchDeliveries(ctx, externalProductID)
	if err != nil {
		slog.Error("Failed to fetch deliveries", "product", productID, "error", err)
		return
	}

	totalFiles := 0
	for _, d := range deliveries {
		deliveryID := fmt.Sprintf("%s:%s", productID, d.ExternalID)
		delivery := database.Delivery{
			ID:          deliveryID,
			ProductID:   productID,
			ExternalID:  d.ExternalID,
			Name:        d.Name,
			PublishedAt: &d.PublishedAt,
			ExpiresAt:   d.ExpiresAt,
		}
		if err := s.DB.Save(&delivery).Error; err != nil {
			slog.Error("Failed to save delivery", "deliveryID", deliveryID, "error", err)
			continue
		}

		files, err := adapter.FetchFiles(ctx, externalProductID, d.ExternalID)
		if err != nil {
			slog.Error("Failed to fetch files", "deliveryID", deliveryID, "error", err)
			continue
		}

		for _, f := range files {
			fileID := fmt.Sprintf("%s:%s", deliveryID, f.ExternalID)
			file := database.File{
				ID:                fileID,
				DeliveryID:        deliveryID,
				ProductID:         productID,
				SourceID:          sourceID,
				ExternalID:        f.ExternalID,
				FileName:          f.FileName,
				FileSize:          f.FileSize,
				ExpectedChecksum:  f.Checksum,
				ChecksumAlgorithm: f.ChecksumAlgorithm,
				DownloadURI:       f.DownloadURI,
				ReleasedAt:        &f.ReleasedAt,
			}
			if err := s.DB.Save(&file).Error; err != nil {
				slog.Error("Failed to save file", "fileID", fileID, "error", err)
				continue
			}
			totalFiles++
		}
	}
	slog.Debug("Synced files", "product", productID, "count", totalFiles)
}

func convertSourceInfo(si sources.SourceInfo) generated.Source {
	source := generated.Source{
		Id:             si.ID,
		Name:           si.Name,
		Enabled:        si.Enabled,
		HasCredentials: si.HasCredentials,
		LastSyncAt:     si.LastSyncAt,
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
