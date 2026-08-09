package auth_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kuddy-ai/tabby-sync/internal/auth"
)

// fixtureMixed returns a users file with one enabled and one disabled
// user. The enabled user (alice / "alice-token") is reused across
// happy-path tests; the disabled user (carol / "carol-token") feeds the
// disabled-user case.
func fixtureMixed(t *testing.T) string {
	t.Helper()
	body := `users:
  - id: 1
    name: alice
    token_prefix: tbs_alice0
    token_hash: ` + hashOf("alice-token") + `
  - id: 3
    name: carol
    token_prefix: tbs_carol0
    token_hash: ` + hashOf("carol-token") + `
    disabled: true
`
	return writeUsersFile(t, body)
}

// downstreamRecorder is the test-only handler the middleware wraps. It
// records whether it was called, asserts the user-on-context invariant
// when it was, and writes a 200 OK with body "ok".
type downstreamRecorder struct {
	called   bool
	wantUser auth.User // when zero-valued, the test only asserts called=false
}

func (d *downstreamRecorder) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.called = true
		got, ok := auth.UserFromContext(r.Context())
		if (d.wantUser != auth.User{}) {
			if !ok {
				t.Errorf("UserFromContext returned ok=false on success path")
			}
			if got.ID != d.wantUser.ID || got.Name != d.wantUser.Name {
				t.Errorf("UserFromContext = %+v; want %+v", got, d.wantUser)
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// build wires Bearer(store, captureLogger) around a downstreamRecorder
// and returns the wrapped handler plus the log buffer.
func build(t *testing.T, path string, want auth.User) (http.Handler, *downstreamRecorder, *bytes.Buffer) {
	t.Helper()
	store, err := auth.LoadUsersFile(path)
	if err != nil {
		t.Fatalf("LoadUsersFile: %v", err)
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	rec := &downstreamRecorder{wantUser: want}
	h := auth.Bearer(store, logger)(rec.handler(t))
	return h, rec, &buf
}

func TestBearerMissingAuthorization401(t *testing.T) {
	t.Parallel()

	h, rec, _ := build(t, fixtureMixed(t), auth.User{})
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rr.Code)
	}
	if got := rr.Body.String(); got != `{"error":"unauthorized"}`+"\n" {
		t.Errorf("body = %q; want generic unauthorized JSON", got)
	}
	if got := rr.Header().Get("WWW-Authenticate"); got != `Bearer realm="tabby-sync"` {
		t.Errorf("WWW-Authenticate = %q; want Bearer realm=\"tabby-sync\"", got)
	}
	if rec.called {
		t.Error("downstream handler should not be called on 401")
	}
}

func TestBearerWrongSchemeReturns401(t *testing.T) {
	t.Parallel()

	h, rec, _ := build(t, fixtureMixed(t), auth.User{})
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	req.Header.Set("Authorization", "Basic abc")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rr.Code)
	}
	if rec.called {
		t.Error("downstream handler should not be called on 401")
	}
}

func TestBearerWrongTokenReturns401(t *testing.T) {
	t.Parallel()

	h, rec, buf := build(t, fixtureMixed(t), auth.User{})
	const sentinel = "this-is-the-wrong-token"
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	req.Header.Set("Authorization", "Bearer "+sentinel)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rr.Code)
	}
	if rec.called {
		t.Error("downstream handler should not be called on 401")
	}
	logs := buf.String()
	if strings.Contains(logs, sentinel) {
		t.Errorf("logs leaked the wrong token plaintext: %s", logs)
	}
	if strings.Contains(logs, "Bearer "+sentinel) {
		t.Errorf("logs leaked the verbatim Authorization header: %s", logs)
	}
	if strings.Contains(logs, hashOf(sentinel)) {
		t.Errorf("logs leaked the SHA-256 hash of the wrong token: %s", logs)
	}
}

func TestBearerDisabledUserReturns401(t *testing.T) {
	t.Parallel()

	h, rec, _ := build(t, fixtureMixed(t), auth.User{})
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	req.Header.Set("Authorization", "Bearer carol-token")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 (disabled user)", rr.Code)
	}
	if rec.called {
		t.Error("downstream handler should not be called for a disabled user")
	}
}

func TestBearerValidTokenReturns200WithUserContext(t *testing.T) {
	t.Parallel()

	want := auth.User{ID: 1, Name: "alice"}
	h, rec, buf := build(t, fixtureMixed(t), want)
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	req.Header.Set("Authorization", "Bearer alice-token")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rr.Code)
	}
	if !rec.called {
		t.Error("downstream handler not called on success")
	}
	if rr.Body.String() != "ok" {
		t.Errorf("body = %q; want ok", rr.Body.String())
	}
	logs := buf.String()
	if !strings.Contains(logs, `"user_id":1`) {
		t.Errorf("logs missing user_id field: %s", logs)
	}
	if !strings.Contains(logs, `"user_name":"alice"`) {
		t.Errorf("logs missing user_name field: %s", logs)
	}
	if strings.Contains(logs, "alice-token") {
		t.Errorf("logs leaked token plaintext: %s", logs)
	}
	if strings.Contains(logs, hashOf("alice-token")) {
		t.Errorf("logs leaked token hash: %s", logs)
	}
}

func TestBearerEmptyTokenAfterPrefixReturns401(t *testing.T) {
	t.Parallel()

	h, rec, _ := build(t, fixtureMixed(t), auth.User{})
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	// Literal "Bearer " with trailing space and empty token.
	req.Header.Set("Authorization", "Bearer ")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rr.Code)
	}
	if rec.called {
		t.Error("downstream handler should not be called on empty-token 401")
	}
}

func TestBearerHeaderValueIsNeverLogged(t *testing.T) {
	t.Parallel()

	const sentinel = "ULTRA-SECRET-TOKEN-XYZ"
	h, _, buf := build(t, fixtureMixed(t), auth.User{})
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	req.Header.Set("Authorization", "Bearer "+sentinel)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", rr.Code)
	}
	logs := buf.String()
	if strings.Contains(logs, sentinel) {
		t.Errorf("logs leaked the sentinel token: %s", logs)
	}
	if strings.Contains(logs, "Bearer "+sentinel) {
		t.Errorf("logs leaked the verbatim Authorization header: %s", logs)
	}
}

// TestBearerSchemeCaseInsensitiveMatchesRFC7235 pins RFC 7235 §2.1: the
// auth-scheme name is a token compared case-insensitively, so a lenient client
// that normalises the
// scheme to lowercase ("bearer ") or uppercase ("BEARER ") MUST still
// authenticate. This is the regression test for any future change that
// re-tightens the prefix check back to a byte-exact comparison.
func TestBearerSchemeCaseInsensitiveMatchesRFC7235(t *testing.T) {
	t.Parallel()

	for _, scheme := range []string{"bearer ", "BEARER ", "BeArEr "} {
		scheme := scheme
		t.Run(strings.TrimSpace(scheme), func(t *testing.T) {
			t.Parallel()
			want := auth.User{ID: 1, Name: "alice"}
			h, rec, _ := build(t, fixtureMixed(t), want)
			req := httptest.NewRequest(http.MethodGet, "/anything", nil)
			req.Header.Set("Authorization", scheme+"alice-token")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("scheme=%q status = %d; want 200 (RFC 7235 §2.1: scheme is case-insensitive)", scheme, rr.Code)
			}
			if !rec.called {
				t.Errorf("scheme=%q downstream handler was not called", scheme)
			}
		})
	}
}
