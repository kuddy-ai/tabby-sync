package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/kuddy-ai/tabby-sync/internal/auth"
	"github.com/kuddy-ai/tabby-sync/internal/crypto"
	"github.com/kuddy-ai/tabby-sync/internal/store"
)

// timeFormat is the on-the-wire format used for created_at /
// modified_at fields. RFC3339Nano gives at least millisecond
// precision (the strictly-monotonic-modified_at contract from issue
// #9 depends on that) and round-trips cleanly through both
// time.RFC3339 and time.RFC3339Nano on the consumer side.
const timeFormat = time.RFC3339Nano

// errorCode constants are the stable, lowercase strings emitted in
// the {"error": "..."} body of every 4xx/5xx response. They are
// repeated as literal strings in handlers_test.go so a rename here
// must also update the test fixtures.
const (
	errBadRequest      = "bad request"       // syntactically broken or malformed JSON, non-numeric path id
	errInvalidRequest  = "invalid request"   // semantically invalid body (empty name, all-nil patch)
	errNotFound        = "not found"         // missing row or cross-user access
	errInternalError   = "internal error"    // unexpected error path; details are logged, not echoed
	errQuotaExceeded   = "quota exceeded"    // user has reached the maximum number of configs
	errContentTooLarge = "content too large" // config content exceeds 2 MB
)

// MaxConfigsPerUser is the maximum number of configurations a single user
// may own. Attempts to create beyond this limit are rejected with 409.
const MaxConfigsPerUser = 50

// MaxContentBytes is the maximum size of a single config content payload
// (2 MB). Content exceeding this is rejected with 413.
const MaxContentBytes = 2 * 1024 * 1024

// handlers carries the per-request dependencies the six API handlers
// share: the encrypted-store wrapper (which is the single seam to
// the cryptographic envelope) and the logger used for ERROR-level
// lines on internal failure paths. Construct via [New].
type handlers struct {
	encStore store.EncryptedStore
	logger   *slog.Logger
}

// handleGetUser implements GET /api/1/user. The handler returns a
// snapshot of the authenticated user's id and display name plus a
// JSON null active_config slot reserved for a future feature. No
// store call is involved; the handler is effectively an echo of the
// auth context.
func (h *handlers) handleGetUser(w http.ResponseWriter, r *http.Request) {
	u, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, userResponse{
		ID:   u.ID,
		Name: u.Name,
	})
}

// handleListConfigs implements GET /api/1/configs. The slice is
// preallocated with `make([]configResponse, 0, len(rows))` so the
// JSON encoder emits `[]` rather than `null` when the user has no
// configs; tests pin this with a raw-bytes comparison.
func (h *handlers) handleListConfigs(w http.ResponseWriter, r *http.Request) {
	u, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	rows, err := h.encStore.ListConfigsByUserPlaintext(r.Context(), u.ID)
	if err != nil {
		h.writeStoreError(w, r, "list_configs", u.ID, 0, err)
		return
	}
	out := make([]configResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, newConfigResponse(row))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetConfig implements GET /api/1/configs/{id}. Cross-user
// access surfaces from the store as [store.ErrConfigNotFound] which
// the handler maps to 404; a decrypt failure (wrong key, tampered
// row, replay) maps to a generic 500 with `decrypt failure` logged.
func (h *handlers) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	u, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	id, ok := parseConfigID(w, r)
	if !ok {
		return
	}
	row, err := h.encStore.GetConfigPlaintext(r.Context(), u.ID, id)
	if err != nil {
		h.writeStoreError(w, r, "get_config", u.ID, id, err)
		return
	}
	writeJSON(w, http.StatusOK, newConfigResponse(row))
}

// handleCreateConfig implements POST /api/1/configs. The body shape
// is `{name}`; an empty name is treated as 400 invalid request to
// match the issue spec. The wrapper encrypts empty Content under the
// final (userID, configID) AAD inside an atomic store create.
func (h *handlers) handleCreateConfig(w http.ResponseWriter, r *http.Request) {
	u, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	var req createConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, errInvalidRequest)
		return
	}
	// Enforce per-user config quota.
	count, err := h.encStore.CountConfigsByUser(r.Context(), u.ID)
	if err != nil {
		h.writeStoreError(w, r, "count_configs", u.ID, 0, err)
		return
	}
	if count >= MaxConfigsPerUser {
		writeError(w, http.StatusConflict, errQuotaExceeded)
		return
	}
	row, err := h.encStore.CreateConfigPlaintext(r.Context(), u.ID, store.CreateConfigPlaintextInput{
		Name: req.Name,
	})
	if err != nil {
		h.writeStoreError(w, r, "create_config", u.ID, 0, err)
		return
	}
	writeJSON(w, http.StatusCreated, newConfigResponse(row))
}

