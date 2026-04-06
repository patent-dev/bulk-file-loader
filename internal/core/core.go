package core

import (
	"log/slog"
	"os"

	"github.com/patent-dev/bulk-file-loader/config"
	"github.com/patent-dev/bulk-file-loader/internal/auth"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/downloader"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"github.com/patent-dev/bulk-file-loader/internal/scheduler"
	"github.com/patent-dev/bulk-file-loader/internal/service"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
	"github.com/patent-dev/bulk-file-loader/internal/sources/dpma"
	"github.com/patent-dev/bulk-file-loader/internal/sources/epo"
	"github.com/patent-dev/bulk-file-loader/internal/sources/uspto"
)

type Core struct {
	Config     *config.Config
	DB         *database.DB
	Auth       *auth.Service
	Registry   *sources.Registry
	Downloader *downloader.Downloader
	Hooks      *hooks.Manager
	Scheduler  *scheduler.Scheduler
	Service    *service.Service
}

type Options struct {
	WithScheduler bool
	NoWebhooks    bool
	SilentLogger  bool
	Version       string
}

func New(opts Options) (*Core, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	if !opts.WithScheduler {
		// CLI mode: suppress log output to avoid cluttering command output
		opts.SilentLogger = true
	}
	if opts.SilentLogger {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	} else {
		logLevel := slog.LevelInfo
		if cfg.DevMode {
			logLevel = slog.LevelDebug
		}
		logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
		slog.SetDefault(logger)
	}

	db, err := database.New(cfg)
	if err != nil {
		return nil, err
	}

	authService := auth.New(db, cfg)
	hooksManager := hooks.New(db)
	if opts.NoWebhooks {
		hooksManager.SetDisabled(true)
	}

	sourceRegistry := sources.NewRegistry(db, cfg)
	sourceRegistry.RegisterBuiltinAdapters(epo.New(), uspto.New(), dpma.New())

	if err := sourceRegistry.LoadCredentialsWithDecryptor(authService); err != nil {
		slog.Debug("Credentials not loaded at startup", "error", err)
	}

	authService.OnCredentialsReady(func() {
		if err := sourceRegistry.LoadCredentialsWithDecryptor(authService); err != nil {
			slog.Error("Failed to load source credentials", "error", err)
		}
	})

	dl := downloader.New(db, sourceRegistry, hooksManager, cfg)

	c := &Core{
		Config:     cfg,
		DB:         db,
		Auth:       authService,
		Registry:   sourceRegistry,
		Downloader: dl,
		Hooks:      hooksManager,
	}

	if opts.WithScheduler {
		c.Scheduler = scheduler.New(db, sourceRegistry, dl, hooksManager)
	}

	c.Service = service.New(db, authService, sourceRegistry, dl, c.Scheduler, hooksManager, opts.Version)

	return c, nil
}

func (c *Core) Close() {
	if c.Scheduler != nil {
		c.Scheduler.Stop()
	}
	if c.DB != nil {
		if sqlDB, err := c.DB.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}
