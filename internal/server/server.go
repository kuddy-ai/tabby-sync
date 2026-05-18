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

	"github.com/kuddy-ai/tabby-sync/internal/auth"
	"github.com/kuddy-ai/tabby-sync/internal/config"
	"github.com/kuddy-ai/tabby-sync/internal/server/middleware"
)

// Timeout defaults. ReadHeaderTimeout is set explicitly to satisfy gosec
// G114 (Slowloris); the others are sized for a small admin/sync API.
const (
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout   = 10 * time.Second

	// defaultMaxHeaderBytes caps the size of inbound request headers.
	// 1 MiB is far above what tabby-sync's own clients send and matches
	// the request body cap enforced by middleware.MaxBodyBytes.
	defaultMaxHeaderBytes = 1 << 20
)

// New builds an *http.Server bound to cfg.Addr with sane timeouts and a
// minimal mux that serves only GET /healthz. The provided logger is not
// retained on the server; it is only used to attach an ErrorLog wrapper so
// http.Server's own diagnostics flow through slog.
//
// authMW is the application-level authentication middleware. Passing nil
// is equivalent to passing [auth.None]; this preserves the contract that
// New always returns a runnable server and lets existing tests keep
// constructing one without an authenticator.
func New(cfg *config.Config, logger *slog.Logger, authMW auth.Middleware) *http.Server {
	if logger == nil {
		logger = slog.Default()
	}
	if authMW == nil {
		authMW = auth.None()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)

	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           buildHandler(mux, logger, authMW),
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		MaxHeaderBytes:    defaultMaxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}

// buildHandler wraps the supplied mux with the standard middleware chain.
// It is unexported and exists so that internal/server tests can build the
// exact same chain around a test-only handler (for example, one that
// always panics) without having to reach into the production mux.
//
// Middleware chain, outermost first. Order matters:
//
//  1. SecurityHeaders runs first so its headers land on EVERY response
//     emitted by the server, including 413 from MaxBodyBytes, 500 from
//     Recover, and any 401 written by the auth middleware.
//  2. RequestID assigns or accepts an X-Request-Id before any logger
//     reads from the context, so every subsequent log line is
//     correlatable.
//  3. AccessLog runs OUTSIDE Recover so it observes the final status
//     code (including a 500 written by Recover when an inner handler
//     panics) and the response size, and can attach the request id
//     picked up from the context. AccessLog's post-handler LogAttrs
//     call would otherwise be skipped on panic.
//  4. Recover wraps the rest of the chain so a panic in the body-limit,
//     auth, or handler layer becomes a generic 500 rather than tearing
//     down the goroutine.
//  5. MaxBodyBytes pre-reads the request body so handlers (including
//     ones that never touch r.Body) cannot be flooded with arbitrarily
//     large payloads.
//  6. routeAwareAuth(authMW) runs the supplied authenticator on every
//     route except /healthz, which is exempt so liveness probes can
//     reach the server without an Authorization header. The bypass is
//     deliberate: /healthz exposes only the literal body "ok\n" with
//     no version, build, configuration, DB or key state mixed in.
func buildHandler(mux http.Handler, logger *slog.Logger, authMW auth.Middleware) http.Handler {
	return middleware.Chain(
		mux,
		middleware.SecurityHeaders(),
		middleware.RequestID,
		middleware.AccessLog(logger),
		middleware.Recover(logger),
		middleware.MaxBodyBytes(middleware.DefaultMaxBodyBytes),
		routeAwareAuth(authMW),
	)
}

// routeAwareAuth wraps an [auth.Middleware] so it applies to every route
// except /healthz. The /healthz path is the unauthenticated liveness
// probe; every other path is gated by authMW. Comparing r.URL.Path
// directly is intentional: the underlying mux would otherwise normalise
// trailing slashes and we do NOT want "/healthz/" or "/healthz/something"
// to bypass authentication.
func routeAwareAuth(authMW auth.Middleware) auth.Middleware {
	if authMW == nil {
		authMW = auth.None()
	}
	return func(next http.Handler) http.Handler {
		guarded := authMW(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			guarded.ServeHTTP(w, r)
		})
	}
}

// healthz is the unauthenticated liveness probe. The body is intentionally
// the literal string "ok\n" with no version, build, configuration, DB or
// key state mixed in; exposing any of that would leak deployment metadata
// to anyone who can reach the listener.
func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
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
