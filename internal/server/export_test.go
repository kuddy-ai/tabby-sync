package server

import (
	"log/slog"
	"net/http"

	"github.com/kuddy-ai/tabby-sync/internal/auth"
)

// BuildHandlerForTest exposes [buildHandler] to the server_test package
// so tests can wrap a test-only handler (e.g. one that panics) with the
// same middleware chain that production traffic flows through. It is
// only compiled into the test binary thanks to the _test.go suffix.
//
// Passing nil for authMW is equivalent to passing [auth.None]; the
// production [New] makes the same substitution.
func BuildHandlerForTest(mux http.Handler, logger *slog.Logger, authMW auth.Middleware) http.Handler {
	if authMW == nil {
		authMW = auth.None()
	}
	return buildHandler(mux, logger, authMW)
}
