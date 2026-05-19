package api

import (
	"log/slog"
	"net/http"
)

// WriteStoreErrorForTest exposes the unexported writeStoreError
// method to the external api_test package so tests can drive the
// catch-all branch with a synthetic wrapped error and assert the
// captured slog output does not echo the inner error string.
//
// The seam is only compiled into the test binary thanks to the
// _test.go suffix; the symbol is not exported in the production
// package. v2 semantic review residual #1 for #8 + #9 needed a way
// to feed writeStoreError a synthetic os.PathError-bearing wrapped
// error without standing up the full EncryptedStore + httptest.Server
// stack, so this seam exists alongside the existing end-to-end
// fixtures rather than replacing them.
func WriteStoreErrorForTest(logger *slog.Logger, w http.ResponseWriter, r *http.Request, op string, userID, configID int64, err error) {
	h := &handlers{logger: logger}
	h.writeStoreError(w, r, op, userID, configID, err)
}
