package auth

import "net/http"

// Middleware is the contract every authentication middleware in this
// package satisfies. Wiring code in internal/server composes a single
// [Middleware] value into the request pipeline so the route-aware
// /healthz bypass and the real Bearer authenticator share one shape.
type Middleware = func(http.Handler) http.Handler

// None returns a no-op [Middleware] that passes every request through
// unchanged. It exists for two callers: tests that build the middleware
// chain without exercising auth, and [internal/server.New] when a nil
// auth middleware is supplied (so existing tests that pass nil keep
// working). Production wiring MUST use [Bearer]; callers should NOT
// rely on None to short-circuit auth at runtime.
func None() Middleware {
	return func(next http.Handler) http.Handler { return next }
}
