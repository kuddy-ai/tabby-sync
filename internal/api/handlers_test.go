package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kuddy-ai/tabby-sync/internal/api"
	"github.com/kuddy-ai/tabby-sync/internal/auth"
	"github.com/kuddy-ai/tabby-sync/internal/config"
	"github.com/kuddy-ai/tabby-sync/internal/server"
	"github.com/kuddy-ai/tabby-sync/internal/store/encrypted"
	"github.com/kuddy-ai/tabby-sync/internal/store/sqlite"
)

// User A and User B fixtures pin the cross-user-isolation tests.
// Token plaintexts live alongside their hashes here so a future
// reviewer can see exactly which credential maps to which user.
const (
	userATokenPlaintext = "alice-token-aaaaaaaa"
	userBTokenPlaintext = "bob-token-bbbbbbbbbb"
	wrongScheme         = "Basic " + "ignored-base64=="
)

// testServer bundles the running httptest.Server and the bare tokens
// for both fixture users so each test can build authenticated
// requests without re-deriving the hash.
type testServer struct {
	srv        *httptest.Server
	userAToken string
	userBToken string
	logBuf     *safeBuffer
	masterKey  []byte
}

// safeBuffer is the goroutine-safe slog sink used by the test
// server: the auth and api packages emit log lines from the
// request goroutine while the test goroutine reads them after the
// fact. Re-implemented locally to avoid an internal/auth/_test
// dependency.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newTestServer wires the full production middleware chain (auth,
// security headers, request id, access log, recover, max body) plus
// the api handler around a real sqlite + encrypted store rooted in
// t.TempDir(). Construct one per test that needs end-to-end coverage;
// each instance has its own DB and its own users.yml and is torn
// down by the registered t.Cleanup.
func newTestServer(t *testing.T) *testServer {
	t.Helper()

	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "tabby-sync.db")

	st, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}

	// Deterministic 32-byte master key so the test does not depend
	// on the file provider (which would write to disk) and so the
	// test never accidentally reads real credentials.
	masterKey := bytes.Repeat([]byte{0x42}, 32)
	encStore, err := encrypted.New(st, masterKey)
	if err != nil {
		_ = st.Close()
		t.Fatalf("encrypted.New: %v", err)
	}

	usersDir := t.TempDir()
	usersFile := filepath.Join(usersDir, "users.yml")
	usersYAML := "users:\n" +
		"  - id: 1\n" +
		"    name: alice\n" +
		"    token_prefix: tbs_test01\n" +
		"    token_hash: " + sha256Hex(userATokenPlaintext) + "\n" +
		"    disabled: false\n" +
		"  - id: 2\n" +
		"    name: bob\n" +
		"    token_prefix: tbs_test02\n" +
		"    token_hash: " + sha256Hex(userBTokenPlaintext) + "\n" +
		"    disabled: false\n"
	if err := os.WriteFile(usersFile, []byte(usersYAML), 0o600); err != nil {
		_ = encStore.Close()
		t.Fatalf("write users.yml: %v", err)
	}
	userStore, err := auth.LoadUsersFile(usersFile)
	if err != nil {
		_ = encStore.Close()
		t.Fatalf("auth.LoadUsersFile: %v", err)
	}

	logBuf := &safeBuffer{}
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	authMW := auth.Bearer(userStore, logger)
	apiHandler := api.New(encStore, logger)

	cfg := &config.Config{
		Addr:              "127.0.0.1:0",
		DataDir:           dataDir,
		UsersFile:         usersFile,
		MasterKeyProvider: "env",
		LogLevel:          "info",
	}
	httpServer := server.New(cfg, logger, authMW, apiHandler)

	srv := httptest.NewServer(httpServer.Handler)

	t.Cleanup(func() {
		srv.Close()
		_ = encStore.Close()
	})

	return &testServer{
		srv:        srv,
		userAToken: userATokenPlaintext,
		userBToken: userBTokenPlaintext,
		logBuf:     logBuf,
		masterKey:  masterKey,
	}
}

