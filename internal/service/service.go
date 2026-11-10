package service

import (
	"context"
	"errors"
	"sync"
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

	// ctx is cancelled on shutdown; background work (scheduled syncs, their
	// downloads) runs under it and is tracked by background.
	ctx          context.Context
	background   sync.WaitGroup
	backgroundMu sync.Mutex
	stopping     bool
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
		ctx:        context.Background(),
	}
}

// SetContext sets the context background work runs under; cancelling it
// stops scheduled syncs and their downloads.
func (s *Service) SetContext(ctx context.Context) { s.ctx = ctx }

// goBackground runs fn in a goroutine that Wait waits for. Once Wait has
// been called, no new background work starts.
func (s *Service) goBackground(fn func(ctx context.Context)) {
	s.backgroundMu.Lock()
	defer s.backgroundMu.Unlock()
	if s.stopping {
		return
	}
	s.background.Add(1)
	go func() {
		defer s.background.Done()
		fn(s.ctx)
	}()
}

// Wait blocks until background work has finished.
func (s *Service) Wait() {
	s.backgroundMu.Lock()
	s.stopping = true
	s.backgroundMu.Unlock()
	s.background.Wait()
}
