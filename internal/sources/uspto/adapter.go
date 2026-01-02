package uspto

import (
	"context"
	"io"
	"regexp"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/sources"
	odp "github.com/patent-dev/uspto-odp"
)

// dateFromFilenameRegex extracts YYYYMMDD date pattern from filenames like "2000-PEDS-full-20250316-json.zip"
var dateFromFilenameRegex = regexp.MustCompile(`-(\d{8})-`)

const (
	SourceID   = "uspto-odp"
	SourceName = "USPTO ODP"
)

// Adapter implements the sources.Adapter interface for USPTO ODP
type Adapter struct {
	client      *odp.Client
	credentials map[string]string
}

// New creates a new USPTO ODP adapter
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
			Key:      "api_key",
			Label:    "API Key",
			Type:     "password",
			Required: true,
			HelpText: "Your USPTO ODP API key from https://data.uspto.gov/apis/getting-started",
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

	// Try to search bulk products to validate credentials
	_, err = client.SearchBulkProducts(ctx, "", 0, 1)
	if err != nil {
		return sources.NewAdapterError(sources.ErrCodeAuth, "Failed to authenticate with USPTO ODP", err)
	}

	return nil
}

// FetchProducts fetches all available bulk products
func (a *Adapter) FetchProducts(ctx context.Context) ([]sources.ProductInfo, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	// Search for all bulk products
	resp, err := client.SearchBulkProducts(ctx, "", 0, 1000)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNetwork, "Failed to fetch products", err)
	}

	if resp.BulkDataProductBag == nil {
		return []sources.ProductInfo{}, nil
	}

	result := make([]sources.ProductInfo, 0, len(*resp.BulkDataProductBag))
	for _, p := range *resp.BulkDataProductBag {
		var id, name, description string
		if p.ProductIdentifier != nil {
			id = *p.ProductIdentifier
		}
		if p.ProductTitleText != nil {
			name = *p.ProductTitleText
		}
		if p.ProductDescriptionText != nil {
			description = *p.ProductDescriptionText
		}

		result = append(result, sources.ProductInfo{
			ExternalID:    id,
			Name:          name,
			Description:   description,
			CheckSchedule: "0 6 * * TUE", // Default: 6 AM every Tuesday (USPTO typical release day)
		})
	}

	return result, nil
}

// FetchDeliveries fetches deliveries for a product
// USPTO doesn't have a delivery concept, so we synthesize one from the product
func (a *Adapter) FetchDeliveries(ctx context.Context, productID string) ([]sources.DeliveryInfo, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	product, err := client.GetBulkProduct(ctx, productID)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNetwork, "Failed to fetch product", err)
	}

	if product.BulkDataProductBag == nil || len(*product.BulkDataProductBag) == 0 {
		return nil, sources.NewAdapterError(sources.ErrCodeNotFound, "Product not found", nil)
	}

	p := (*product.BulkDataProductBag)[0]

	// Synthesize a single delivery from the product
	var publishedAt time.Time
	if p.LastModifiedDateTime != nil {
		publishedAt, _ = time.Parse(time.RFC3339, *p.LastModifiedDateTime)
	}

	return []sources.DeliveryInfo{
		{
			ExternalID:  "latest",
			Name:        "Latest",
			PublishedAt: publishedAt,
		},
	}, nil
}

// FetchFiles fetches files for a delivery
func (a *Adapter) FetchFiles(ctx context.Context, productID, deliveryID string) ([]sources.FileInfo, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	product, err := client.GetBulkProduct(ctx, productID)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeNetwork, "Failed to fetch product", err)
	}

	if product.BulkDataProductBag == nil || len(*product.BulkDataProductBag) == 0 {
		return nil, sources.NewAdapterError(sources.ErrCodeNotFound, "Product not found", nil)
	}

	p := (*product.BulkDataProductBag)[0]
	if p.ProductFileBag == nil || p.ProductFileBag.FileDataBag == nil {
		return []sources.FileInfo{}, nil
	}

	result := make([]sources.FileInfo, 0, len(*p.ProductFileBag.FileDataBag))
	for _, f := range *p.ProductFileBag.FileDataBag {
		var fileName, downloadURI string
		var fileSize int64
		var releasedAt time.Time

		if f.FileName != nil {
			fileName = *f.FileName
		}
		if f.FileDownloadURI != nil {
			downloadURI = *f.FileDownloadURI
		}
		if f.FileSize != nil {
			fileSize = int64(*f.FileSize)
		}
		if f.FileReleaseDate != nil && *f.FileReleaseDate != "" {
			// Try multiple date formats - USPTO uses various formats
			for _, layout := range []string{
				time.RFC3339,
				"2006-01-02T15:04:05Z",
				"2006-01-02T15:04:05.000Z",
				"2006-01-02T15:04:05",
				"2006-01-02 15:04:05",
				"2006-01-02",
				"2006/01/02",
				"01/02/2006",
				"1/2/2006",
				"01-02-2006",
				"January 2, 2006",
				"Jan 2, 2006",
				"20060102",
			} {
				if t, err := time.Parse(layout, *f.FileReleaseDate); err == nil {
					releasedAt = t
					break
				}
			}
		}

		// Fallback: extract date from filename if not parsed (e.g., "2000-PEDS-full-20250316-json.zip")
		if releasedAt.IsZero() && fileName != "" {
			if matches := dateFromFilenameRegex.FindStringSubmatch(fileName); len(matches) > 1 {
				if t, err := time.Parse("20060102", matches[1]); err == nil {
					releasedAt = t
				}
			}
		}

		result = append(result, sources.FileInfo{
			ExternalID:  fileName, // Use filename as ID since USPTO doesn't provide file IDs
			FileName:    fileName,
			FileSize:    fileSize,
			DownloadURI: downloadURI,
			ReleasedAt:  releasedAt,
			// USPTO doesn't provide checksums
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

	// Download with progress
	err = client.DownloadBulkFileWithProgress(ctx, file.DownloadURI, dst, func(bytesComplete, bytesTotal int64) {
		if progress != nil {
			progress(bytesComplete, bytesTotal)
		}
	})

	if err != nil {
		return err // Pass through original error, downloader will add context
	}

	return nil
}

// getClient returns or creates the ODP client
func (a *Adapter) getClient() (*odp.Client, error) {
	if a.client != nil {
		return a.client, nil
	}

	apiKey := a.credentials["api_key"]
	if apiKey == "" {
		return nil, sources.NewAdapterError(sources.ErrCodeInvalidConfig, "Missing API key", nil)
	}

	// Start with default config and set API key
	cfg := odp.DefaultConfig()
	cfg.APIKey = apiKey
	cfg.Timeout = 3600 // 1 hour timeout for large file downloads

	client, err := odp.NewClient(cfg)
	if err != nil {
		return nil, sources.NewAdapterError(sources.ErrCodeAuth, "Failed to create client", err)
	}

	a.client = client
	return client, nil
}
