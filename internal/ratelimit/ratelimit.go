// Package ratelimit provides a simple in-memory token-bucket rate limiter
// keyed by an opaque string (typically the authenticated user ID or the
// remote IP for unauthenticated requests).
//
// The implementation is deliberately minimal: no persistence, no
// distributed coordination, no Redis dependency. Buckets are stored in a
// sync.Map and lazily garbage-collected by a background goroutine so
// memory does not grow unbounded under scanning traffic.
//
// Per docs/LOGGING_POLICY.md and AGENTS.md §7, the package never logs
// tokens, Authorization headers, request bodies, master keys, or config
// content. The only information surfaced in rate-limit error responses is
// the generic "rate limit exceeded" message and standard Retry-After /
// X-RateLimit-* headers.
package ratelimit

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kuddy-ai/tabby-sync/internal/auth"
)

// Limiter is an in-memory token-bucket rate limiter. Each key (user ID
// or remote IP) maintains an independent bucket of capacity [rate]
// tokens that refills at [rate] tokens per [window]. A request that
// finds the bucket empty is rejected with HTTP 429.
type Limiter struct {
	rate   int           // max tokens (burst capacity)
	window time.Duration // refill period for [rate] tokens
	mu     sync.Mutex
	// buckets maps key -> *bucket. We use a plain map + mutex instead of
	// sync.Map because the GC sweep needs to iterate all entries and
	// delete stale ones, which sync.Map does not support efficiently.
	buckets map[string]*bucket
	stopped atomic.Bool
}

type bucket struct {
	tokens    float64
	lastCheck time.Time
}

// New creates a Limiter that allows [rate] requests per [window] per key.
// A background goroutine runs every [window] to evict idle buckets.
func New(rate int, window time.Duration) *Limiter {
	if rate < 1 {
		rate = 1
	}
	if window < time.Second {
		window = time.Second
	}
	l := &Limiter{
		rate:    rate,
		window:  window,
		buckets: make(map[string]*bucket),
	}
	go l.gc()
	return l
}

// Allow checks whether key may proceed. Returns true and decrements the
// bucket if allowed; returns false if the bucket is exhausted.
func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.rate), lastCheck: now}
		l.buckets[key] = b
	}
	// Refill tokens based on elapsed time.
	elapsed := now.Sub(b.lastCheck)
	b.tokens += elapsed.Seconds() * (float64(l.rate) / l.window.Seconds())
	if b.tokens > float64(l.rate) {
		b.tokens = float64(l.rate)
	}
	b.lastCheck = now

	if b.tokens < 1 {
		l.mu.Unlock()
		return false
	}
	b.tokens--
	l.mu.Unlock()
	return true
}

// gc evicts buckets that have been idle for at least 2*window.
func (l *Limiter) gc() {
	ticker := time.NewTicker(l.window)
	defer ticker.Stop()
	for range ticker.C {
		if l.stopped.Load() {
			return
		}
		cutoff := time.Now().Add(-2 * l.window)
		l.mu.Lock()
		for k, b := range l.buckets {
			if b.lastCheck.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

// Stop halts the background GC goroutine.
func (l *Limiter) Stop() {
	l.stopped.Store(true)
}

// Middleware returns an HTTP middleware that rate-limits requests.
//
// For authenticated requests, the key is the user ID from the auth context.
// For unauthenticated requests (e.g. before auth middleware runs), the key
// is the remote IP address. The logger is used for DEBUG-level rate limit
// hit notifications only.
func Middleware(limiter *Limiter, logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := extractKey(r)
			if !limiter.Allow(key) {
				logger.LogAttrs(r.Context(), slog.LevelDebug, "rate limit exceeded",
					slog.String("key_type", keyType(r)),
				)
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Retry-After", strconv.Itoa(int(limiter.window.Seconds())))
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}` + "\n"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// extractKey determines the rate limit bucket key for a request.
func extractKey(r *http.Request) string {
	if u, ok := auth.UserFromContext(r.Context()); ok {
		return "user:" + strconv.FormatInt(u.ID, 10)
	}
	return "ip:" + remoteIP(r.RemoteAddr)
}

func keyType(r *http.Request) string {
	if _, ok := auth.UserFromContext(r.Context()); ok {
		return "user"
	}
	return "ip"
}

func remoteIP(addr string) string {
	if addr == "" {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
