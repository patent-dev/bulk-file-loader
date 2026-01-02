package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/patent-dev/bulk-file-loader/config"
	"github.com/patent-dev/bulk-file-loader/internal/database"
)

// Registry manages source adapters
type Registry struct {
	db       *database.DB
	cfg      *config.Config
	adapters map[string]Adapter
	mu       sync.RWMutex
}

// NewRegistry creates a new source registry
func NewRegistry(db *database.DB, cfg *config.Config) *Registry {
	return &Registry{
		db:       db,
		cfg:      cfg,
		adapters: make(map[string]Adapter),
	}
}

// RegisterBuiltinAdapters registers the built-in source adapters
// This is called from main.go to avoid import cycles
func (r *Registry) RegisterBuiltinAdapters(adapters ...Adapter) {
	for _, adapter := range adapters {
		r.Register(adapter)
	}
}

// Register adds an adapter to the registry
func (r *Registry) Register(adapter Adapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[adapter.ID()] = adapter
}

// Get returns an adapter by ID
func (r *Registry) Get(id string) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[id]
	return adapter, ok
}

// List returns all registered adapters
func (r *Registry) List() []Adapter {
	r.mu.RLock()
	defer r.mu.RUnlock()

	adapters := make([]Adapter, 0, len(r.adapters))
	for _, a := range r.adapters {
		adapters = append(adapters, a)
	}
	return adapters
}

// ListSources returns all sources with their database state
func (r *Registry) ListSources() ([]SourceInfo, error) {
	adapters := r.List()
	sources := make([]SourceInfo, 0, len(adapters))

	for _, adapter := range adapters {
		info := SourceInfo{
			ID:               adapter.ID(),
			Name:             adapter.Name(),
			CredentialFields: adapter.CredentialFields(),
		}

		// Load database state
		var dbSource database.Source
		if err := r.db.Where("id = ?", adapter.ID()).First(&dbSource).Error; err == nil {
			info.Enabled = dbSource.Enabled
			info.LastSyncAt = dbSource.LastSyncAt
			info.HasCredentials = len(dbSource.CredentialsEnc) > 0
		}

		sources = append(sources, info)
	}

	// Sort by name for consistent ordering
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].Name < sources[j].Name
	})

	return sources, nil
}

// GetSource returns a source by ID with its database state
func (r *Registry) GetSource(id string) (*SourceInfo, error) {
	adapter, ok := r.Get(id)
	if !ok {
		return nil, fmt.Errorf("source not found: %s", id)
	}

	info := &SourceInfo{
		ID:               adapter.ID(),
		Name:             adapter.Name(),
		CredentialFields: adapter.CredentialFields(),
	}

	var dbSource database.Source
	if err := r.db.Where("id = ?", id).First(&dbSource).Error; err == nil {
		info.Enabled = dbSource.Enabled
		info.LastSyncAt = dbSource.LastSyncAt
		info.HasCredentials = len(dbSource.CredentialsEnc) > 0
	}

	return info, nil
}

// CredentialDecryptorEncryptor combines both interfaces for UpdateSource
type CredentialDecryptorEncryptor interface {
	CredentialEncryptor
	CredentialDecryptor
}

// UpdateSource updates source configuration
func (r *Registry) UpdateSource(id string, enabled bool, credentials map[string]string, cryptor CredentialDecryptorEncryptor) error {
	adapter, ok := r.Get(id)
	if !ok {
		return fmt.Errorf("source not found: %s", id)
	}

	// Load existing source from database
	var existingSource database.Source
	r.db.Where("id = ?", id).First(&existingSource)

	// Start with existing credentials
	credentialsEnc := existingSource.CredentialsEnc

	// If new credentials provided, encrypt and store them
	if len(credentials) > 0 {
		credJSON, err := json.Marshal(credentials)
		if err != nil {
			return fmt.Errorf("failed to marshal credentials: %w", err)
		}
		credentialsEnc, err = cryptor.EncryptCredentials(credJSON)
		if err != nil {
			return fmt.Errorf("failed to encrypt credentials: %w", err)
		}

		// Set credentials on adapter
		adapter.SetCredentials(credentials)
	} else if len(existingSource.CredentialsEnc) > 0 {
		// Load and set existing credentials on adapter
		credJSON, err := cryptor.DecryptCredentials(existingSource.CredentialsEnc)
		if err == nil {
			var existingCreds map[string]string
			if json.Unmarshal(credJSON, &existingCreds) == nil {
				adapter.SetCredentials(existingCreds)
			}
		}
	}

	// Upsert source in database
	source := database.Source{
		ID:             id,
		Name:           adapter.Name(),
		Enabled:        enabled,
		CredentialsEnc: credentialsEnc,
	}

	return r.db.Save(&source).Error
}

// TestCredentials tests if the credentials for a source are valid
func (r *Registry) TestCredentials(ctx context.Context, id string, credentials map[string]string) error {
	adapter, ok := r.Get(id)
	if !ok {
		return fmt.Errorf("source not found: %s", id)
	}

	// Temporarily set credentials
	adapter.SetCredentials(credentials)

	// Validate
	return adapter.ValidateCredentials(ctx)
}

// LoadCredentialsWithDecryptor loads and decrypts credentials for all sources
func (r *Registry) LoadCredentialsWithDecryptor(decryptor CredentialDecryptor) error {
	var sources []database.Source
	if err := r.db.Find(&sources).Error; err != nil {
		return err
	}

	for _, source := range sources {
		if len(source.CredentialsEnc) == 0 {
			continue
		}

		adapter, ok := r.Get(source.ID)
		if !ok {
			continue
		}

		credJSON, err := decryptor.DecryptCredentials(source.CredentialsEnc)
		if err != nil {
			continue
		}

		var credentials map[string]string
		if err := json.Unmarshal(credJSON, &credentials); err != nil {
			continue
		}

		adapter.SetCredentials(credentials)
	}

	return nil
}

// SourceInfo contains source metadata and state
type SourceInfo struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Enabled          bool              `json:"enabled"`
	HasCredentials   bool              `json:"hasCredentials"`
	LastSyncAt       *time.Time        `json:"lastSyncAt,omitempty"`
	CredentialFields []CredentialField `json:"credentialFields"`
}

// CredentialEncryptor interface for encrypting credentials
type CredentialEncryptor interface {
	EncryptCredentials(plaintext []byte) ([]byte, error)
}

// CredentialDecryptor interface for decrypting credentials
type CredentialDecryptor interface {
	DecryptCredentials(ciphertext []byte) ([]byte, error)
}
