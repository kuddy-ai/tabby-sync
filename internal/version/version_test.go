// The version_test file lives in the same package as version.go (rather than
// `package version_test`) so it can mutate the package-level Version var to
// verify -ldflags overridability without exporting test-only hooks.
package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestInfoContainsExpectedFields(t *testing.T) {
	t.Parallel()

	got := Info()

	for _, want := range []string{
		"tabby-sync",
		Version,
		runtime.GOOS,
		runtime.Version(),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Info() = %q; want substring %q", got, want)
		}
	}
}

func TestInfoReflectsOverriddenVersion(t *testing.T) {
	original := Version
	t.Cleanup(func() { Version = original })

	Version = "9.9.9-test"

	got := Info()
	if !strings.Contains(got, "9.9.9-test") {
		t.Fatalf("Info() = %q; want substring %q", got, "9.9.9-test")
	}
	if strings.Contains(got, original) && original != Version {
		t.Fatalf("Info() = %q; should not contain old version %q", got, original)
	}
}
