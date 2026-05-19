package server_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kuddy-ai/tabby-sync/internal/config"
	"github.com/kuddy-ai/tabby-sync/internal/server"
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
	if got := rr.Body.String(); got != "ok" {
		t.Errorf("body = %q; want %q", got, "ok")
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q; want %q", ct, "text/plain; charset=utf-8")
	}
}

func TestNewSetsTimeouts(t *testing.T) {
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
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		cancel()
		<-done
		t.Fatalf("/healthz status=%d body=%q; want 200 ok", resp.StatusCode, string(body))
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
