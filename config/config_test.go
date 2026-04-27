package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	// Clear env vars to test defaults
	t.Setenv("BULK_LOADER_PASSPHRASE", "")
	t.Setenv("BULK_LOADER_DB_DRIVER", "")
	t.Setenv("BULK_LOADER_DB_DSN", "")
	t.Setenv("BULK_LOADER_PORT", "")
	t.Setenv("BULK_LOADER_MAX_CONCURRENT", "")
	t.Setenv("BULK_LOADER_DOWNLOAD_TIMEOUT", "")
	t.Setenv("BULK_LOADER_DEV_MODE", "")

	// Use temp directory
	tmpDir := t.TempDir()
	t.Setenv("BULK_LOADER_DATA_DIR", tmpDir)

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
	if cfg.InsecureCookie {
		t.Error("InsecureCookie should be false by default")
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies should be empty by default, got %d", len(cfg.TrustedProxies))
	}
}

func TestLoadFromEnv(t *testing.T) {
	tmpDir := t.TempDir()

	t.Setenv("BULK_LOADER_PASSPHRASE", "secret123")
	t.Setenv("BULK_LOADER_DB_DRIVER", "postgres")
	t.Setenv("BULK_LOADER_DB_DSN", "postgres://localhost/test")
	t.Setenv("BULK_LOADER_DATA_DIR", tmpDir)
	t.Setenv("BULK_LOADER_PORT", "9000")
	t.Setenv("BULK_LOADER_MAX_CONCURRENT", "5")
	t.Setenv("BULK_LOADER_DOWNLOAD_TIMEOUT", "7200")
	t.Setenv("BULK_LOADER_DEV_MODE", "true")
	t.Setenv("BULK_LOADER_VITE_PROXY", "http://localhost:5173")
	t.Setenv("BULK_LOADER_INSECURE_COOKIE", "true")

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
	if !cfg.InsecureCookie {
		t.Error("InsecureCookie should be true")
	}
}

func TestInvalidPortFallsBackToDefault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("BULK_LOADER_DATA_DIR", tmpDir)
	t.Setenv("BULK_LOADER_PORT", "not-a-number")

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

func TestLoadTrustedProxies(t *testing.T) {
	t.Setenv("BULK_LOADER_DATA_DIR", t.TempDir())
	t.Setenv("BULK_LOADER_TRUSTED_PROXIES", "127.0.0.1, 10.0.0.0/8 ,::1,2001:db8::/32")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(cfg.TrustedProxies), 4; got != want {
		t.Fatalf("TrustedProxies len = %d, want %d", got, want)
	}

	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:9999", true},
		{"10.5.5.5:80", true},
		{"[::1]:80", true},
		{"[2001:db8::1]:443", true},
		{"8.8.8.8:80", false},
		{"172.16.0.1:80", false},
		{"notanip:80", false},
		{"", false},
	}
	for _, c := range cases {
		if got := cfg.IsTrustedProxy(c.addr); got != c.want {
			t.Errorf("IsTrustedProxy(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

func TestLoadTrustedProxiesInvalid(t *testing.T) {
	t.Setenv("BULK_LOADER_DATA_DIR", t.TempDir())
	t.Setenv("BULK_LOADER_TRUSTED_PROXIES", "not-an-ip")

	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid trusted proxy entry, got nil")
	}
}

func TestLoadCreatesDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "nested", "data")

	t.Setenv("BULK_LOADER_DATA_DIR", dataDir)

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
