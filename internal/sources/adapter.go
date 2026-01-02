package sources

import (
	"context"
	"io"
	"time"
)

// Adapter defines the interface for patent office API adapters
type Adapter interface {
	// Identity
	ID() string
	Name() string

	// Credentials
	CredentialFields() []CredentialField
	SetCredentials(creds map[string]string)
	ValidateCredentials(ctx context.Context) error

	// Data fetching
	FetchProducts(ctx context.Context) ([]ProductInfo, error)
	FetchDeliveries(ctx context.Context, productID string) ([]DeliveryInfo, error)
	FetchFiles(ctx context.Context, productID, deliveryID string) ([]FileInfo, error)

	// Download
	DownloadFile(ctx context.Context, file FileInfo, dst io.Writer, progress ProgressFunc) error
}

// CredentialField defines a credential input field
type CredentialField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"` // "text", "password"
	Required bool   `json:"required"`
	HelpText string `json:"helpText,omitempty"`
}

// ProductInfo represents product metadata from an API
type ProductInfo struct {
	ExternalID    string `json:"externalId"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	CheckSchedule string `json:"checkSchedule"` // Default cron schedule for this product
}

// DeliveryInfo represents delivery/release metadata from an API
type DeliveryInfo struct {
	ExternalID  string     `json:"externalId"`
	Name        string     `json:"name"`
	PublishedAt time.Time  `json:"publishedAt"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
}

// FileInfo represents file metadata from an API
type FileInfo struct {
	ExternalID        string    `json:"externalId"`
	FileName          string    `json:"fileName"`
	FileSize          int64     `json:"fileSize"`
	Checksum          string    `json:"checksum,omitempty"`
	ChecksumAlgorithm string    `json:"checksumAlgorithm,omitempty"`
	DownloadURI       string    `json:"downloadUri"`
	ReleasedAt        time.Time `json:"releasedAt"`
}

// ProgressFunc is called during file downloads to report progress
type ProgressFunc func(bytesWritten, totalBytes int64)

// AdapterError represents an error from an adapter
type AdapterError struct {
	Code    string
	Message string
	Err     error
}

func (e *AdapterError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *AdapterError) Unwrap() error {
	return e.Err
}

// Common error codes
const (
	ErrCodeAuth          = "AUTH_ERROR"
	ErrCodeNotFound      = "NOT_FOUND"
	ErrCodeRateLimit     = "RATE_LIMITED"
	ErrCodeNetwork       = "NETWORK_ERROR"
	ErrCodeInvalidConfig = "INVALID_CONFIG"
)

// NewAdapterError creates a new adapter error
func NewAdapterError(code, message string, err error) *AdapterError {
	return &AdapterError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}
