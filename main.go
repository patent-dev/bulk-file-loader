package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/api/handlers"
	"github.com/patent-dev/bulk-file-loader/config"
	"github.com/patent-dev/bulk-file-loader/internal/auth"
	"github.com/patent-dev/bulk-file-loader/internal/database"
	"github.com/patent-dev/bulk-file-loader/internal/downloader"
	"github.com/patent-dev/bulk-file-loader/internal/hooks"
	"github.com/patent-dev/bulk-file-loader/internal/scheduler"
	"github.com/patent-dev/bulk-file-loader/internal/sources"
	"github.com/patent-dev/bulk-file-loader/internal/sources/epo"
	"github.com/patent-dev/bulk-file-loader/internal/sources/uspto"
)

//go:embed web/ui/dist/*
var webAssets embed.FS

func main() {
	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "Show version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println("bulk-file-loader v0.1.0")
		os.Exit(0)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	logLevel := slog.LevelInfo
	if cfg.DevMode {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	slog.Info("Starting bulk-file-loader", "port", cfg.Port, "dataDir", cfg.DataDir)

	db, err := database.New(cfg)
	if err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}

	authService := auth.New(db, cfg)
	hooksManager := hooks.New(db)

	sourceRegistry := sources.NewRegistry(db, cfg)
	sourceRegistry.RegisterBuiltinAdapters(epo.New(), uspto.New())

	if err := sourceRegistry.LoadCredentialsWithDecryptor(authService); err != nil {
		slog.Debug("Credentials not loaded at startup", "error", err)
	}

	authService.OnCredentialsReady(func() {
		if err := sourceRegistry.LoadCredentialsWithDecryptor(authService); err != nil {
			slog.Error("Failed to load source credentials", "error", err)
		}
	})

	dl := downloader.New(db, sourceRegistry, hooksManager, cfg)
	sched := scheduler.New(db, sourceRegistry, dl, hooksManager)

	mux := http.NewServeMux()
	apiHandler := handlers.New(db, authService, sourceRegistry, dl, sched, hooksManager)
	_ = generated.HandlerWithOptions(apiHandler, generated.StdHTTPServerOptions{
		BaseURL:     "/api",
		BaseRouter:  mux,
		Middlewares: []generated.MiddlewareFunc{authService.Middleware},
	})

	if cfg.DevMode && cfg.ViteProxy != "" {
		slog.Info("Dev mode: proxying to Vite", "url", cfg.ViteProxy)
		viteURL, err := url.Parse(cfg.ViteProxy)
		if err != nil {
			slog.Error("Failed to parse Vite proxy URL", "error", err)
			os.Exit(1)
		}
		mux.Handle("/", httputil.NewSingleHostReverseProxy(viteURL))
	} else {
		webFS, err := fs.Sub(webAssets, "web/ui/dist")
		if err != nil {
			slog.Error("Failed to get web assets", "error", err)
			os.Exit(1)
		}
		fileServer := http.FileServer(http.FS(webFS))
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if path == "/" {
				path = "/index.html"
			}
			if _, err := fs.Stat(webFS, path[1:]); err != nil {
				r.URL.Path = "/"
			}
			fileServer.ServeHTTP(w, r)
		}))
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("Server listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Shutdown error", "error", err)
	}

	sched.Stop()
}
