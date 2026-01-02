package epo

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/patent-dev/bulk-file-loader/internal/sources"
	bdds "github.com/patent-dev/epo-bdds"
)

const (
	SourceID   = "epo-bdds"
	SourceName = "EPO BDDS"
)

// Adapter implements the sources.Adapter interface for EPO BDDS
type Adapter struct {
	client      *bdds.Client
	credentials map[string]string
}

// New creates a new EPO BDDS adapter
func New() *Adapter {
	return &Adapter{
		credentials: make(map[string]string),
	}
}

// ID returns the source identifier
func (a *Adapter) ID() string {
	return SourceID
}

// Name returns the human-readable source name
func (a *Adapter) Name() string {
	return SourceName
}

// CredentialFields returns the required credential fields
func (a *Adapter) CredentialFields() []sources.CredentialField {
	return []sources.CredentialField{
		{
			Key:      "username",
			Label:    "Username",
			Type:     "text",
			Required: true,
			HelpText: "Your EPO BDDS username",
		},
		{
			Key:      "password",
			Label:    "Password",
			Type:     "password",
			Required: true,
			HelpText: "Your EPO BDDS password",
		},
	}
}

// SetCredentials sets the credentials for the adapter
func (a *Adapter) SetCredentials(creds map[string]string) {
	a.credentials = creds
	a.client = nil // Reset client to force re-creation with new credentials
}

// ValidateCredentials tests if the credentials are valid
func (a *Adapter) ValidateCredentials(ctx context.Context) error {
	client, err := a.getClient()
	if err != nil {
		return err
	}

	// Try to list products to validate credentials
	_, err = client.ListProducts(ctx)
	if err != nil {
		return sources.NewAdapterError(sources.ErrCodeAuth, "Failed to authenticate with EPO BDDS", err)
	}

	return nil
}

// FetchProducts fetches all available products
func (a *Adapter) FetchProducts(ctx context.Context) ([]sources.ProductInfo, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	products, err := client.ListProducts(ctx)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNetwork, "Failed to fetch products", err)
	}

	result := make([]sources.ProductInfo, 0, len(products))
	for _, p := range products {
		result = append(result, sources.ProductInfo{
			ExternalID:    strconv.Itoa(p.ID),
			Name:          p.Name,
			Description:   p.Description,
			CheckSchedule: "0 6 * * *", // Default: 6 AM daily
		})
	}

	return result, nil
}

// FetchDeliveries fetches deliveries for a product
func (a *Adapter) FetchDeliveries(ctx context.Context, productID string) ([]sources.DeliveryInfo, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	pid, err := strconv.Atoi(productID)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "Invalid product ID", err)
	}

	product, err := client.GetProduct(ctx, pid)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNetwork, "Failed to fetch product", err)
	}

	result := make([]sources.DeliveryInfo, 0, len(product.Deliveries))
	for _, d := range product.Deliveries {
		info := sources.DeliveryInfo{
			ExternalID:  strconv.Itoa(d.DeliveryID),
			Name:        d.DeliveryName,
			PublishedAt: d.DeliveryPublicationDatetime,
		}
		if d.DeliveryExpiryDatetime != nil {
			info.ExpiresAt = d.DeliveryExpiryDatetime
		}
		result = append(result, info)
	}

	return result, nil
}

// FetchFiles fetches files for a delivery
func (a *Adapter) FetchFiles(ctx context.Context, productID, deliveryID string) ([]sources.FileInfo, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	pid, err := strconv.Atoi(productID)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "Invalid product ID", err)
	}

	product, err := client.GetProduct(ctx, pid)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNetwork, "Failed to fetch product", err)
	}

	// Find the delivery
	var delivery *bdds.Delivery
	for _, d := range product.Deliveries {
		if strconv.Itoa(d.DeliveryID) == deliveryID {
			delivery = d
			break
		}
	}

	if delivery == nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNotFound, "Delivery not found", nil)
	}

	result := make([]sources.FileInfo, 0, len(delivery.Files))
	for _, f := range delivery.Files {
		// Parse file size from string (e.g., "1.5 GB")
		fileSize := parseFileSize(f.FileSize)

		result = append(result, sources.FileInfo{
			ExternalID:        strconv.Itoa(f.FileID),
			FileName:          f.FileName,
			FileSize:          fileSize,
			Checksum:          f.FileChecksum,
			ChecksumAlgorithm: "md5", // EPO uses MD5
			DownloadURI:       fmt.Sprintf("%d/%d/%d", pid, delivery.DeliveryID, f.FileID),
			ReleasedAt:        f.FilePublicationDatetime,
		})
	}

	return result, nil
}

// DownloadFile downloads a file
func (a *Adapter) DownloadFile(ctx context.Context, file sources.FileInfo, dst io.Writer, progress sources.ProgressFunc) error {
	client, err := a.getClient()
	if err != nil {
		return err
	}

	// Parse product, delivery, file IDs from download URI
	var productID, deliveryID, fileID int
	_, err = fmt.Sscanf(file.DownloadURI, "%d/%d/%d", &productID, &deliveryID, &fileID)
	if err != nil {
		return sources.NewAdapterError(sources.ErrCodeInvalidConfig, "Invalid download URI", err)
	}

	// Download with progress
	err = client.DownloadFileWithProgress(ctx, productID, deliveryID, fileID, dst, func(bytesWritten, totalBytes int64) {
		if progress != nil {
			progress(bytesWritten, totalBytes)
		}
	})

	if err != nil {
		return err // Pass through original error, downloader will add context
	}

	return nil
}

// getClient returns or creates the BDDS client
func (a *Adapter) getClient() (*bdds.Client, error) {
	if a.client != nil {
		return a.client, nil
	}

	username := a.credentials["username"]
	password := a.credentials["password"]

	if username == "" || password == "" {
		return nil, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "Missing credentials", nil)
	}

	client, err := bdds.NewClient(&bdds.Config{
		Username: username,
		Password: password,
	})
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeAuth, "Failed to create client", err)
	}

	a.client = client
	return client, nil
}

// parseFileSize parses a human-readable file size string to bytes
func parseFileSize(sizeStr string) int64 {
	// Simple implementation - EPO returns sizes like "1.5 GB", "500 MB"
	var size float64
	var unit string
	_, err := fmt.Sscanf(sizeStr, "%f %s", &size, &unit)
	if err != nil {
		return 0
	}

	multipliers := map[string]float64{
		"B":  1,
		"KB": 1024,
		"MB": 1024 * 1024,
		"GB": 1024 * 1024 * 1024,
		"TB": 1024 * 1024 * 1024 * 1024,
	}

	if mult, ok := multipliers[unit]; ok {
		return int64(size * mult)
	}

	return 0
}
