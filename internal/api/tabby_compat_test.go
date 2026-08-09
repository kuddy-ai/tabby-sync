package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestTabbySyncE2ECompat verifies the complete Tabby config sync API flow
// as described in issue #16. This test simulates a Tabby client's full
// sync workflow to ensure API compatibility.
//
// Flow:
// 1. GET /api/1/user - verify authentication
// 2. POST /api/1/configs - create config
// 3. PATCH /api/1/configs/{id} - write content
// 4. GET /api/1/configs/{id} - verify content and modified_at
// 5. Multiple PATCH - verify modified_at changes each time
// 6. GET /api/1/configs - verify list
// 7. DELETE /api/1/configs/{id} - delete config
// 8. GET /api/1/configs/{id} - expect 404
func TestTabbySyncE2ECompat(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)

	// Step 1: GET /api/1/user - verify authentication
	resp, body := doRequest(t, ts, http.MethodGet, "/api/1/user", ts.userAToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/1/user: status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var userResp map[string]json.RawMessage
	if err := json.Unmarshal(body, &userResp); err != nil {
		t.Fatalf("decode user response: %v", err)
	}
	if string(userResp["id"]) != "1" {
		t.Errorf("user.id = %s; want 1", userResp["id"])
	}
	if string(userResp["name"]) != `"alice"` {
		t.Errorf("user.name = %s; want \"alice\"", userResp["name"])
	}

	// Step 2: POST /api/1/configs - create config
	resp, body = doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken,
		map[string]string{"name": "tabby-config"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/1/configs: status = %d; want 201; body=%s", resp.StatusCode, body)
	}
	created := decodeAs[configResp](t, body)
	if created.Name != "tabby-config" {
		t.Errorf("created.name = %q; want \"tabby-config\"", created.Name)
	}
	if created.Content != "" {
		t.Errorf("created.content = %q; want empty", created.Content)
	}

	configIDPath := "/api/1/configs/" + strconv.FormatInt(created.ID, 10)

	// Step 3: PATCH /api/1/configs/{id} - write content
	syncContent := "settings:\n  ssh:\n    hosts:\n      - name: my-server\n        host: example.com\n"
	resp, body = doRequest(t, ts, http.MethodPatch, configIDPath, ts.userAToken,
		map[string]string{"content": syncContent})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH content: status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	patched := decodeAs[configResp](t, body)
	if patched.Content != syncContent {
		t.Errorf("patched.content = %q; want %q", patched.Content, syncContent)
	}

	// Step 4: GET /api/1/configs/{id} - verify content and modified_at
	resp, body = doRequest(t, ts, http.MethodGet, configIDPath, ts.userAToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET config: status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	got := decodeAs[configResp](t, body)
	if got.Content != syncContent {
		t.Errorf("GET content = %q; want %q", got.Content, syncContent)
	}
	if _, err := time.Parse(time.RFC3339Nano, got.ModifiedAt); err != nil {
		t.Errorf("modified_at not RFC3339Nano: %v (%q)", err, got.ModifiedAt)
	}
	// Verify modified_at matches PATCH response
	if got.ModifiedAt != patched.ModifiedAt {
		t.Errorf("GET modified_at = %q; want %q (from PATCH)", got.ModifiedAt, patched.ModifiedAt)
	}

	// Step 5: Multiple PATCH - verify modified_at changes each time
	prevModified, _ := time.Parse(time.RFC3339Nano, got.ModifiedAt)
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Millisecond) // Ensure time advancement

		newContent := "settings:\n  ssh:\n    hosts:\n      - name: server-" + strconv.Itoa(i) + "\n"
		resp, body = doRequest(t, ts, http.MethodPatch, configIDPath, ts.userAToken,
			map[string]string{"content": newContent})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH #%d: status = %d; want 200; body=%s", i, resp.StatusCode, body)
		}
		updated := decodeAs[configResp](t, body)
		curModified, err := time.Parse(time.RFC3339Nano, updated.ModifiedAt)
		if err != nil {
			t.Fatalf("PATCH #%d: parse modified_at: %v", i, err)
		}
		if !curModified.After(prevModified) {
			t.Errorf("PATCH #%d: modified_at not strictly advancing: prev=%s cur=%s",
				i, prevModified.Format(time.RFC3339Nano), updated.ModifiedAt)
		}
		if updated.Content != newContent {
			t.Errorf("PATCH #%d: content = %q; want %q", i, updated.Content, newContent)
		}
		prevModified = curModified
	}

	// Step 6: GET /api/1/configs - verify list
	resp, body = doRequest(t, ts, http.MethodGet, "/api/1/configs", ts.userAToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/1/configs: status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	list := decodeAs[[]configResp](t, body)
	if len(list) != 1 {
		t.Fatalf("list length = %d; want 1; body=%s", len(list), body)
	}
	if list[0].ID != created.ID {
		t.Errorf("list[0].id = %d; want %d", list[0].ID, created.ID)
	}
	if list[0].Name != "tabby-config" {
		t.Errorf("list[0].name = %q; want \"tabby-config\"", list[0].Name)
	}

	// Step 7: DELETE /api/1/configs/{id}
	resp, _ = doRequest(t, ts, http.MethodDelete, configIDPath, ts.userAToken, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: status = %d; want 204", resp.StatusCode)
	}

	// Step 8: GET /api/1/configs/{id} - expect 404
	resp, body = doRequest(t, ts, http.MethodGet, configIDPath, ts.userAToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET after DELETE: status = %d; want 404; body=%s", resp.StatusCode, body)
	}
	errResp := decodeAs[errorResp](t, body)
	if errResp.Error != "not found" {
		t.Errorf("error = %q; want \"not found\"", errResp.Error)
	}
}