// sha256Hex returns the lowercase hex-encoded SHA-256 of s. It
// mirrors the helper used by docs/users.yml.example so a future
// editor can copy fixtures between test files without re-deriving
// the format.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// doRequest sends an HTTP request to the test server and returns
// the response and the (already-drained) body bytes. token, when
// non-empty, is sent verbatim as the Bearer credential; the empty
// string means "no Authorization header at all". body, when
// non-nil, is sent as application/json.
func doRequest(t *testing.T, ts *testServer, method, path, token string, body any) (*http.Response, []byte) {
	t.Helper()

	var rdr io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rdr = strings.NewReader(b)
		case []byte:
			rdr = bytes.NewReader(b)
		default:
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			rdr = bytes.NewReader(raw)
		}
	}
	req, err := http.NewRequest(method, ts.srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := ts.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, raw
}

// decodeAs unmarshals body into a fresh T and returns it; on
// failure t.Fatalf's with the raw body so a regression that
// changed the wire shape is easy to debug.
func decodeAs[T any](t *testing.T, body []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %T: %v\nbody: %s", v, err, body)
	}
	return v
}

// configResp is the post-decode shape mirroring api.configResponse.
// It is duplicated here so the test does not import unexported
// types from internal/api and so the JSON contract is pinned by a
// type the test owns.
type configResp struct {
	ID                  int64           `json:"id"`
	Name                string          `json:"name"`
	Content             string          `json:"content"`
	LastUsedWithVersion json.RawMessage `json:"last_used_with_version"`
	CreatedAt           string          `json:"created_at"`
	ModifiedAt          string          `json:"modified_at"`
}

// errorResp mirrors api.errorResponse.
type errorResp struct {
	Error string `json:"error"`
}

// strPtr is a tiny helper for *string fields in patch bodies.
func strPtr(s string) *string { return &s }

func TestGetUser_Happy(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	resp, body := doRequest(t, ts, http.MethodGet, "/api/1/user", ts.userAToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("Content-Type = %q; want application/json", got)
	}
	// Pin the active_config null shape via raw bytes so a regression
	// that changed it to "" or 0 surfaces immediately.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode raw: %v\nbody=%s", err, body)
	}
	if string(raw["active_config"]) != "null" {
		t.Errorf("active_config = %s; want null", raw["active_config"])
	}
	if string(raw["id"]) != "1" {
		t.Errorf("id = %s; want 1", raw["id"])
	}
	if string(raw["name"]) != `"alice"` {
		t.Errorf("name = %s; want \"alice\"", raw["name"])
	}
}

func TestListConfigs_EmptyReturnsArray(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	resp, body := doRequest(t, ts, http.MethodGet, "/api/1/configs", ts.userAToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	// Pin the literal `[]` shape: the encoder writes a trailing
	// newline so the body is "[]\n".
	if got := strings.TrimSpace(string(body)); got != "[]" {
		t.Errorf("body = %q; want \"[]\"", got)
	}
}

func TestCreateThenGet(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	resp, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": "primary"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST status = %d; want 201; body=%s", resp.StatusCode, body)
	}
	created := decodeAs[configResp](t, body)
	if created.ID == 0 {
		t.Fatalf("created.ID = 0; want assigned id; body=%s", body)
	}
	if created.Name != "primary" {
		t.Errorf("created.Name = %q; want \"primary\"", created.Name)
	}
	if created.Content != "" {
		t.Errorf("created.Content = %q; want empty", created.Content)
	}
	if string(created.LastUsedWithVersion) != "null" {
		t.Errorf("created.last_used_with_version = %s; want null", created.LastUsedWithVersion)
	}
	if _, err := time.Parse(time.RFC3339Nano, created.CreatedAt); err != nil {
		t.Errorf("created_at not RFC3339Nano: %v (%q)", err, created.CreatedAt)
	}
	if _, err := time.Parse(time.RFC3339, created.CreatedAt); err != nil {
		t.Errorf("created_at not RFC3339: %v (%q)", err, created.CreatedAt)
	}
	if _, err := time.Parse(time.RFC3339Nano, created.ModifiedAt); err != nil {
		t.Errorf("modified_at not RFC3339Nano: %v (%q)", err, created.ModifiedAt)
	}

	resp, body = doRequest(t, ts, http.MethodGet, "/api/1/configs/"+strconv.FormatInt(created.ID, 10), ts.userAToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	got := decodeAs[configResp](t, body)
	if got.ID != created.ID || got.Name != created.Name || got.Content != created.Content {
		t.Errorf("GET round-trip mismatch: got=%+v; want=%+v", got, created)
	}
}

