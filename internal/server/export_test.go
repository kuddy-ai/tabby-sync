package server

import (
	"log/slog"
	"net/http"
)

// BuildHandlerForTest exposes [buildHandler] to the server_test package
// so tests can wrap a test-only handler (e.g. one that panics) with the
// same middleware chain that production traffic flows through. It is
// only compiled into the test binary thanks to the _test.go suffix.
func BuildHandlerForTest(mux http.Handler, logger *slog.Logger) http.Handler {
	return buildHandler(mux, logger)
}
