package api

import "github.com/kuddy-ai/tabby-sync/internal/store"

// configResponse is the wire format for every config-bearing response
// (POST, GET single, GET list element, PATCH). The JSON shape is the
// Tabby-compatible {id, name, content, last_used_with_version,
// created_at, modified_at} contract pinned by tests in
// handlers_test.go; do not rename or reorder these fields without
// updating the wire-format consumers.
//
// LastUsedWithVersion is a *string so the JSON encoder emits null
// when the underlying value is empty (instead of "" or omitting the
// field). The store contract treats the empty string as the single
// "no version recorded" sentinel, so the API only exposes that one
// state via JSON null and the round-trip "" -> null is intentional.
type configResponse struct {
	ID                  int64   `json:"id"`
	Name                string  `json:"name"`
	Content             string  `json:"content"`
	LastUsedWithVersion *string `json:"last_used_with_version"`
	CreatedAt           string  `json:"created_at"`
	ModifiedAt          string  `json:"modified_at"`
}

// userResponse is the wire format for GET /api/1/user. ActiveConfig
// is intentionally a *int64 nil pointer so the encoder emits JSON
// null; the field is reserved for a future "currently selected
// config id" feature and has no on-disk source today.
type userResponse struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	ActiveConfig *int64 `json:"active_config"`
}

// createConfigRequest is the JSON body of POST /api/1/configs. The
// handler treats a missing or empty Name as a 400 invalid request;
// Content is intentionally not part of the create payload (clients
// post an empty config and PATCH the content afterwards, matching
// the upstream Tabby protocol).
type createConfigRequest struct {
	Name string `json:"name"`
}

// patchConfigRequest is the JSON body of PATCH /api/1/configs/{id}.
// Every field is a pointer so a missing field ("do not change this")
// is distinguishable from an explicit empty string ("clear this").
// LastUsedWithVersion=*"" maps to SQL NULL on disk per the store
// contract, which the API surfaces as JSON null on the next read.
type patchConfigRequest struct {
	Name                *string `json:"name"`
	Content             *string `json:"content"`
	LastUsedWithVersion *string `json:"last_used_with_version"`
}

// errorResponse is the JSON body emitted for every 4xx/5xx response
// produced by the api package. The single "error" field carries a
// short, stable code (e.g. "not found", "bad request", "invalid
// request", "internal error") that clients can match against; the
// raw underlying error is never echoed.
type errorResponse struct {
	Error string `json:"error"`
}

// newConfigResponse converts a [store.ConfigWithPlaintext] into the
// wire-format [configResponse]. The empty-string -> nil mapping for
// LastUsedWithVersion is centralised here so every handler emits the
// same shape (POST, GET single, GET list, PATCH all hit this one
// constructor). Timestamps are formatted with time.RFC3339Nano so
// clients see at least millisecond precision and so the round-trip
// `time.Parse(time.RFC3339, s)` keeps working for clients that
// accept only the non-Nano profile.
func newConfigResponse(c store.ConfigWithPlaintext) configResponse {
	resp := configResponse{
		ID:         c.ID,
		Name:       c.Name,
		Content:    string(c.Content),
		CreatedAt:  c.CreatedAt.UTC().Format(timeFormat),
		ModifiedAt: c.ModifiedAt.UTC().Format(timeFormat),
	}
	if c.LastUsedWithVersion != "" {
		v := c.LastUsedWithVersion
		resp.LastUsedWithVersion = &v
	}
	return resp
}
