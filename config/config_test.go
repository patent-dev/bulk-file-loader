package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	// Clear env vars to test defaults
	os.Unsetenv("BULK_LOADER_PASSPHRASE")
	os.Unsetenv("BULK_LOADER_DB_DRIVER")
	os.Unsetenv("BULK_LOADER_DB_DSN")
	os.Unsetenv("BULK_LOADER_PORT")
	os.Unsetenv("BULK_LOADER_MAX_CONCURRENT")
	os.Unsetenv("BULK_LOADER_DOWNLOAD_TIMEOUT")
	os.Unsetenv("BULK_LOADER_DEV_MODE")

	// Use temp directory
	tmpDir := t.TempDir()
	os.Setenv("BULK_LOADER_DATA_DIR", tmpDir)
	defer os.Unsetenv("BULK_LOADER_DATA_DIR")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.DBDriver != "sqlite" {
		t.Errorf("DBDriver = %q, want sqlite", cfg.DBDriver)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.MaxConcurrent != 3 {
		t.Errorf("MaxConcurrent = %d, want 3", cfg.MaxConcurrent)
	}
	if cfg.DownloadTimeout != 3600 {
		t.Errorf("DownloadTimeout = %d, want 3600", cfg.DownloadTimeout)
	}
	if cfg.DevMode {
		t.Error("DevMode should be false by default")
	}
}

func TestLoadFromEnv(t *testing.T) {
	tmpDir := t.TempDir()

	os.Setenv("BULK_LOADER_PASSPHRASE", "secret123")
	os.Setenv("BULK_LOADER_DB_DRIVER", "postgres")
	os.Setenv("BULK_LOADER_DB_DSN", "postgres://localhost/test")
	os.Setenv("BULK_LOADER_DATA_DIR", tmpDir)
	os.Setenv("BULK_LOADER_PORT", "9000")
	os.Setenv("BULK_LOADER_MAX_CONCURRENT", "5")
	os.Setenv("BULK_LOADER_DOWNLOAD_TIMEOUT", "7200")
	os.Setenv("BULK_LOADER_DEV_MODE", "true")
	os.Setenv("BULK_LOADER_VITE_PROXY", "http://localhost:5173")

	defer func() {
		os.Unsetenv("BULK_LOADER_PASSPHRASE")
		os.Unsetenv("BULK_LOADER_DB_DRIVER")
		os.Unsetenv("BULK_LOADER_DB_DSN")
		os.Unsetenv("BULK_LOADER_DATA_DIR")
		os.Unsetenv("BULK_LOADER_PORT")
		os.Unsetenv("BULK_LOADER_MAX_CONCURRENT")
		os.Unsetenv("BULK_LOADER_DOWNLOAD_TIMEOUT")
		os.Unsetenv("BULK_LOADER_DEV_MODE")
		os.Unsetenv("BULK_LOADER_VITE_PROXY")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Passphrase != "secret123" {
		t.Errorf("Passphrase = %q, want secret123", cfg.Passphrase)
	}
	if cfg.DBDriver != "postgres" {
		t.Errorf("DBDriver = %q, want postgres", cfg.DBDriver)
	}
	if cfg.DBDSN != "postgres://localhost/test" {
		t.Errorf("DBDSN = %q, want postgres://localhost/test", cfg.DBDSN)
	}
	if cfg.Port != 9000 {
		t.Errorf("Port = %d, want 9000", cfg.Port)
	}
	if cfg.MaxConcurrent != 5 {
		t.Errorf("MaxConcurrent = %d, want 5", cfg.MaxConcurrent)
	}
	if cfg.DownloadTimeout != 7200 {
		t.Errorf("DownloadTimeout = %d, want 7200", cfg.DownloadTimeout)
	}
	if !cfg.DevMode {
		t.Error("DevMode should be true")
	}
	if cfg.ViteProxy != "http://localhost:5173" {
		t.Errorf("ViteProxy = %q, want http://localhost:5173", cfg.ViteProxy)
	}
}

func TestInvalidPortFallsBackToDefault(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("BULK_LOADER_DATA_DIR", tmpDir)
	os.Setenv("BULK_LOADER_PORT", "not-a-number")
	defer os.Unsetenv("BULK_LOADER_DATA_DIR")
	defer os.Unsetenv("BULK_LOADER_PORT")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080 (default)", cfg.Port)
	}
}

func TestDatabasePath(t *testing.T) {
	cfg := &Config{DataDir: "/var/data"}
	expected := filepath.Join("/var/data", "bulk-loader.db")
	if cfg.DatabasePath() != expected {
		t.Errorf("DatabasePath() = %q, want %q", cfg.DatabasePath(), expected)
	}
}

func TestDownloadsPath(t *testing.T) {
	cfg := &Config{DataDir: "/var/data"}
	expected := filepath.Join("/var/data", "downloads")
	if cfg.DownloadsPath() != expected {
		t.Errorf("DownloadsPath() = %q, want %q", cfg.DownloadsPath(), expected)
	}
}

func TestLoadCreatesDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "nested", "data")

	os.Setenv("BULK_LOADER_DATA_DIR", dataDir)
	defer os.Unsetenv("BULK_LOADER_DATA_DIR")

	_, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		t.Error("data directory was not created")
	}

	downloadsDir := filepath.Join(dataDir, "downloads")
	if _, err := os.Stat(downloadsDir); os.IsNotExist(err) {
		t.Error("downloads directory was not created")
	}
}
