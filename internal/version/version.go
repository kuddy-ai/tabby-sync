// Package version exposes build-time identifying metadata for tabby-sync.
//
// The Version, Commit, and Date variables are intentionally declared as
// package-level vars (not consts) so that release builds can override them
// via -ldflags "-X github.com/kuddy-ai/tabby-sync/internal/version.Version=..."
// without rebuilding the source.
package version

import (
	"fmt"
	"runtime"
)

// Version is the semantic version of the binary. Override via -ldflags at
// build time. The default value indicates an unreleased local build.
var Version = "0.0.0-dev"

// Commit is the VCS commit identifier the binary was built from. Override via
// -ldflags at build time. The default value indicates an unknown source.
var Commit = "unknown"

// Date is the UTC build timestamp. Override via -ldflags at build time. The
// default value indicates an unknown build time.
var Date = "unknown"

// Info returns a single-line human-readable summary of the build identity.
// The output is intended for the `version` CLI subcommand and for structured
// log attributes; it never contains secrets.
func Info() string {
	return fmt.Sprintf(
		"tabby-sync %s (commit %s, built %s, go %s, %s/%s)",
		Version, Commit, Date, runtime.Version(), runtime.GOOS, runtime.GOARCH,
	)
}
