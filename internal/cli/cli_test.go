package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kuddy-ai/tabby-sync/internal/cli"
	"github.com/kuddy-ai/tabby-sync/internal/config"
	"github.com/kuddy-ai/tabby-sync/internal/version"
)

// fakeEnv builds a getenv-compatible closure backed by an in-memory map so
// tests can run in parallel without touching the real process environment.
func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// safeBuffer is a goroutine-safe bytes.Buffer for the serve test, which
// captures stderr from one goroutine while another reads it.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"tabby-sync", "version"}, fakeEnv(nil), &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d; want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "tabby-sync") {
		t.Errorf("stdout = %q; want substring %q", out, "tabby-sync")
	}
	if !strings.Contains(out, version.Version) {
		t.Errorf("stdout = %q; want version substring %q", out, version.Version)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty; got %q", stderr.String())
	}
}

func TestRunNoArgsPrintsUsage(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"tabby-sync"}, fakeEnv(nil), &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d; want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Errorf("stdout missing 'Usage:'; got %q", stdout.String())
	}
}

func TestRunHelpListsAllSubcommands(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"tabby-sync", "help"}, fakeEnv(nil), &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d; want 0", code)
	}
	out := stdout.String()
	for _, sub := range []string{"serve", "version", "help"} {
		if !strings.Contains(out, sub) {
			t.Errorf("help output missing %q; got %q", sub, out)
		}
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"tabby-sync", "bogus"}, fakeEnv(nil), &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code = %d; want 2", code)
	}
	if !strings.HasPrefix(stderr.String(), "unknown command:") {
		t.Errorf("stderr = %q; want prefix %q", stderr.String(), "unknown command:")
	}
}

func TestRunServeMissingRequiredVar(t *testing.T) {
	t.Parallel()

	// Sentinel that should not appear in any output: the test asserts the
	// CLI never echoes other env values when reporting a missing var.
	const sentinel = "SECRET_SENTINEL_VALUE_DO_NOT_LOG"

	env := map[string]string{
		// DataDir intentionally missing.
		config.EnvUsersFile:         sentinel,
		config.EnvMasterKeyProvider: "env",
	}

	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{"tabby-sync", "serve"}, fakeEnv(env), &stdout, &stderr)

	if code != 2 {
		t.Errorf("exit code = %d; want 2", code)
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, config.EnvDataDir) {
		t.Errorf("stderr should mention %q; got %q", config.EnvDataDir, errOut)
	}
	if strings.Contains(errOut, sentinel) {
		t.Errorf("stderr leaked another env value: %q", errOut)
	}
	if strings.Contains(stdout.String(), sentinel) {
		t.Errorf("stdout leaked another env value: %q", stdout.String())
	}
}

func TestRunServeHappyPath(t *testing.T) {
	t.Parallel()

	const sentinelUsersFile = "/this/path/should/never/appear/in/logs/users.json"
	const sentinelProvider = "env"

	sentinelDataDir := t.TempDir()
	env := map[string]string{
		config.EnvAddr:              "127.0.0.1:0",
		config.EnvDataDir:           sentinelDataDir,
		config.EnvUsersFile:         sentinelUsersFile,
		config.EnvMasterKeyProvider: sentinelProvider,
		config.EnvLogLevel:          "info",
	}

	var stdout bytes.Buffer
	stderr := &safeBuffer{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan int, 1)
	go func() {
		done <- cli.Run(ctx, []string{"tabby-sync", "serve"}, fakeEnv(env), &stdout, stderr)
	}()

	// Wait for the "tabby-sync ready" log line, with a hard ceiling so a
	// hang fails the test rather than blocking forever.
	if !waitForLog(stderr, `"msg":"tabby-sync ready"`, 3*time.Second) {
		cancel()
		<-done
		t.Fatalf("timed out waiting for ready log; stderr so far:\n%s", stderr.String())
	}

	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d; want 0", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Run did not return within 3s after ctx cancel; stderr:\n%s", stderr.String())
	}

	logs := stderr.String()
	// Logs must not echo the users-file path.
	if strings.Contains(logs, sentinelUsersFile) {
		t.Errorf("logs leaked TABBY_SYNC_USERS_FILE value:\n%s", logs)
	}
	// Logs must not echo the data-dir path.
	if strings.Contains(logs, sentinelDataDir) {
		t.Errorf("logs leaked TABBY_SYNC_DATA_DIR value:\n%s", logs)
	}
	// Logs must contain the redaction marker for sensitive fields.
	if !strings.Contains(logs, "<set>") {
		t.Errorf("logs missing <set> redaction marker:\n%s", logs)
	}
	// Logs should mention "starting tabby-sync".
	if !strings.Contains(logs, "starting tabby-sync") {
		t.Errorf("logs missing 'starting tabby-sync':\n%s", logs)
	}
}

func waitForLog(buf *safeBuffer, needle string, total time.Duration) bool {
	deadline := time.Now().Add(total)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), needle) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestRunServeStoreOpenFailureRedactsPath provokes a deterministic
// sqlite.Open failure by pointing TABBY_SYNC_DATA_DIR at a regular file
// (so MkdirAll's "not a directory" error fires synchronously) and then
// asserts the resulting log line never echoes that path verbatim. This
// is the regression test for v1 review issue #1: wrapped errors from
// sqlite.Open used to leak the absolute path, defeating the redactPath
// field on the same log line.
func TestRunServeStoreOpenFailureRedactsPath(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	// A regular file masquerading as the data dir: filepath.Dir of
	// "<file>/tabby-sync.db" is the file itself, and os.MkdirAll on a
	// path whose ancestor is a non-directory returns ENOTDIR.
	dataDir := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(dataDir, []byte("blocker"), 0o600); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}

	env := map[string]string{
		config.EnvAddr:              "127.0.0.1:0",
		config.EnvDataDir:           dataDir,
		config.EnvUsersFile:         "/dev/null",
		config.EnvMasterKeyProvider: "env",
		config.EnvLogLevel:          "info",
	}

	var stdout bytes.Buffer
	stderr := &safeBuffer{}

	code := cli.Run(context.Background(), []string{"tabby-sync", "serve"}, fakeEnv(env), &stdout, stderr)
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}

	logs := stderr.String()
	if !strings.Contains(logs, "failed to open sqlite store") {
		t.Errorf("logs missing open-failure marker:\n%s", logs)
	}
	if strings.Contains(logs, dataDir) {
		t.Errorf("logs leaked TABBY_SYNC_DATA_DIR (%q):\n%s", dataDir, logs)
	}
	dbPath := filepath.Join(dataDir, "tabby-sync.db")
	if strings.Contains(logs, dbPath) {
		t.Errorf("logs leaked DB file path (%q):\n%s", dbPath, logs)
	}
	if !strings.Contains(logs, "<redacted>") {
		t.Errorf("logs missing <redacted> scrub marker:\n%s", logs)
	}
}
