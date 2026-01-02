package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Passphrase      string
	DBDriver        string
	DBDSN           string
	DataDir         string
	Port            int
	MaxConcurrent   int
	DownloadTimeout int
	DevMode         bool
	ViteProxy       string
}

func Load() (*Config, error) {
	cfg := &Config{
		Passphrase:      os.Getenv("BULK_LOADER_PASSPHRASE"),
		DBDriver:        getEnvOrDefault("BULK_LOADER_DB_DRIVER", "sqlite"),
		DBDSN:           os.Getenv("BULK_LOADER_DB_DSN"),
		DataDir:         getEnvOrDefault("BULK_LOADER_DATA_DIR", "./data"),
		Port:            getEnvIntOrDefault("BULK_LOADER_PORT", 8080),
		MaxConcurrent:   getEnvIntOrDefault("BULK_LOADER_MAX_CONCURRENT", 3),
		DownloadTimeout: getEnvIntOrDefault("BULK_LOADER_DOWNLOAD_TIMEOUT", 3600),
		DevMode:         os.Getenv("BULK_LOADER_DEV_MODE") == "true",
		ViteProxy:       os.Getenv("BULK_LOADER_VITE_PROXY"),
	}

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "downloads"), 0755); err != nil {
		return nil, fmt.Errorf("create downloads directory: %w", err)
	}

	return cfg, nil
}

func (c *Config) DatabasePath() string {
	return filepath.Join(c.DataDir, "bulk-loader.db")
}

func (c *Config) DownloadsPath() string {
	return filepath.Join(c.DataDir, "downloads")
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvIntOrDefault(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultValue
}
