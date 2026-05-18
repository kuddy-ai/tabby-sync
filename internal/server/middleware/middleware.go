// Package middleware contains the HTTP middleware stack used by the
// tabby-sync server. Every middleware is implemented with the Go standard
// library only (net/http, context, log/slog, crypto/rand, encoding/hex,
// regexp, runtime/debug, time) so that the binary keeps a small, audited
// dependency surface.
//
// The middlewares in this package are deliberately framework-agnostic:
// each one is a plain func(http.Handler) http.Handler value, and they are
// composed by [Chain] in registration order so the first registered
// middleware is the outermost wrapper of the resulting handler.
package middleware

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"
)

// Middleware is the contract every HTTP middleware in this package
// satisfies. It wraps a downstream [http.Handler] and returns a new handler
// that runs additional logic before and/or after the wrapped one.
type Middleware = func(http.Handler) http.Handler

// Chain composes the supplied middlewares around h. The first middleware
// in mws becomes the OUTERMOST wrapper, which means it runs first on the
// way in and last on the way out. Callers should therefore pass the most
// generic middleware (for example security headers) first and the most
// specific one (for example body-size limiting) last.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	// Iterate in reverse so the registration order matches the call order:
	// the first registered middleware ends up wrapping all the others.
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] == nil {
			continue
		}
		h = mws[i](h)
	}
	return h
}

// HeaderRequestID is the canonical name of the request-id header used both
// inbound (when a client or a reverse proxy supplies an id) and outbound
// (set by [RequestID] before the wrapped handler runs).
const HeaderRequestID = "X-Request-Id"

// requestIDPattern is intentionally strict: only printable ASCII letters,
// digits, dot, underscore and dash, capped at 64 bytes. Anything else
// (whitespace, newlines, control characters, oversized payloads) is
// rejected so a malicious client cannot poison structured logs by
// injecting newline or quote characters into the inbound header.
var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// requestIDKey is unexported so only this package can stash or read the
// request id on a context, preventing accidental key collisions.
type requestIDKey struct{}

// RequestID returns a middleware that ensures every request carries a
// request-id. If the inbound request supplies an [HeaderRequestID] header
// matching the strict allowlist, that value is reused; otherwise a fresh
// 16-byte random value is generated with crypto/rand and hex encoded.
//
// The id is stored on the request context (retrievable with [FromContext])
// and echoed on the response via the same header before the wrapped
// handler runs, so downstream middlewares can include it in their logs.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderRequestID)
		if !requestIDPattern.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set(HeaderRequestID, id)
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// newRequestID returns a 32-character hex string sourced from crypto/rand.
// The function never panics; if the OS RNG fails (extremely unusual) we
// fall back to a timestamp-based id so the request still receives a
// non-empty identifier.
func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "ts-" + time.Now().UTC().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(buf[:])
}

// FromContext returns the request id stored on ctx by [RequestID] and
// reports whether one was present.
func FromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(requestIDKey{}).(string)
	return v, ok && v != ""
}

// MustFromContext is a convenience wrapper around [FromContext] that
// returns an empty string when no request id is present. It is intended
// for log lines where an empty id is preferable to a panic.
func MustFromContext(ctx context.Context) string {
	v, _ := FromContext(ctx)
	return v
}

// statusRecorder wraps an [http.ResponseWriter] so middlewares can observe
// the eventually-written status code, the number of body bytes written,
// and whether the header has already been flushed.
//
// The recorder forwards [http.Flusher] when the underlying writer
// implements it so streaming handlers (server-sent events, chunked
// responses) can still flush through the chain. The other optional
// interfaces ([http.Hijacker], [http.CloseNotifier], [http.Pusher]) are
// not forwarded today because no current handler relies on them.
//
// TODO(#5-followup): forward Hijacker/CloseNotifier/Pusher once a handler
// in this codebase actually requires them. See review v1, issue #7.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.status = code
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if !s.wroteHeader {
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	n, err := s.ResponseWriter.Write(p)
	s.bytes += int64(n)
	return n, err
}

