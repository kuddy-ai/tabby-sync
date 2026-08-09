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
	"github.com/kuddy-ai/tabby-sync/internal/ratelimit"
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
	// 1 MiB is far above what tabby-sync's own clients send. Request bodies
	// have a separate 2 MiB cap enforced by middleware.MaxBodyBytes.
	defaultMaxHeaderBytes = 1 << 20
)

// New builds an *http.Server bound to cfg.Addr with sane timeouts and a
// mux that serves GET /healthz plus, when apiHandler is non-nil, every
// route registered by [internal/api.New] under the /api/1/ prefix. The
// provided logger is not retained on the server; it is only used to
// attach an ErrorLog wrapper so http.Server's own diagnostics flow
// through slog.
//
// authMW is the application-level authentication middleware. Passing
// nil is equivalent to passing [auth.None]; this preserves the
// contract that New always returns a runnable server and lets existing
// tests keep constructing one without an authenticator.
//
// apiHandler is mounted under the literal /api/1/ prefix when non-nil
// so the route-aware auth middleware automatically gates every
// /api/1/* request. Tests that do not exercise the API may pass nil;
// /healthz keeps working in that mode and any /api/1/* request falls
// through to the mux's 404 (still after the auth middleware).
//
// limiter, when non-nil, adds per-key rate limiting after authentication.
func New(cfg *config.Config, logger *slog.Logger, authMW auth.Middleware, apiHandler http.Handler, limiter *ratelimit.Limiter) *http.Server {
	if logger == nil {
		logger = slog.Default()
	}
	if authMW == nil {
		authMW = auth.None()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	if apiHandler != nil {
		mux.Handle("/api/1/", apiHandler)
	}

	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           buildHandler(mux, logger, authMW, limiter),
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
//  2. CORS runs next so Access-Control-Allow-* headers land on every
//     response too, and so an OPTIONS preflight from the Tabby desktop
//     client's fetch() calls is answered with 204 before it ever
//     reaches the auth middleware. A preflight never carries the real
//     Authorization header, so gating it behind auth makes every
//     preflight fail with 401 and no CORS headers - which the browser
//     reports to fetch() as an opaque network error, not a 401.
//  3. TrustedProxy substitutes the real client address in for
//     r.RemoteAddr, taken from X-Forwarded-For, but only when the
//     immediate TCP peer is a known-private range (i.e. the Caddy
//     sidecar on the same Docker network). It runs before AccessLog
//     and ratelimit, both of which read r.RemoteAddr: otherwise every
//     access log line and every unauthenticated rate-limit bucket is
//     keyed by the reverse proxy's container IP instead of the actual
//     client.
//  4. RequestID assigns or accepts an X-Request-Id before any logger
//     reads from the context, so every subsequent log line is
//     correlatable.
//  5. AccessLog runs OUTSIDE Recover so it observes the final status
//     code (including a 500 written by Recover when an inner handler
//     panics) and the response size, and can attach the request id
//     picked up from the context. AccessLog's post-handler LogAttrs
//     call would otherwise be skipped on panic.
//  6. Recover wraps the rest of the chain so a panic in the body-limit,
//     auth, or handler layer becomes a generic 500 rather than tearing
//     down the goroutine.
//  7. MaxBodyBytes pre-reads the request body so handlers (including
//     ones that never touch r.Body) cannot be flooded with arbitrarily
//     large payloads.
//  8. routeAwareAuth(authMW) runs the supplied authenticator on every
//     route except a GET or HEAD request to the literal /healthz path,
//     which is exempt so liveness probes can reach the server without
//     an Authorization header. The bypass is deliberate: /healthz
//     exposes only the literal body "ok\n" with no version, build,
//     configuration, DB or key state mixed in. Non-GET/HEAD methods to
//     /healthz are still gated, so the auth-before-mux contract holds
//     for any other verb the mux does not register.
func buildHandler(mux http.Handler, logger *slog.Logger, authMW auth.Middleware, limiter *ratelimit.Limiter) http.Handler {
	trustedProxy, err := middleware.TrustedProxy(nil)
	if err != nil {
		// nil selects middleware's own hardcoded, known-valid default
		// CIDR list; a parse failure here is a build-time invariant
		// violation, not a runtime condition callers can recover from.
		panic("server: default trusted-proxy CIDRs failed to parse: " + err.Error())
	}

	mws := []middleware.Middleware{
		middleware.SecurityHeaders(),
		middleware.CORS(),
		trustedProxy,
		middleware.RequestID,
		middleware.AccessLog(logger),
		middleware.Recover(logger),
		middleware.MaxBodyBytes(middleware.DefaultMaxBodyBytes),
		routeAwareAuth(authMW),
	}
	if limiter != nil {
		mws = append(mws, ratelimit.Middleware(limiter, logger))
	}
	return middleware.Chain(mux, mws...)
}

// routeAwareAuth wraps an [auth.Middleware] so it applies to every route
// except a GET/HEAD request to the literal /healthz path. Other methods
// (POST /healthz, OPTIONS /healthz, etc.) are still gated by authMW; the
// mux returns 405 only after auth has run, so a probing client cannot
// learn route shapes by bouncing requests off the bypass. Comparing
// r.URL.Path directly (no normalisation) is intentional: the underlying
// mux would otherwise normalise trailing slashes and we do NOT want
// "/healthz/" or "/healthz/something" to bypass authentication.
func routeAwareAuth(authMW auth.Middleware) auth.Middleware {
	if authMW == nil {
		authMW = auth.None()
	}
	return func(next http.Handler) http.Handler {
		guarded := authMW(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
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
