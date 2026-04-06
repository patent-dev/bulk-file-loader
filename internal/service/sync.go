package service

import (
	"fmt"

	"github.com/patent-dev/bulk-file-loader/internal/database"
)

func (s *Service) SyncProductFull(productID string) error {
	var product database.Product
	if err := s.DB.First(&product, "id = ?", productID).Error; err != nil {
		return fmt.Errorf("product not found: %w", err)
	}

	if err := s.SyncSourceProducts(product.SourceID); err != nil {
		return fmt.Errorf("failed to sync products for source %s: %w", product.SourceID, err)
	}
	if err := s.SyncSourceFiles(product.SourceID); err != nil {
		return fmt.Errorf("failed to sync files for source %s: %w", product.SourceID, err)
	}
	return nil
}