// Flush implements [http.Flusher] when the wrapped ResponseWriter does.
// It is a no-op otherwise so that callers can rely on the assertion
// `_, ok := w.(http.Flusher)` succeeding for any writer that genuinely
// supports flushing through the middleware chain.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Recover returns a middleware that catches any panic raised by a
// downstream handler. It logs the panic value at slog.LevelError together
// with the request id, method, path, the type of the panic value, and a
// full goroutine stack, and returns a generic 500 response to the client.
// The panic value and stack are NEVER written to the response body.
//
// If the wrapped handler had already started writing a response before
// panicking we cannot safely emit a fresh status line, so we only log.
//
// The sentinel [http.ErrAbortHandler] is intentionally re-panicked: it
// is the documented contract for asking the server to abort without
// further logging, and re-panicking lets the standard library handle it.
//
// Logging contract: the recovered value is written to the ERROR log via
// slog.Any, so callers MUST NOT panic with values that contain secrets
// (tokens, master keys, raw configuration, request bodies, etc.). The
// type of the panic value is always logged separately as panic_type so
// type information is structured even when the value itself is opaque.
func Recover(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w}
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				if v == http.ErrAbortHandler { //nolint:errorlint // sentinel comparison is correct here
					// http.ErrAbortHandler is a sentinel that explicitly
					// asks the server to abort without further logging.
					panic(v)
				}
				logger.LogAttrs(r.Context(), slog.LevelError,
					"panic recovered",
					slog.String("request_id", MustFromContext(r.Context())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("panic_type", fmt.Sprintf("%T", v)),
					slog.Any("panic", v),
					slog.String("stack", string(debug.Stack())),
				)
				if rec.wroteHeader {
					return
				}
				rec.Header().Set("Content-Type", "text/plain; charset=utf-8")
				rec.WriteHeader(http.StatusInternalServerError)
				_, _ = rec.Write([]byte("internal server error\n"))
			}()
			next.ServeHTTP(rec, r)
		})
	}
}

// AccessLog returns a middleware that emits one structured INFO log line
// per request after the downstream handler returns. The line carries:
//   - method
//   - path (URL.Path only, never the raw query string)
//   - status (HTTP status code)
//   - duration_ms (request latency in milliseconds)
//   - bytes (response body size in bytes)
//   - remote_ip (best-effort, from RemoteAddr)
//   - request_id (if [RequestID] ran earlier in the chain)
//   - user_agent
//
// AccessLog deliberately does NOT log the Authorization or Cookie headers,
// nor any portion of the request body, in line with docs/LOGGING_POLICY.md.
func AccessLog(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logger.LogAttrs(r.Context(), slog.LevelInfo,
				"http access",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.Int64("bytes", rec.bytes),
				slog.String("remote_ip", remoteIP(r.RemoteAddr)),
				slog.String("request_id", MustFromContext(r.Context())),
				slog.String("user_agent", r.UserAgent()),
			)
		})
	}
}

// remoteIP extracts the IP portion of a Go-style "host:port" RemoteAddr.
// It falls back to the raw value if the input does not parse so we still
// log something useful for unit tests or unusual transports.
func remoteIP(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// DefaultMaxBodyBytes is the body-size cap applied by [MaxBodyBytes] when
// a caller does not override it. 1 MiB is large enough for tabby-sync's
// JSON-style admin and sync payloads while still bounding memory cost.
const DefaultMaxBodyBytes int64 = 1 << 20

// MaxBodyBytes returns a middleware that enforces an upper bound on the
// size of the inbound request body. Unlike [http.MaxBytesReader], which
// only surfaces an error when the wrapped handler tries to read the body,
// this middleware PRE-READS up to limit+1 bytes from r.Body before calling
// the wrapped handler:
//
//   - if the read overflows (more than limit bytes were available) the
//     middleware writes HTTP 413 with a generic body and stops;
//   - otherwise the already-read bytes are wrapped back into r.Body via
//     bytes.NewReader, so downstream handlers see the full payload.
//
// Pre-reading guarantees the cap is enforced even for handlers that never
// touch the body (for example a GET handler that still receives a body
// from a misconfigured client). Empty and exactly-at-limit bodies pass
// through untouched.
//
// Memory cost: this middleware buffers up to limit+1 bytes per in-flight
// request before the handler runs, trading the streaming behaviour of
// [http.MaxBytesReader] for the "413 even when the handler ignores the
// body" guarantee. With the [DefaultMaxBodyBytes] cap of 1 MiB this is
// bounded; raising the limit also raises peak memory per concurrent
// request, so any future increase should be paired with a switch to a
// streaming enforcement strategy.
func MaxBodyBytes(limit int64) Middleware {
	if limit < 0 {
		limit = 0
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body == nil {
				next.ServeHTTP(w, r)
				return
			}
			// Read at most limit+1 bytes so we can distinguish "exactly at
			// the limit" (allowed) from "over the limit" (rejected).
			buf, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
			_ = r.Body.Close()
			if err != nil {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("invalid request body\n"))
				return
			}
			if int64(len(buf)) > limit {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_, _ = w.Write([]byte("request body too large\n"))
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(buf))
			r.ContentLength = int64(len(buf))
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders returns a middleware that sets a small, conservative
// set of HTTP security headers on every response before the downstream
// handler runs:
//
//   - X-Content-Type-Options: nosniff
//   - Referrer-Policy: no-referrer
//   - Cache-Control: no-store
//   - Content-Security-Policy: frame-ancestors 'none'
//   - X-Frame-Options: DENY
//
// HSTS is intentionally NOT set: tabby-sync runs as plain HTTP behind a
// reverse proxy in development, and HSTS belongs to the TLS-terminating
// proxy in production.
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cache-Control", "no-store")
			h.Set("Content-Security-Policy", "frame-ancestors 'none'")
			h.Set("X-Frame-Options", "DENY")
			next.ServeHTTP(w, r)
		})
	}
}
