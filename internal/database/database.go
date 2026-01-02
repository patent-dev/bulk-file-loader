package database

import (
	"fmt"
	"log/slog"

	"github.com/patent-dev/bulk-file-loader/config"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type DB struct {
	*gorm.DB
}

func New(cfg *config.Config) (*DB, error) {
	var dialector gorm.Dialector

	switch cfg.DBDriver {
	case "sqlite":
		dialector = sqlite.Open(cfg.DatabasePath())
	case "postgres":
		if cfg.DBDSN == "" {
			return nil, fmt.Errorf("BULK_LOADER_DB_DSN is required for postgres")
		}
		dialector = postgres.Open(cfg.DBDSN)
	case "mysql":
		if cfg.DBDSN == "" {
			return nil, fmt.Errorf("BULK_LOADER_DB_DSN is required for mysql")
		}
		dialector = mysql.Open(cfg.DBDSN)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.DBDriver)
	}

	gormLogger := logger.Default.LogMode(logger.Silent)
	if cfg.DevMode {
		gormLogger = logger.Default.LogMode(logger.Info)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := runMigrations(db); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	result := db.Model(&DownloadEntry{}).
		Where("status = ?", DownloadStatusDownloading).
		Updates(map[string]interface{}{
			"status":        DownloadStatusFailed,
			"error_message": "interrupted by restart",
		})
	if result.RowsAffected > 0 {
		slog.Info("Cleaned up stale downloads", "count", result.RowsAffected)
	}

	slog.Info("Database connected", "driver", cfg.DBDriver)

	return &DB{DB: db}, nil
}

func runMigrations(db *gorm.DB) error {
	return db.AutoMigrate(
		&Source{},
		&Product{},
		&Delivery{},
		&File{},
		&DownloadEntry{},
		&Webhook{},
		&Setting{},
	)
}

func (db *DB) GetSetting(key string) (string, error) {
	var setting Setting
	if err := db.Where("key = ?", key).First(&setting).Error; err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (db *DB) SetSetting(key, value string) error {
	return db.Save(&Setting{Key: key, Value: value}).Error
}

func (db *DB) HasSetting(key string) bool {
	var count int64
	db.Model(&Setting{}).Where("key = ?", key).Count(&count)
	return count > 0
}
