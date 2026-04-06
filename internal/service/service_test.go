package service

import (
	"testing"

	"github.com/patent-dev/bulk-file-loader/config"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/downloader"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestService(t *testing.T) *Service {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = gormDB.AutoMigrate(
		&database.Source{},
		&database.Product{},
		&database.Delivery{},
		&database.File{},
		&database.DownloadEntry{},
		&database.Webhook{},
		&database.Setting{},
	)
	if err != nil {
		t.Fatal(err)
	}
	db := &database.DB{DB: gormDB}
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
