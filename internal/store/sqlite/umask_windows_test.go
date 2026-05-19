//go:build windows

package sqlite_test

// syscallUmask is a no-op stub on Windows. The test that calls it
// (TestOpenTightensDBFileMode) t.Skips on Windows because the umask
// concept does not exist there and POSIX file-mode bits are not
// meaningful, so this stub only exists to keep the file
// cross-platform compilable.
func syscallUmask(_ int) int { return 0 }