func TestListReturnsCreated(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	_, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": "primary"})
	created := decodeAs[configResp](t, body)

	resp, body := doRequest(t, ts, http.MethodGet, "/api/1/configs", ts.userAToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	list := decodeAs[[]configResp](t, body)
	if len(list) != 1 {
		t.Fatalf("len(list) = %d; want 1; body=%s", len(list), body)
	}
	if list[0].ID != created.ID || list[0].Name != "primary" {
		t.Errorf("list[0] = %+v; want id=%d name=primary", list[0], created.ID)
	}
}

func TestPatchPartialUpdates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		patch      map[string]any
		wantName   string
		wantContnt string
		wantLUWV   string // "" => null in raw JSON; otherwise the literal string
	}{
		{
			name:       "name_only",
			patch:      map[string]any{"name": "renamed"},
			wantName:   "renamed",
			wantContnt: "",
			wantLUWV:   "",
		},
		{
			name:       "content_only",
			patch:      map[string]any{"content": "settings:\n  foo: 1\n"},
			wantName:   "primary",
			wantContnt: "settings:\n  foo: 1\n",
			wantLUWV:   "",
		},
		{
			name:       "luwv_only",
			patch:      map[string]any{"last_used_with_version": "v1.2.3"},
			wantName:   "primary",
			wantContnt: "",
			wantLUWV:   "v1.2.3",
		},
		{
			name: "all_three",
			patch: map[string]any{
				"name":                   "renamed-all",
				"content":                "settings:\n  bar: 2\n",
				"last_used_with_version": "v9.9.9",
			},
			wantName:   "renamed-all",
			wantContnt: "settings:\n  bar: 2\n",
			wantLUWV:   "v9.9.9",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ts := newTestServer(t)
			_, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": "primary"})
			created := decodeAs[configResp](t, body)

			// Sleep just long enough that even on a clock that does
			// not advance between two RFC3339Nano formats, the
			// monotonic-modified_at fallback (old + 1ms) is
			// observable.
			time.Sleep(2 * time.Millisecond)

			resp, body := doRequest(t, ts, http.MethodPatch,
				"/api/1/configs/"+strconv.FormatInt(created.ID, 10), ts.userAToken, tc.patch)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("PATCH status = %d; want 200; body=%s", resp.StatusCode, body)
			}
			patched := decodeAs[configResp](t, body)
			if patched.ID != created.ID {
				t.Errorf("id = %d; want %d", patched.ID, created.ID)
			}
			if patched.Name != tc.wantName {
				t.Errorf("name = %q; want %q", patched.Name, tc.wantName)
			}
			if patched.Content != tc.wantContnt {
				t.Errorf("content = %q; want %q", patched.Content, tc.wantContnt)
			}
			if tc.wantLUWV == "" {
				if string(patched.LastUsedWithVersion) != "null" {
					t.Errorf("last_used_with_version = %s; want null", patched.LastUsedWithVersion)
				}
			} else {
				if string(patched.LastUsedWithVersion) != strconv.Quote(tc.wantLUWV) {
					t.Errorf("last_used_with_version = %s; want %s", patched.LastUsedWithVersion, strconv.Quote(tc.wantLUWV))
				}
			}

			oldT, _ := time.Parse(time.RFC3339Nano, created.ModifiedAt)
			newT, _ := time.Parse(time.RFC3339Nano, patched.ModifiedAt)
			if !newT.After(oldT) {
				t.Errorf("modified_at did not advance: old=%s new=%s", created.ModifiedAt, patched.ModifiedAt)
			}
		})
	}
}

