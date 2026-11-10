package sources

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/patent-dev/bulk-file-loader/config"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/database/dbtest"
)

type mockCryptor struct{}

func (m *mockCryptor) EncryptCredentials(plaintext []byte) ([]byte, error) {
	return append([]byte("enc:"), plaintext...), nil
}

func (m *mockCryptor) DecryptCredentials(ciphertext []byte) ([]byte, error) {
	if len(ciphertext) > 4 {
		return ciphertext[4:], nil
	}
	return ciphertext, nil
}

type mockAdapter struct {
	id    string
	name  string
	creds map[string]string
}

func (m *mockAdapter) ID() string                                           { return m.id }
func (m *mockAdapter) Name() string                                         { return m.name }
func (m *mockAdapter) CredentialFields() []CredentialField                  { return nil }
func (m *mockAdapter) SetCredentials(creds map[string]string)               { m.creds = creds }
func (m *mockAdapter) ValidateCredentials(context.Context) error            { return nil }
func (m *mockAdapter) FetchProducts(context.Context) ([]ProductInfo, error) { return nil, nil }
func (m *mockAdapter) FetchDeliveries(context.Context, string) ([]DeliveryInfo, error) {
	return nil, nil
}
func (m *mockAdapter) FetchFiles(context.Context, string, string) ([]FileInfo, error) {
	return nil, nil
}
func (m *mockAdapter) DownloadFile(context.Context, FileInfo, io.Writer, ProgressFunc) error {
	return nil
}

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.New(dbtest.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestUpdateSourcePreservesCredentials(t *testing.T) {
	db := setupTestDB(t)
	registry := NewRegistry(db, &config.Config{})
	cryptor := &mockCryptor{}

	adapter := &mockAdapter{id: "test-source", name: "Test Source"}
	registry.Register(adapter)

	if err := registry.UpdateSource("test-source", true, map[string]string{"api_key": "secret123"}, cryptor); err != nil {
		t.Fatal(err)
	}

	var source database.Source
	db.Where("id = ?", "test-source").First(&source)
	if len(source.CredentialsEnc) == 0 {
		t.Fatal("credentials should be saved")
	}

	if err := registry.UpdateSource("test-source", false, nil, cryptor); err != nil {
		t.Fatal(err)
	}

	db.Where("id = ?", "test-source").First(&source)
	if len(source.CredentialsEnc) == 0 {
		t.Fatal("credentials should be preserved")
	}

	if adapter.creds["api_key"] != "secret123" {
		t.Fatal("adapter should have existing credentials")
	}
}

type configurableMockAdapter struct {
	mockAdapter
	configuredTimeout int
}

func (m *configurableMockAdapter) Configure(cfg AdapterConfig) {
	m.configuredTimeout = cfg.DownloadTimeoutSeconds
}

func TestConfigurableAdapterReceivesConfig(t *testing.T) {
	db := setupTestDB(t)
	registry := NewRegistry(db, &config.Config{DownloadTimeout: 7200})

	adapter := &configurableMockAdapter{
		mockAdapter: mockAdapter{id: "configurable", name: "Configurable"},
	}
	registry.Register(adapter)

	if adapter.configuredTimeout != 7200 {
		t.Errorf("Configure() got timeout %d, want 7200", adapter.configuredTimeout)
	}
}

func TestNonConfigurableAdapterNotBroken(t *testing.T) {
	db := setupTestDB(t)
	registry := NewRegistry(db, &config.Config{DownloadTimeout: 7200})

	adapter := &mockAdapter{id: "plain", name: "Plain"}
	registry.Register(adapter) // should not panic

	got, ok := registry.Get("plain")
	if !ok || got.ID() != "plain" {
		t.Error("non-configurable adapter should still be registered")
	}
}

func TestUpdateSourceWithNewCredentials(t *testing.T) {
	db := setupTestDB(t)
	registry := NewRegistry(db, &config.Config{})
	cryptor := &mockCryptor{}

	adapter := &mockAdapter{id: "test-source", name: "Test Source"}
	registry.Register(adapter)

	if err := registry.UpdateSource("test-source", true, map[string]string{"api_key": "secret123"}, cryptor); err != nil {
		t.Fatal(err)
	}

	if err := registry.UpdateSource("test-source", true, map[string]string{"api_key": "newsecret456"}, cryptor); err != nil {
		t.Fatal(err)
	}

	if adapter.creds["api_key"] != "newsecret456" {
		t.Fatalf("got %q, want newsecret456", adapter.creds["api_key"])
	}
}

// credentialedAdapter requires a username and accepts only "valid".
type credentialedAdapter struct {
	mockAdapter
}

func (c *credentialedAdapter) CredentialFields() []CredentialField {
	return []CredentialField{{Key: "username", Required: true}}
}

func (c *credentialedAdapter) ValidateCredentials(context.Context) error {
	if c.creds["username"] != "valid" {
		return context.DeadlineExceeded
	}
	return nil
}

func (c *credentialedAdapter) NewInstance() Adapter {
	return &credentialedAdapter{mockAdapter: mockAdapter{id: c.id, name: c.name}}
}

// TestTestCredentialsLeavesLiveAdapterAlone verifies that testing wrong
// credentials does not replace the credentials the adapter is running with.
func TestTestCredentialsLeavesLiveAdapterAlone(t *testing.T) {
	db := setupTestDB(t)
	registry := NewRegistry(db, &config.Config{})
	adapter := &credentialedAdapter{mockAdapter: mockAdapter{id: "src", name: "Source"}}
	registry.Register(adapter)
	if err := registry.UpdateSource("src", true, map[string]string{"username": "valid"}, &mockCryptor{}); err != nil {
		t.Fatal(err)
	}

	if err := registry.TestCredentials(context.Background(), "src", map[string]string{"username": "typo"}); err == nil {
		t.Fatal("wrong credentials must fail the test")
	}
	if adapter.creds["username"] != "valid" {
		t.Errorf("live adapter credentials = %v, want the stored ones", adapter.creds)
	}
	if err := registry.TestStoredCredentials(context.Background(), "src"); err != nil {
		t.Errorf("stored credentials should still validate: %v", err)
	}
}

// TestTestStoredCredentialsNeedsLoadedCredentials verifies that stored but
// undecrypted credentials are reported as not loaded rather than invalid.
func TestTestStoredCredentialsNeedsLoadedCredentials(t *testing.T) {
	db := setupTestDB(t)
	registry := NewRegistry(db, &config.Config{})
	registry.Register(&credentialedAdapter{mockAdapter: mockAdapter{id: "src", name: "Source"}})
	db.Create(&database.Source{ID: "src", Name: "Source", Enabled: true, CredentialsEnc: []byte("enc:{}")})

	if err := registry.TestStoredCredentials(context.Background(), "src"); !errors.Is(err, ErrCredentialsNotLoaded) {
		t.Errorf("error = %v, want ErrCredentialsNotLoaded", err)
	}
}
