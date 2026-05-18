//go:build !windows

package sqlite_test

import "syscall"

// syscallUmask is a thin, test-only wrapper around syscall.Umask. It
// lives in the external _test package so the production binary never
// links against it. On non-Windows targets syscall.Umask returns the
// previous mask, which the caller restores via t.Cleanup. The Windows
// counterpart in umask_windows_test.go is a no-op because the umask
// concept does not apply there and TestOpenTightensDBFileMode skips on
// that platform anyway.
func syscallUmask(mask int) int {
	return syscall.Umask(mask)
}
