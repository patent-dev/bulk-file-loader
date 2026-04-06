package cli

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/patent-dev/bulk-file-loader/api/generated"
	"github.com/patent-dev/bulk-file-loader/api/handlers"
	"github.com/patent-dev/bulk-file-loader/internal/core"
	"github.com/spf13/cobra"
)

var (
	servePort int
	serveDev  bool
)

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 0, "server port (env: BULK_LOADER_PORT, default: 8080)")
	serveCmd.Flags().BoolVar(&serveDev, "dev", false, "enable dev mode (env: BULK_LOADER_DEV_MODE)")
	rootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the HTTP server with web UI and API",
	RunE:  runServe,
}

func runServe(cmd *cobra.Command, args []string) error {
	ApplyFlagsToEnv()

	if servePort > 0 {
		setEnvIfFlag("BULK_LOADER_PORT", strconv.Itoa(servePort))
	}
	if serveDev {
		_ = os.Setenv("BULK_LOADER_DEV_MODE", "true")
	}

	c, err := core.New(core.Options{
		WithScheduler: true,
		NoWebhooks:    flagNoWebhooks,
		Version:       Version,
	})
	if err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}
	defer c.Close()

	slog.Info("Starting bulk-file-loader", "port", c.Config.Port, "dataDir", c.Config.DataDir)

	mux := http.NewServeMux()
	apiHandler := handlers.New(c.Service)
	_ = generated.HandlerWithOptions(apiHandler, generated.StdHTTPServerOptions{
		BaseURL:     "/api",
		BaseRouter:  mux,
		Middlewares: []generated.MiddlewareFunc{apiHandler.AuthService().Middleware},
	})

	if c.Config.DevMode && c.Config.ViteProxy != "" {
		slog.Info("Dev mode: proxying to Vite", "url", c.Config.ViteProxy)
		viteURL, err := url.Parse(c.Config.ViteProxy)
		if err != nil {
			return fmt.Errorf("parse Vite proxy URL: %w", err)
		}
		mux.Handle("/", httputil.NewSingleHostReverseProxy(viteURL))
	} else if WebAssets != nil {
		webFS, err := fs.Sub(WebAssets, "web/ui/dist")
		if err != nil {
			return fmt.Errorf("get web assets: %w", err)
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
	} else {
		slog.Warn("No embedded web assets available")
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" || r.URL.Path == "/index.html" {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = fmt.Fprintln(w, "bulk-file-loader API server (no web UI embedded)")
			} else {
				http.NotFound(w, r)
			}
		})
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", c.Config.Port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // Disabled to support SSE streams; handlers manage their own timeouts via context
		IdleTimeout:  60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("Server listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("server error: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	slog.Info("Shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Shutdown error", "error", err)
	}

	return nil
}