func TestPatchBumpsModifiedAtRapidly(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	_, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": "primary"})
	created := decodeAs[configResp](t, body)

	prev, err := time.Parse(time.RFC3339Nano, created.ModifiedAt)
	if err != nil {
		t.Fatalf("parse created.modified_at: %v", err)
	}

	for i := 0; i < 10; i++ {
		resp, body := doRequest(t, ts, http.MethodPatch,
			"/api/1/configs/"+strconv.FormatInt(created.ID, 10), ts.userAToken,
			map[string]any{"name": "rev-" + strconv.Itoa(i)})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH #%d status = %d; want 200; body=%s", i, resp.StatusCode, body)
		}
		patched := decodeAs[configResp](t, body)
		cur, err := time.Parse(time.RFC3339Nano, patched.ModifiedAt)
		if err != nil {
			t.Fatalf("PATCH #%d: parse modified_at: %v", i, err)
		}
		if !cur.After(prev) {
			t.Fatalf("PATCH #%d: modified_at not strictly advancing: prev=%s cur=%s", i, prev.Format(time.RFC3339Nano), patched.ModifiedAt)
		}
		prev = cur
	}
}

func TestPatchReturnedModifiedAtMatchesGet(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	_, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": "primary"})
	created := decodeAs[configResp](t, body)

	_, body = doRequest(t, ts, http.MethodPatch,
		"/api/1/configs/"+strconv.FormatInt(created.ID, 10), ts.userAToken,
		map[string]any{"name": "renamed"})
	patched := decodeAs[configResp](t, body)

	_, body = doRequest(t, ts, http.MethodGet,
		"/api/1/configs/"+strconv.FormatInt(created.ID, 10), ts.userAToken, nil)
	got := decodeAs[configResp](t, body)

	if got.ModifiedAt != patched.ModifiedAt {
		t.Errorf("GET modified_at = %q; want %q (PATCH return)", got.ModifiedAt, patched.ModifiedAt)
	}
}

func TestDeleteThenGetReturns404(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	_, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": "primary"})
	created := decodeAs[configResp](t, body)

	resp, _ := doRequest(t, ts, http.MethodDelete,
		"/api/1/configs/"+strconv.FormatInt(created.ID, 10), ts.userAToken, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %d; want 204", resp.StatusCode)
	}

	resp, body = doRequest(t, ts, http.MethodGet,
		"/api/1/configs/"+strconv.FormatInt(created.ID, 10), ts.userAToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET-after-DELETE status = %d; want 404; body=%s", resp.StatusCode, body)
	}
	got := decodeAs[errorResp](t, body)
	if got.Error != "not found" {
		t.Errorf("error = %q; want \"not found\"", got.Error)
	}
}

func TestUnauthenticatedRequestsAre401(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	cases := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/1/user", nil},
		{http.MethodGet, "/api/1/configs", nil},
		{http.MethodPost, "/api/1/configs", map[string]string{"name": "x"}},
		{http.MethodGet, "/api/1/configs/1", nil},
		{http.MethodPatch, "/api/1/configs/1", map[string]string{"name": "x"}},
		{http.MethodDelete, "/api/1/configs/1", nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.method+"_"+tc.path, func(t *testing.T) {
			t.Parallel()

			// No Authorization header.
			resp, _ := doRequest(t, ts, tc.method, tc.path, "", tc.body)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("no-auth status = %d; want 401", resp.StatusCode)
			}
			if resp.Header.Get("WWW-Authenticate") == "" {
				t.Errorf("no-auth: missing WWW-Authenticate header")
			}

			// Wrong scheme (Basic).
			req, err := http.NewRequest(tc.method, ts.srv.URL+tc.path, nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Authorization", wrongScheme)
			respBad, err := ts.srv.Client().Do(req)
			if err != nil {
				t.Fatalf("client.Do: %v", err)
			}
			_ = respBad.Body.Close()
			if respBad.StatusCode != http.StatusUnauthorized {
				t.Errorf("wrong-scheme status = %d; want 401", respBad.StatusCode)
			}

			// Unknown bearer token.
			resp, _ = doRequest(t, ts, tc.method, tc.path, "totally-unknown-token", tc.body)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("unknown-token status = %d; want 401", resp.StatusCode)
			}
		})
	}
}

