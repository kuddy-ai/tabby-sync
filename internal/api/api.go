// Package api hosts the tabby-sync HTTP API handlers exposed under
// /api/1/. The package is mounted by [internal/server.New] when its
// caller passes a non-nil [http.Handler] for the api slot, and it is
// constructed by cli.runServe with a fully wired [store.EncryptedStore]
// so plaintext never leaves the wrapper boundary on the way in or out.
//
// The wire format is intentionally Tabby-compatible: every config row
// is rendered as `{id, name, content, last_used_with_version,
// created_at, modified_at}` with `last_used_with_version` serialised
// to JSON null when the underlying value is empty (the store uses a
// single state for "no version recorded", so the API cannot expose a
// distinct empty string), and lists are serialised as `[]` rather
// than `null` when empty.
//
// Logging contract. Per docs/LOGGING_POLICY.md and AGENTS.md §7 the
// api package emits log lines only on internal-error paths and never
// echoes Authorization headers, token plaintext, decrypted content,
// config Name on the body-decode error path, master-key bytes, or
// full request bodies. Internal errors carry the structured fields
// `op`, `user_id`, and (when the request resolved to one) `config_id`
// only. Decrypt failures use the literal message `decrypt failure`
// with no err field; other internal errors echo only the error
// string returned by the encrypted/sqlite layers (which is already
// path-scrubbed and ciphertext-free at that layer).
package api

import (
	"log/slog"
	"net/http"

	"github.com/kuddy-ai/tabby-sync/internal/store"
)

// New returns an [http.Handler] (a *http.ServeMux under the hood)
// registered for the six Tabby-compatible config-sync endpoints under
// /api/1/. The returned handler relies on the auth middleware run by
// [internal/server.New]: every handler resolves the authenticated
// user via [auth.UserFromContext] and refuses to proceed when the
// slot is empty (the route-aware auth middleware should have written
// 401 long before that, so an empty slot is a programmer error).
//
// The supplied logger is used only for ERROR-level lines on internal
// failure paths; passing a nil logger falls back to [slog.Default].
//
// Routes (Go 1.22+ method-prefixed patterns):
//
//	GET    /api/1/user
//	GET    /api/1/configs
//	POST   /api/1/configs
//	GET    /api/1/configs/{id}
//	PATCH  /api/1/configs/{id}
//	DELETE /api/1/configs/{id}
func New(encStore store.EncryptedStore, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	h := &handlers{
		encStore: encStore,
		logger:   logger,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/1/user", h.handleGetUser)
	mux.HandleFunc("GET /api/1/configs", h.handleListConfigs)
	mux.HandleFunc("POST /api/1/configs", h.handleCreateConfig)
	mux.HandleFunc("GET /api/1/configs/{id}", h.handleGetConfig)
	mux.HandleFunc("PATCH /api/1/configs/{id}", h.handlePatchConfig)
	mux.HandleFunc("DELETE /api/1/configs/{id}", h.handleDeleteConfig)
	return mux
}
