package service

import (
	"errors"
	"time"

	"github.com/patent-dev/bulk-file-loader/internal/auth"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/downloader"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"github.com/patent-dev/bulk-file-loader/internal/scheduler"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
)

var (
	ErrNotFound           = errors.New("not found")
	ErrConflict           = errors.New("conflict")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidSchedule    = errors.New("invalid schedule")
)

type Service struct {
	DB         *database.DB
	Auth       *auth.Service
	Registry   *sources.Registry
	Downloader *downloader.Downloader
	Scheduler  *scheduler.Scheduler // can be nil (CLI mode)
	Hooks      *hooks.Manager
	Version    string
	startTime  time.Time
}

func New(
	db *database.DB,
	authService *auth.Service,
	registry *sources.Registry,
	dl *downloader.Downloader,
	sched *scheduler.Scheduler,
	hooksManager *hooks.Manager,
	version string,
) *Service {
	return &Service{
		DB:         db,
		Auth:       authService,
		Registry:   registry,
		Downloader: dl,
		Scheduler:  sched,
		Hooks:      hooksManager,
		Version:    version,
		startTime:  time.Now(),
	}
}
