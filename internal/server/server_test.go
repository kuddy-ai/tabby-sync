package server_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kuddy-ai/tabby-sync/internal/config"
	"github.com/kuddy-ai/tabby-sync/internal/server"
	"github.com/kuddy-ai/tabby-sync/internal/server/middleware"
	"github.com/kuddy-ai/tabby-sync/internal/version"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestConfig() *config.Config {
	return &config.Config{
		Addr:              "127.0.0.1:0",
		DataDir:           "/tmp/data",
		UsersFile:         "/tmp/users.json",
		MasterKeyProvider: "env",
		LogLevel:          "info",
	}
}

func TestHealthzHandler(t *testing.T) {
	t.Parallel()

	srv := server.New(newTestConfig(), quietLogger())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	srv.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d; want %d", rr.Code, http.StatusOK)
	}
	if got := rr.Body.String(); got != "ok\n" {
		t.Errorf("body = %q; want %q", got, "ok\n")
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q; want %q", ct, "text/plain; charset=utf-8")
	}
}

func TestHealthzDoesNotLeakMetadata(t *testing.T) {
	t.Parallel()

	srv := server.New(newTestConfig(), quietLogger())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)

	body := rr.Body.String()
	// The version string should never appear in the body or in headers we
	// control. (We only check version.Version because it is the only
	// non-empty string of those three at test time without ldflags.)
	if version.Version != "" && strings.Contains(body, version.Version) {
		t.Errorf("/healthz body leaks version %q: %q", version.Version, body)
	}
	for _, hv := range rr.Header() {
		for _, v := range hv {
			if version.Version != "" && strings.Contains(v, version.Version) {
				t.Errorf("/healthz header leaks version %q: %q", version.Version, v)
			}
		}
	}
	// Sanity: the body must not contain config-derived strings either.
	for _, leaked := range []string{"/tmp/data", "/tmp/users.json", "env", "MasterKey"} {
		if strings.Contains(body, leaked) {
			t.Errorf("/healthz body leaks config value %q: %q", leaked, body)
		}
	}
}

func TestHealthzCarriesSecurityHeadersAndRequestID(t *testing.T) {
	t.Parallel()

	srv := server.New(newTestConfig(), quietLogger())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)

	wantHeaders := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
		"Content-Security-Policy": "frame-ancestors 'none'",
		"X-Frame-Options":         "DENY",
	}
	for k, want := range wantHeaders {
		if got := rr.Header().Get(k); got != want {
			t.Errorf("header %q = %q; want %q", k, got, want)
		}
	}
	if got := rr.Header().Get(middleware.HeaderRequestID); got == "" {
		t.Errorf("missing %s header", middleware.HeaderRequestID)
	}
}

func TestServerRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	srv := server.New(newTestConfig(), quietLogger())

	// Even though /healthz is registered as GET, MaxBodyBytes runs ahead of
	// the mux so a POST with a 2 MiB body should be rejected with 413.
	body := bytes.Repeat([]byte("a"), 2<<20)
	req := httptest.NewRequest(http.MethodPost, "/healthz", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d; want %d", rr.Code, http.StatusRequestEntityTooLarge)
	}
	if strings.Contains(rr.Body.String(), "aaaa") {
		t.Errorf("413 response echoes the request body: %q", rr.Body.String())
	}
}

func TestNewSetsTimeoutsAndHeaderCap(t *testing.T) {
	t.Parallel()

	srv := server.New(newTestConfig(), quietLogger())
	if srv.ReadHeaderTimeout <= 0 {
		t.Errorf("ReadHeaderTimeout = %v; want > 0", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout <= 0 {
		t.Errorf("ReadTimeout = %v; want > 0", srv.ReadTimeout)
	}
	if srv.WriteTimeout <= 0 {
		t.Errorf("WriteTimeout = %v; want > 0", srv.WriteTimeout)
	}
	if srv.IdleTimeout <= 0 {
		t.Errorf("IdleTimeout = %v; want > 0", srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes < 1<<20 {
		t.Errorf("MaxHeaderBytes = %d; want >= %d", srv.MaxHeaderBytes, 1<<20)
	}
}

func TestRunGracefulShutdown(t *testing.T) {
	t.Parallel()

	srv := server.New(newTestConfig(), quietLogger())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, srv, ln, quietLogger())
	}()

	// Give Serve a moment to start so the http.Server is ready to accept.
	addr := ln.Addr().String()
	if !waitForServer(t, addr, 2*time.Second) {
		cancel()
		<-done
		t.Fatalf("server did not start listening on %s", addr)
	}

	// Hit /healthz once to confirm the server is actually serving traffic.
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		cancel()
		<-done
		t.Fatalf("GET /healthz failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "ok\n" {
		cancel()
		<-done
		t.Fatalf("/healthz status=%d body=%q; want 200 ok\\n", resp.StatusCode, string(body))
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error after graceful shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s after ctx cancel")
	}
}

func TestRunReturnsServeErrorImmediately(t *testing.T) {
	t.Parallel()

	srv := server.New(newTestConfig(), quietLogger())

	// Pre-close the listener so Serve returns net.ErrClosed without ever
	// being canceled by ctx. Run must surface that error rather than block.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, srv, ln, quietLogger())
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run returned nil; want non-nil error from broken listener")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s when listener was already closed")
	}
}

func waitForServer(t *testing.T, addr string, total time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(total)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
