package service

import (
	"testing"

	"github.com/patent-dev/bulk-file-loader/config"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/database/dbtest"
	"github.com/patent-dev/bulk-file-loader/internal/downloader"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

func setupTestService(t *testing.T) *Service {
	t.Helper()
	db, err := database.New(dbtest.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{
		DataDir:         t.TempDir(),
		MaxConcurrent:   2,
		DownloadTimeout: 60,
	}
	registry := sources.NewRegistry(db, cfg)
	hooksManager := hooks.New(db)
	dl := downloader.New(db, registry, hooksManager, cfg)
	return New(db, nil, registry, dl, nil, hooksManager, "test")
}