func TestCrossUserAccessIs404(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)

	// User A creates a config.
	_, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": "alice-only"})
	created := decodeAs[configResp](t, body)

	idPath := "/api/1/configs/" + strconv.FormatInt(created.ID, 10)

	// User B GET / PATCH / DELETE all 404.
	for _, op := range []struct {
		method string
		body   any
	}{
		{http.MethodGet, nil},
		{http.MethodPatch, map[string]any{"name": "stolen"}},
		{http.MethodDelete, nil},
	} {
		op := op
		t.Run(op.method, func(t *testing.T) {
			t.Parallel()
			resp, b := doRequest(t, ts, op.method, idPath, ts.userBToken, op.body)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("user-B %s status = %d; want 404; body=%s", op.method, resp.StatusCode, b)
			}
			got := decodeAs[errorResp](t, b)
			if got.Error != "not found" {
				t.Errorf("user-B %s error = %q; want \"not found\"", op.method, got.Error)
			}
		})
	}

	// User B's listing must NOT include user A's row.
	resp, body := doRequest(t, ts, http.MethodGet, "/api/1/configs", ts.userBToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user-B list status = %d; body=%s", resp.StatusCode, body)
	}
	list := decodeAs[[]configResp](t, body)
	if len(list) != 0 {
		t.Errorf("user-B sees %d configs; want 0; body=%s", len(list), body)
	}
}

func TestMalformedJSONReturns400(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)

	bodies := map[string]string{
		"invalid_json":  `{not json}`,
		"unknown_field": `{"name":"a","unexpected":"field"}`,
		"trailing_data": `{"name":"a"} {"name":"b"}`,
		"empty_body":    ``,
	}
	for label, raw := range bodies {
		raw := raw
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			resp, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, raw)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d; want 400; body=%s", resp.StatusCode, body)
			}
			got := decodeAs[errorResp](t, body)
			if got.Error != "bad request" {
				t.Errorf("error = %q; want \"bad request\"", got.Error)
			}
		})
	}
}

func TestNonNumericConfigIDReturns400(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	for _, m := range []string{http.MethodGet, http.MethodPatch, http.MethodDelete} {
		m := m
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			var body any
			if m == http.MethodPatch {
				body = map[string]any{"name": "x"}
			}
			resp, b := doRequest(t, ts, m, "/api/1/configs/abc", ts.userAToken, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s status = %d; want 400; body=%s", m, resp.StatusCode, b)
			}
			got := decodeAs[errorResp](t, b)
			if got.Error != "bad request" {
				t.Errorf("%s error = %q; want \"bad request\"", m, got.Error)
			}
		})
	}
}

func TestEmptyNameOnCreateReturns400(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	resp, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": ""})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body=%s", resp.StatusCode, body)
	}
	got := decodeAs[errorResp](t, body)
	if got.Error != "invalid request" {
		t.Errorf("error = %q; want \"invalid request\"", got.Error)
	}
}

func TestEmptyPatchReturns400(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	_, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": "primary"})
	created := decodeAs[configResp](t, body)

	resp, body := doRequest(t, ts, http.MethodPatch,
		"/api/1/configs/"+strconv.FormatInt(created.ID, 10), ts.userAToken, map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body=%s", resp.StatusCode, body)
	}
	got := decodeAs[errorResp](t, body)
	if got.Error != "invalid request" {
		t.Errorf("error = %q; want \"invalid request\"", got.Error)
	}
}

func TestLastUsedWithVersionSerialisation(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	_, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": "primary"})
	created := decodeAs[configResp](t, body)
	idPath := "/api/1/configs/" + strconv.FormatInt(created.ID, 10)

	// Set "v1.2.3", then GET, expect "v1.2.3" string in JSON.
	_, _ = doRequest(t, ts, http.MethodPatch, idPath, ts.userAToken,
		map[string]*string{"last_used_with_version": strPtr("v1.2.3")})

	_, body = doRequest(t, ts, http.MethodGet, idPath, ts.userAToken, nil)
	got := decodeAs[configResp](t, body)
	if string(got.LastUsedWithVersion) != `"v1.2.3"` {
		t.Errorf("after set: last_used_with_version = %s; want \"v1.2.3\"", got.LastUsedWithVersion)
	}

	// Clear by sending "" - the store collapses "" and SQL NULL,
	// so GET should report JSON null.
	_, _ = doRequest(t, ts, http.MethodPatch, idPath, ts.userAToken,
		map[string]*string{"last_used_with_version": strPtr("")})

	_, body = doRequest(t, ts, http.MethodGet, idPath, ts.userAToken, nil)
	got = decodeAs[configResp](t, body)
	if string(got.LastUsedWithVersion) != "null" {
		t.Errorf("after clear: last_used_with_version = %s; want null", got.LastUsedWithVersion)
	}
}

