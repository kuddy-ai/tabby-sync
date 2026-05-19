// Package server builds and runs the tabby-sync HTTP server.
//
// The package exposes a minimal surface area: New constructs a configured
// *http.Server and Run drives its lifecycle on a caller-supplied listener.
// Splitting Listen from Serve lets tests bind to 127.0.0.1:0 and discover
// the chosen port synchronously before the server goroutine starts, which
// avoids racing on a hard-coded port.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/kuddy-ai/tabby-sync/internal/config"
)

// Timeout defaults. ReadHeaderTimeout is set explicitly to satisfy gosec
// G114 (Slowloris); the others are sized for a small admin/sync API.
const (
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
)

// New builds an *http.Server bound to cfg.Addr with sane timeouts and a
// minimal mux that serves only GET /healthz. The provided logger is not
// retained on the server; it is only used to attach an ErrorLog wrapper so
// http.Server's own diagnostics flow through slog.
func New(cfg *config.Config, logger *slog.Logger) *http.Server {
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}

// Run serves srv on ln until ctx is canceled, then gracefully shuts the
// server down with a fresh 10-second timeout. Run consumes the listener:
// it will be closed by the time Run returns.
//
// Returns nil when shutdown completes after Serve returned http.ErrServerClosed
// (the expected graceful-stop path). Any other error is returned to the caller.
func Run(ctx context.Context, srv *http.Server, ln net.Listener, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining HTTP server")
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}

	// Drain Serve's terminal error after Shutdown has completed.
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	logger.Info("HTTP server shutdown complete")
	return nil
}
