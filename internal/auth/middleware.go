package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
)

// bearerPrefix is the literal RFC 6750 scheme prefix the middleware
// expects, including the trailing space. The match is case-sensitive
// per RFC 6750 §2.1 ("The string 'Bearer' is case-sensitive").
const bearerPrefix = "Bearer "

// ctxUserKey is the unexported context key used to stash an authenticated
// [User] on a request's context. Keeping the type unexported prevents
// other packages from constructing the same key and either reading or
// overwriting the user slot.
type ctxUserKey struct{}

// UserFromContext returns a copy of the [User] stashed by the [Bearer]
// middleware on ctx and reports whether one was present. Callers always
// receive a value-typed copy so they cannot mutate the snapshot the
// store still holds a pointer to.
func UserFromContext(ctx context.Context) (User, bool) {
	if ctx == nil {
		return User{}, false
	}
	v, ok := ctx.Value(ctxUserKey{}).(User)
	if !ok {
		return User{}, false
	}
	return v, true
}

// Bearer returns a [Middleware] that authenticates requests via the
// Authorization: Bearer <token> header against the supplied [UserStore].
//
// On success the resolved [User] is stashed on the request context (see
// [UserFromContext]) and the wrapped handler runs. On any failure path
// the middleware writes 401 with a generic JSON body and a
// WWW-Authenticate header, and the wrapped handler is NOT called.
//
// Logging contract. The middleware emits exactly one structured log
// line per request via the supplied logger:
//
//   - success: DEBUG "authorized" with user_id (int64) and user_name
//     (string). No token, hash, or header value is ever logged.
//   - failure: DEBUG "unauthorized" with NO further detail. The
//     middleware MUST NOT distinguish "missing header" from "wrong
//     scheme" from "wrong token" from "disabled user" in either the
//     response body or the log line; this prevents a probing client
//     from learning anything from differential responses.
//
// If logger is nil, [slog.Default] is used.
func Bearer(store *UserStore, logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if authz == "" || !strings.HasPrefix(authz, bearerPrefix) {
				logger.LogAttrs(r.Context(), slog.LevelDebug, "unauthorized")
				writeUnauthorized(w)
				return
			}
			token := strings.TrimPrefix(authz, bearerPrefix)
			// An empty token after the prefix, or one carrying any
			// whitespace, is rejected. Tokens are opaque server-issued
			// strings and never legitimately contain whitespace.
			if token == "" || strings.ContainsAny(token, " \t\r\n") {
				logger.LogAttrs(r.Context(), slog.LevelDebug, "unauthorized")
				writeUnauthorized(w)
				return
			}
			user, err := store.Lookup(token)
			if err != nil {
				logger.LogAttrs(r.Context(), slog.LevelDebug, "unauthorized")
				writeUnauthorized(w)
				return
			}
			logger.LogAttrs(r.Context(), slog.LevelDebug, "authorized",
				slog.Int64("user_id", user.ID),
				slog.String("user_name", user.Name),
			)
			ctx := context.WithValue(r.Context(), ctxUserKey{}, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// writeUnauthorized writes the canonical 401 response. The body is the
// literal JSON object {"error":"unauthorized"} with a trailing newline,
// chosen so a probing client cannot distinguish missing-header from
// wrong-scheme from wrong-token from disabled-user. The
// WWW-Authenticate header advertises the Bearer scheme per RFC 6750.
func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("WWW-Authenticate", `Bearer realm="tabby-sync"`)
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"unauthorized"}` + "\n"))
}