func TestContentTypeIsJSON(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)

	// 200 GET /user.
	resp, _ := doRequest(t, ts, http.MethodGet, "/api/1/user", ts.userAToken, nil)
	assertJSONContentType(t, resp, "GET /api/1/user")

	// 200 GET /configs (empty).
	resp, _ = doRequest(t, ts, http.MethodGet, "/api/1/configs", ts.userAToken, nil)
	assertJSONContentType(t, resp, "GET /api/1/configs")

	// 201 POST /configs.
	resp, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": "primary"})
	assertJSONContentType(t, resp, "POST /api/1/configs")
	created := decodeAs[configResp](t, body)
	idPath := "/api/1/configs/" + strconv.FormatInt(created.ID, 10)

	// 200 GET single.
	resp, _ = doRequest(t, ts, http.MethodGet, idPath, ts.userAToken, nil)
	assertJSONContentType(t, resp, "GET single")

	// 200 PATCH.
	resp, _ = doRequest(t, ts, http.MethodPatch, idPath, ts.userAToken, map[string]string{"name": "renamed"})
	assertJSONContentType(t, resp, "PATCH")

	// 400 invalid request (empty name).
	resp, _ = doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": ""})
	assertJSONContentType(t, resp, "400 invalid request")

	// 400 bad request (non-numeric id).
	resp, _ = doRequest(t, ts, http.MethodGet, "/api/1/configs/abc", ts.userAToken, nil)
	assertJSONContentType(t, resp, "400 bad request")

	// 404 not found (cross-user).
	resp, _ = doRequest(t, ts, http.MethodGet, idPath, ts.userBToken, nil)
	assertJSONContentType(t, resp, "404 not found")

	// 204 DELETE has no body and no Content-Type expectation; not asserted here.
	resp, _ = doRequest(t, ts, http.MethodDelete, idPath, ts.userAToken, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE status = %d; want 204", resp.StatusCode)
	}
}

func assertJSONContentType(t *testing.T, resp *http.Response, label string) {
	t.Helper()
	got := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(got, "application/json") {
		t.Errorf("%s: Content-Type = %q; want application/json prefix", label, got)
	}
}

// TestErrorPathsDoNotLeakSecrets pins a negative-substring contract
// on the captured logger across the most failure-rich code paths:
// the auth header value, the bare token plaintext, and the server's
// master-key bytes (in their raw and hex forms) MUST NOT appear in
// any log line. This is a defence-in-depth check on top of the
// auth-package and api-package source-level discipline.
func TestErrorPathsDoNotLeakSecrets(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)

	// 401 path.
	_, _ = doRequest(t, ts, http.MethodGet, "/api/1/user", "totally-unknown-token", nil)

	// 200 path.
	_, _ = doRequest(t, ts, http.MethodGet, "/api/1/user", ts.userAToken, nil)

	// 404 cross-user path.
	_, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken, map[string]string{"name": "primary"})
	created := decodeAs[configResp](t, body)
	idPath := "/api/1/configs/" + strconv.FormatInt(created.ID, 10)
	_, _ = doRequest(t, ts, http.MethodGet, idPath, ts.userBToken, nil)

	logs := ts.logBuf.String()
	if strings.Contains(logs, ts.userAToken) {
		t.Errorf("logs leaked user A token plaintext:\n%s", logs)
	}
	if strings.Contains(logs, ts.userBToken) {
		t.Errorf("logs leaked user B token plaintext:\n%s", logs)
	}
	if strings.Contains(logs, "totally-unknown-token") {
		t.Errorf("logs leaked unknown bearer token:\n%s", logs)
	}
	if strings.Contains(logs, hex.EncodeToString(ts.masterKey)) {
		t.Errorf("logs leaked master key hex:\n%s", logs)
	}
}