// handlePatchConfig implements PATCH /api/1/configs/{id}. Every
// field of [patchConfigRequest] is a pointer so the handler can
// distinguish "field absent in JSON" (do not change) from "field
// present and empty" (set to ""). A patch with all three fields
// nil is rejected with 400 invalid request; an explicit
// `last_used_with_version: ""` clears the field on disk (the store
// collapses "" and SQL NULL into a single state).
//
// The wrapper's UpdateConfigPlaintext compares a supplied content value
// with the current plaintext and only re-encrypts when they differ. The
// handler still forwards a non-nil pointer for an explicit empty string so
// `PATCH {"content": ""}` clears non-empty content while an already-empty
// row remains idempotent. See issue #62.
func (h *handlers) handlePatchConfig(w http.ResponseWriter, r *http.Request) {
	u, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	id, ok := parseConfigID(w, r)
	if !ok {
		return
	}
	var req patchConfigRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil && req.Content == nil && req.LastUsedWithVersion == nil {
		writeError(w, http.StatusBadRequest, errInvalidRequest)
		return
	}
	// Enforce max content size (2 MB).
	if req.Content != nil && len(*req.Content) > MaxContentBytes {
		writeError(w, http.StatusRequestEntityTooLarge, errContentTooLarge)
		return
	}
	patch := store.UpdateConfigPlaintextPatch{
		Name:                req.Name,
		LastUsedWithVersion: req.LastUsedWithVersion,
	}
	if req.Content != nil {
		// Forward a non-nil pointer (possibly to an empty slice) so
		// the wrapper re-encrypts under a fresh nonce. An empty
		// plaintext is a valid envelope payload: the resulting
		// ciphertext is just the GCM auth tag.
		b := []byte(*req.Content)
		patch.Content = &b
	}
	row, err := h.encStore.UpdateConfigPlaintext(r.Context(), u.ID, id, patch)
	if err != nil {
		h.writeStoreError(w, r, "update_config", u.ID, id, err)
		return
	}
	writeJSON(w, http.StatusOK, newConfigResponse(row))
}

