// Package auth will host the tabby-sync authentication and authorization logic.
//
// Implementation lands in a later issue. This file exists so the
// package directory ships in the issue #4 skeleton.
package auth

import "net/http"

// Middleware is the contract any future authentication middleware must
// satisfy. Wiring code in internal/server can compose an [auth.Middleware]
// into the request pipeline today and swap in a real implementation when
// authentication ships in a later issue without changing the surrounding
// chain.
type Middleware = func(http.Handler) http.Handler

// None returns a no-op authentication [Middleware] that passes every
// request through unchanged. It is used by the server skeleton to keep
// the middleware chain shape stable until a real authenticator lands;
// callers should NOT rely on it in production once that happens.
func None() Middleware {
	return func(next http.Handler) http.Handler { return next }
}
