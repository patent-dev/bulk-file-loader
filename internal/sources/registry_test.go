package sources

import (
	"context"
	"io"
	"testing"

	"github.com/ncruces/go-sqlite3/gormlite"
	"github.com/patent-dev/bulk-file-loader/config"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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
	gormDB, err := gorm.Open(gormlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = gormDB.AutoMigrate(&database.Source{})
	return &database.DB{DB: gormDB}
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
