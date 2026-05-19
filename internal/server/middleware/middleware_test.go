package middleware_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/kuddy-ai/tabby-sync/internal/server/middleware"
)

func newJSONLogger(buf io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestRequestIDGeneratesWhenAbsent(t *testing.T) {
	t.Parallel()

	var seen string
	h := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = middleware.MustFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	got := rr.Header().Get(middleware.HeaderRequestID)
	if got == "" {
		t.Fatalf("response missing %s", middleware.HeaderRequestID)
	}
	if got != seen {
		t.Errorf("context id = %q; response header = %q; want match", seen, got)
	}
	matched, err := regexp.MatchString(`^[0-9a-f]{32}$`, got)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Errorf("generated id %q is not a 32-char hex string", got)
	}
}

func TestRequestIDAcceptsWellFormedInbound(t *testing.T) {
	t.Parallel()

	const inbound = "abc-123_DEF.456"
	h := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(middleware.HeaderRequestID, inbound)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get(middleware.HeaderRequestID); got != inbound {
		t.Errorf("response id = %q; want %q", got, inbound)
	}
}

func TestRequestIDRejectsMaliciousInbound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		inbound string
	}{
		{"with space", "bad value"},
		{"with newline", "bad\nvalue"},
		{"with control byte", "bad\x00value"},
		{"with quote", `bad"value`},
		{"too long", strings.Repeat("a", 65)},
		{"empty", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.inbound != "" {
				req.Header.Set(middleware.HeaderRequestID, tc.inbound)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			got := rr.Header().Get(middleware.HeaderRequestID)
			if got == tc.inbound && tc.inbound != "" {
				t.Errorf("middleware accepted malicious id %q", tc.inbound)
			}
			if got == "" {
				t.Errorf("middleware did not generate a replacement id")
			}
			matched, _ := regexp.MatchString(`^[0-9a-f]{32}$`, got)
			if !matched {
				t.Errorf("replacement id %q is not a 32-char hex string", got)
			}
		})
	}
}

func TestRecoverRePanicsErrAbortHandler(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := newJSONLogger(&logBuf)

	chain := middleware.Chain(
		http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			panic(http.ErrAbortHandler)
		}),
		middleware.RequestID,
		middleware.Recover(logger),
	)

	req := httptest.NewRequest(http.MethodGet, "/abort", nil)
	rr := httptest.NewRecorder()

	// Recover must re-panic the sentinel so net/http's own server can
	// observe it. We catch the propagation here in the test.
	var caught any
	func() {
		defer func() {
			caught = recover()
		}()
		chain.ServeHTTP(rr, req)
	}()

	if caught == nil {
		t.Fatalf("expected http.ErrAbortHandler to propagate, got nil")
	}
	if err, ok := caught.(error); !ok || err != http.ErrAbortHandler { //nolint:errorlint // sentinel comparison is intentional
		t.Fatalf("propagated value = %#v; want http.ErrAbortHandler", caught)
	}

	logs := logBuf.String()
	if strings.Contains(logs, "panic recovered") {
		t.Errorf("Recover should not log when re-panicking ErrAbortHandler: %s", logs)
	}
}

func TestRecoverLogsPanicType(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := newJSONLogger(&logBuf)

	chain := middleware.Chain(
		http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			panic(42) // int panic to make panic_type easy to assert
		}),
		middleware.RequestID,
		middleware.Recover(logger),
	)

	req := httptest.NewRequest(http.MethodGet, "/typed-panic", nil)
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", rr.Code)
	}
	logs := logBuf.String()
	if !strings.Contains(logs, `"panic_type":"int"`) {
		t.Errorf("logs missing panic_type=int: %s", logs)
	}
}

// errReader simulates an io.Reader that returns a non-EOF error on the
// first read, which is what MaxBodyBytes turns into a 400 response.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func (errReader) Close() error { return nil }

