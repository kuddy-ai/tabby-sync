package sqlite

import (
	"testing"
	"time"
)

// SetClockForTest swaps the package-private nowFn clock seam for the
// duration of the calling test and registers a t.Cleanup that restores
// the prior value. It exists so tests in package sqlite_test can
// simulate clock jumps (in particular, a backwards skew or a stalled
// wall clock) without exporting the seam from the production API.
//
// Tests that rely on this helper MUST NOT also run with t.Parallel(),
// because the seam is package-global state. The helper does not
// enforce the no-parallel rule itself; the burden is on the test.
func SetClockForTest(t *testing.T, fn func() time.Time) {
	t.Helper()
	if fn == nil {
		t.Fatalf("SetClockForTest: fn must not be nil")
	}
	prev := nowFn
	nowFn = fn
	t.Cleanup(func() { nowFn = prev })
}