// TestTabbySyncAuthRequired verifies that unauthenticated requests are rejected.
func TestTabbySyncAuthRequired(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/1/user"},
		{http.MethodGet, "/api/1/configs"},
		{http.MethodPost, "/api/1/configs"},
		{http.MethodGet, "/api/1/configs/1"},
		{http.MethodPatch, "/api/1/configs/1"},
		{http.MethodDelete, "/api/1/configs/1"},
	}

	for _, ep := range endpoints {
		ep := ep
		t.Run(ep.method+"_"+ep.path, func(t *testing.T) {
			t.Parallel()

			resp, _ := doRequest(t, ts, ep.method, ep.path, "", nil)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d; want 401", resp.StatusCode)
			}
			if resp.Header.Get("WWW-Authenticate") == "" {
				t.Errorf("missing WWW-Authenticate header")
			}
		})
	}
}

// TestTabbySyncUserIsolation verifies users cannot access other users' configs.
func TestTabbySyncUserIsolation(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)

	// User A creates a config
	_, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken,
		map[string]string{"name": "alice-secret"})
	aliceConfig := decodeAs[configResp](t, body)
	configPath := "/api/1/configs/" + strconv.FormatInt(aliceConfig.ID, 10)

	// User B cannot access it
	for _, op := range []struct {
		method string
		body   any
	}{
		{http.MethodGet, nil},
		{http.MethodPatch, map[string]string{"name": "stolen"}},
		{http.MethodDelete, nil},
	} {
		op := op
		t.Run(op.method, func(t *testing.T) {
			t.Parallel()

			resp, b := doRequest(t, ts, op.method, configPath, ts.userBToken, op.body)
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d; want 404; body=%s", resp.StatusCode, b)
			}
		})
	}

	// User B's list should be empty
	resp, body := doRequest(t, ts, http.MethodGet, "/api/1/configs", ts.userBToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user B list: status = %d; body=%s", resp.StatusCode, body)
	}
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("user B sees configs: %s; want []", body)
	}
}

// TestTabbySyncJSONFieldsCompatible verifies JSON field names match Tabby expectations.
func TestTabbySyncJSONFieldsCompatible(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)

	// Create config and verify field names via raw JSON
	_, body := doRequest(t, ts, http.MethodPost, "/api/1/configs", ts.userAToken,
		map[string]string{"name": "test"})
	created := decodeAs[configResp](t, body)

	// Verify expected field names exist
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}

	expectedFields := []string{"id", "name", "content", "last_used_with_version", "created_at", "modified_at"}
	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("missing field %q in response", field)
		}
	}

	// GET single config
	resp, body := doRequest(t, ts, http.MethodGet,
		"/api/1/configs/"+strconv.FormatInt(created.ID, 10), ts.userAToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET: status = %d; body=%s", resp.StatusCode, body)
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal raw GET: %v", err)
	}
	for _, field := range expectedFields {
		if _, ok := raw[field]; !ok {
			t.Errorf("GET missing field %q", field)
		}
	}
}