func TestMaxBodyBytesReadErrorReturns400(t *testing.T) {
	t.Parallel()

	called := false
	chain := middleware.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}),
		middleware.MaxBodyBytes(1024),
	)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = errReader{}
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", rr.Code)
	}
	if got := rr.Body.String(); got != "invalid request body\n" {
		t.Errorf("body = %q; want %q", got, "invalid request body\n")
	}
	if called {
		t.Errorf("downstream handler ran despite mid-read error")
	}
}

// flushRecorder is an httptest.ResponseRecorder that tracks how many
// times Flush was called; it lets us assert that statusRecorder forwards
// http.Flusher to the underlying writer.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (f *flushRecorder) Flush() { f.flushed++ }

func TestStatusRecorderForwardsFlush(t *testing.T) {
	t.Parallel()

	fr := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	chain := middleware.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			f, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("ResponseWriter does not implement http.Flusher inside the chain")
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("partial"))
			f.Flush()
		}),
		middleware.AccessLog(slog.New(slog.NewJSONHandler(io.Discard, nil))),
		middleware.Recover(slog.New(slog.NewJSONHandler(io.Discard, nil))),
	)

	chain.ServeHTTP(fr, httptest.NewRequest(http.MethodGet, "/", nil))

	if fr.flushed == 0 {
		t.Errorf("underlying ResponseWriter.Flush was never invoked through the chain")
	}
}

func TestRecoverWritesGeneric500AndLogs(t *testing.T) {
	t.Parallel()

	const panicMsg = "kaboom-secret-stack-marker"

	var logBuf bytes.Buffer
	logger := newJSONLogger(&logBuf)

	chain := middleware.Chain(
		http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			panic(panicMsg)
		}),
		middleware.RequestID,
		middleware.Recover(logger),
	)

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500", rr.Code)
	}
	body := rr.Body.String()
	if body != "internal server error\n" {
		t.Errorf("body = %q; want generic 500 message", body)
	}
	if strings.Contains(body, panicMsg) {
		t.Errorf("response body leaks panic value: %q", body)
	}
	if strings.Contains(body, "goroutine") {
		t.Errorf("response body leaks stack trace: %q", body)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "panic recovered") {
		t.Errorf("logs missing 'panic recovered': %s", logs)
	}
	if !strings.Contains(logs, panicMsg) {
		t.Errorf("logs missing panic value %q: %s", panicMsg, logs)
	}
	if !strings.Contains(logs, "goroutine") {
		t.Errorf("logs missing stack trace: %s", logs)
	}
	if !strings.Contains(logs, `"level":"ERROR"`) {
		t.Errorf("logs missing ERROR level: %s", logs)
	}
}

func TestAccessLogDoesNotLeakAuthorizationHeader(t *testing.T) {
	t.Parallel()

	const authValue = "Bearer ULTRA-SECRET-TOKEN-A1B2C3D4"

	var logBuf bytes.Buffer
	logger := newJSONLogger(&logBuf)

	chain := middleware.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("brewing"))
		}),
		middleware.RequestID,
		middleware.AccessLog(logger),
	)

	req := httptest.NewRequest(http.MethodGet, "/teapot?token=should-not-matter", nil)
	req.Header.Set("Authorization", authValue)
	req.Header.Set("Cookie", "session=top-secret-cookie")
	req.RemoteAddr = "10.20.30.40:54321"
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)

	logs := logBuf.String()
	if strings.Contains(logs, authValue) {
		t.Errorf("access log leaks Authorization header: %s", logs)
	}
	if strings.Contains(logs, "ULTRA-SECRET-TOKEN") {
		t.Errorf("access log leaks Authorization token fragment: %s", logs)
	}
	if strings.Contains(logs, "top-secret-cookie") {
		t.Errorf("access log leaks Cookie value: %s", logs)
	}
	if !strings.Contains(logs, `"method":"GET"`) {
		t.Errorf("access log missing method: %s", logs)
	}
	if !strings.Contains(logs, `"path":"/teapot"`) {
		t.Errorf("access log missing path: %s", logs)
	}
	if !strings.Contains(logs, `"status":418`) {
		t.Errorf("access log missing status: %s", logs)
	}
	if !strings.Contains(logs, `"remote_ip":"10.20.30.40"`) {
		t.Errorf("access log missing remote_ip: %s", logs)
	}
	if !strings.Contains(logs, `"request_id":`) {
		t.Errorf("access log missing request_id: %s", logs)
	}
}

