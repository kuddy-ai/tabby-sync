package middleware

import "net/http"

// CORS returns a middleware that lets the Tabby desktop client's renderer
// process call this API cross-origin. Tabby's ConfigSyncService issues
// plain browser fetch() calls (see tabby-settings/src/services/
// configSync.service.ts upstream) carrying a custom `Authorization`
// header, which is not a CORS "simple request": the browser sends an
// OPTIONS preflight first and refuses to even attempt the real request
// unless that preflight comes back with matching Access-Control-Allow-*
// headers. Without this middleware every request from the desktop app
// fails at the preflight stage - the server never sees it - which
// surfaces in the Tabby UI as a sync tab that spins forever with no
// visible error.
//
// Access-Control-Allow-Origin is set to "*" rather than reflecting the
// request's Origin: tabby-sync does not use cookies, the client never
// sends fetch(..., { credentials: 'include' }), and the Authorization
// bearer token is validated per-request by the auth middleware
// regardless of Origin, so a wildcard does not weaken the actual
// authorization boundary. This also keeps the middleware stateless and
// avoids maintaining an origin allowlist for a value Electron apps set
// inconsistently (file://, app://, or no Origin header at all).
//
// Preflight (OPTIONS) requests are answered here with 204 and no body,
// BEFORE the request reaches MaxBodyBytes or the auth middleware: a
// preflight never carries the real Authorization header by spec, so
// gating it behind auth (as happened before this middleware existed)
// makes every preflight fail with 401 and no CORS headers attached,
// which is indistinguishable from a network error to fetch().
func CORS() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Max-Age", "600")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