// handleDeleteConfig implements DELETE /api/1/configs/{id}. Success
// is 204 with no body; a missing or cross-user row is 404 with the
// canonical "not found" error body so the client cannot probe for
// other users' rows.
func (h *handlers) handleDeleteConfig(w http.ResponseWriter, r *http.Request) {
	u, ok := h.currentUser(w, r)
	if !ok {
		return
	}
	id, ok := parseConfigID(w, r)
	if !ok {
		return
	}
	if err := h.encStore.DeleteConfig(r.Context(), u.ID, id); err != nil {
		h.writeStoreError(w, r, "delete_config", u.ID, id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// currentUser returns the authenticated user from the request
// context. The auth middleware run by [internal/server.New]
// populates this slot on every request that reaches the api
// package; an empty slot is therefore a programmer error (the
// middleware was not wired or its order changed). The handler
// writes a generic 500 and logs an ERROR line so the misconfiguration
// is observable without ever leaking which user header was missing.
func (h *handlers) currentUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		h.logger.LogAttrs(r.Context(), slog.LevelError,
			"auth user missing on authenticated request",
			slog.String("op", "current_user"),
		)
		writeError(w, http.StatusInternalServerError, errInternalError)
		return auth.User{}, false
	}
	return u, true
}

// writeStoreError centralises the error-mapping table used by every
// store-touching handler. The mapping is:
//
//   - store.ErrConfigNotFound -> 404 {"error":"not found"} (no log)
//   - crypto.ErrDecrypt       -> 500 {"error":"internal error"} with
//     an ERROR log line carrying op, user_id, config_id and the
//     literal message "decrypt failure"
//   - any other error         -> 500 {"error":"internal error"} with
//     an ERROR log line carrying op, user_id, config_id ONLY
//
// Neither branch echoes the wrapped error string. The catch-all
// branch in particular drops err on the floor so a low-probability
// disk-full / fsync / permission failure inside the SQLite CRUD
// layer cannot surface an underlying os.PathError (whose Path field
// would carry the data-dir path) through the %w chain into the log
// line. The op + user_id + config_id triple is sufficient to
// correlate the failed handler call against the access log and the
// per-request RequestID; startup-time errors that DO carry paths
// are routed through cli.runServe's path-scrubbed logger instead
// of through writeStoreError. TestWriteStoreErrorCatchAllDoesNotEchoWrappedError
// pins this structural property with a synthetic path-bearing error.
func (h *handlers) writeStoreError(w http.ResponseWriter, r *http.Request, op string, userID, configID int64, err error) {
	switch {
	case errors.Is(err, store.ErrConfigNotFound):
		writeError(w, http.StatusNotFound, errNotFound)
	case errors.Is(err, crypto.ErrDecrypt):
		h.logger.LogAttrs(r.Context(), slog.LevelError, "decrypt failure",
			storeErrorAttrs(op, userID, configID)...)
		writeError(w, http.StatusInternalServerError, errInternalError)
	default:
		h.logger.LogAttrs(r.Context(), slog.LevelError, "internal error",
			storeErrorAttrs(op, userID, configID)...)
		writeError(w, http.StatusInternalServerError, errInternalError)
	}
}

// storeErrorAttrs builds the structured-log attribute slice shared
// by the writeStoreError branches. configID is included only when
// positive (the create / list paths set it to 0 because no row id
// has been resolved yet). The wrapped error is intentionally NOT
// part of the attribute set: dropping it at the helper level
// structurally prevents either branch from echoing an underlying
// os.PathError or other path-bearing error through %w-wrapping. The helper
// intentionally has no error parameter, enforcing the no-echo property at the
// type level.
func storeErrorAttrs(op string, userID, configID int64) []slog.Attr {
	attrs := make([]slog.Attr, 0, 3)
	attrs = append(attrs, slog.String("op", op), slog.Int64("user_id", userID))
	if configID > 0 {
		attrs = append(attrs, slog.Int64("config_id", configID))
	}
	return attrs
}

// parseConfigID resolves the {id} path parameter to a positive
// int64. Anything that cannot resolve to a valid row id (non-numeric,
// empty, negative, zero) is rejected with 404 not found rather than
// 400, so a probing client cannot distinguish "obviously invalid id"
// (`/configs/0`, `/configs/abc`) from "valid id, but not yours"
// (`/configs/<other-user-id>`); the cross-user / missing-row path
// already returns 404, and folding bad-id into the same shape keeps
// the surface uniform. The store layer maps any well-formed but absent id to
// [store.ErrConfigNotFound], which writeStoreError surfaces as the same 404.
func parseConfigID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, errNotFound)
		return 0, false
	}
	return id, true
}

// decodeJSON reads the request body and decodes it into v with
// strict semantics: unknown fields are rejected, trailing data
// after the first JSON value is rejected, and a missing/empty body
// is rejected. Any failure writes 400 bad request and returns
// false; the caller should return immediately on false.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, errBadRequest)
		return false
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, errBadRequest)
		return false
	}
	// Reject any trailing data after the first JSON value: a body
	// like `{"name":"a"} {"name":"b"}` is malformed even though
	// each token parses individually.
	if dec.More() {
		writeError(w, http.StatusBadRequest, errBadRequest)
		return false
	}
	// Drain any leftover whitespace so a subsequent reader does
	// not block; the body is small (capped by middleware.MaxBodyBytes).
	_, _ = io.Copy(io.Discard, r.Body)
	return true
}

// writeJSON pins the Content-Type / status / encoding combination
// every 2xx and 4xx response body in this package uses. Encoder
// errors are intentionally swallowed: the status and Content-Type
// have already been written by the time Encode runs, and surfacing
// a write error to the client is not actionable.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError is the writeJSON shortcut for {"error": code} bodies.
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, errorResponse{Error: code})
}