func TestMaxBodyBytesAtAndOverLimit(t *testing.T) {
	t.Parallel()

	const limit int64 = 16

	got := make(chan []byte, 1)
	chain := middleware.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var b []byte
			if r.Body != nil {
				b, _ = io.ReadAll(r.Body)
			}
			got <- b
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}),
		middleware.MaxBodyBytes(limit),
	)

	t.Run("at limit passes through", func(t *testing.T) {
		body := bytes.Repeat([]byte("x"), int(limit))
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		chain.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200", rr.Code)
		}
		select {
		case forwarded := <-got:
			if !bytes.Equal(forwarded, body) {
				t.Errorf("handler saw %q; want %q", forwarded, body)
			}
		default:
			t.Fatal("handler never ran")
		}
	})

	t.Run("over limit returns 413", func(t *testing.T) {
		body := bytes.Repeat([]byte("y"), int(limit)+1)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		chain.ServeHTTP(rr, req)

		if rr.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d; want 413", rr.Code)
		}
		if strings.Contains(rr.Body.String(), "yyyy") {
			t.Errorf("413 body echoes request body: %q", rr.Body.String())
		}
		select {
		case <-got:
			t.Fatal("handler should not have run for over-limit request")
		default:
		}
	})

	t.Run("nil body passes through", func(t *testing.T) {
		// httptest.NewRequest with nil body still wraps a non-nil io.Reader
		// internally, so build the request manually to exercise the
		// r.Body == nil branch.
		req := &http.Request{
			Method:     http.MethodGet,
			URL:        mustParseURL(t, "/"),
			Host:       "example.com",
			RemoteAddr: "127.0.0.1:1234",
			Header:     http.Header{},
		}
		rr := httptest.NewRecorder()
		chain.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("status = %d; want 200 for nil body", rr.Code)
		}
		<-got // drain
	})
}

func TestSecurityHeadersSetsAllFive(t *testing.T) {
	t.Parallel()

	chain := middleware.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		middleware.SecurityHeaders(),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	chain.ServeHTTP(rr, req)

	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
		"Cache-Control":           "no-store",
		"Content-Security-Policy": "frame-ancestors 'none'",
		"X-Frame-Options":         "DENY",
	}
	for k, v := range want {
		if got := rr.Header().Get(k); got != v {
			t.Errorf("header %q = %q; want %q", k, got, v)
		}
	}
	// HSTS must NOT be set by this middleware.
	if got := rr.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS leaked: %q", got)
	}
}

func TestChainOrderingOuterRunsFirst(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		log []string
	)
	record := func(name string) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				log = append(log, "before:"+name)
				mu.Unlock()
				next.ServeHTTP(w, r)
				mu.Lock()
				log = append(log, "after:"+name)
				mu.Unlock()
			})
		}
	}

	final := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		log = append(log, "handler")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	chain := middleware.Chain(final, record("outer"), record("middle"), record("inner"))
	chain.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{
		"before:outer",
		"before:middle",
		"before:inner",
		"handler",
		"after:inner",
		"after:middle",
		"after:outer",
	}
	if !equalSlices(log, want) {
		t.Errorf("call order = %v; want %v", log, want)
	}
}

func TestFromContextEmptyByDefault(t *testing.T) {
	t.Parallel()

	if v, ok := middleware.FromContext(context.Background()); ok || v != "" {
		t.Errorf("FromContext on empty context = (%q, %v); want (\"\", false)", v, ok)
	}
	if v := middleware.MustFromContext(context.Background()); v != "" {
		t.Errorf("MustFromContext on empty context = %q; want \"\"", v)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
