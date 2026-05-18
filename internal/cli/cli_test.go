package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
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

	const sentinelProvider = "file"

	sentinelDataDir := t.TempDir()

	// Write a real users.yml so runServe's auth.LoadUsersFile call
	// succeeds. The fixture mirrors the deterministic one documented in
	// the task brief: id=1, name=alice, token_prefix=tbs_test01,
	// token_hash=sha256("alice-token") hex-encoded, disabled=false.
	usersDir := t.TempDir()
	sentinelUsersFile := filepath.Join(usersDir, "users.yml")
	tokenHash := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	}("alice-token")
	usersYAML := "users:\n" +
		"  - id: 1\n" +
		"    name: alice\n" +
		"    token_prefix: tbs_test01\n" +
		"    token_hash: " + tokenHash + "\n" +
		"    disabled: false\n"
	if err := os.WriteFile(sentinelUsersFile, []byte(usersYAML), 0o600); err != nil {
		t.Fatalf("seed users.yml: %v", err)
	}

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
	// v1 review issue #7: the happy path must emit a structured
	// "users file loaded" log line so an operator can confirm the
	// users.yml was consumed without the line ever naming a user.
	// Pinning this string here means a regression that dropped the
	// log call (or renamed the message) would surface immediately.
	if !strings.Contains(logs, `"msg":"users file loaded"`) {
		t.Errorf("logs missing 'users file loaded' line:\n%s", logs)
	}
	// v1 review issue #7: the load path must NOT log user names. The
	// fixture loads exactly one user named "alice"; a regression that
	// started logging the name (or any user-identifying field other
	// than the count) would land "alice" in the captured stderr.
	if strings.Contains(logs, "alice") {
		t.Errorf("logs leaked the loaded user name 'alice':\n%s", logs)
	}
	// And the structured user_count field should be present, since
	// it is the only operational signal that pins a non-empty load.
	if !strings.Contains(logs, `"user_count":1`) {
		t.Errorf("logs missing user_count=1 field:\n%s", logs)
	}

	// Issue #10: the master-key load emits exactly one structured
	// "master key loaded" line carrying only the provider field.
	if !strings.Contains(logs, `"msg":"master key loaded"`) {
		t.Errorf("logs missing 'master key loaded' line:\n%s", logs)
	}
	if !strings.Contains(logs, `"provider":"file"`) {
		t.Errorf("logs missing provider=file field:\n%s", logs)
	}

	// The master.key fixture must end up under cfg.DataDir at the
	// canonical filename, with mode 0o600 and size MasterKeySize.
	masterKeyPath := filepath.Join(sentinelDataDir, "master.key")
	st, err := os.Stat(masterKeyPath)
	if err != nil {
		t.Fatalf("master.key not created: %v", err)
	}
	if st.Size() != 32 {
		t.Errorf("master.key size = %d; want 32", st.Size())
	}
	if runtime.GOOS != "windows" {
		if mode := st.Mode().Perm(); mode != 0o600 {
			t.Errorf("master.key mode = %o; want 0o600", mode)
		}
	}

	// Logs must not echo the master.key path verbatim.
	if strings.Contains(logs, masterKeyPath) {
		t.Errorf("logs leaked master.key path:\n%s", logs)
	}

	// And the auto-generated key bytes must not appear in any form
	// (the provider does not let us pin the value, but we can at
	// least assert the file's hex form is absent).
	keyBytes, err := os.ReadFile(masterKeyPath)
	if err != nil {
		t.Fatalf("read master.key: %v", err)
	}
	keyHex := hex.EncodeToString(keyBytes)
	if strings.Contains(logs, keyHex) {
		t.Errorf("logs leaked master key hex:\n%s", logs)
	}
}

func TestRunServeEnvProviderHappyPath(t *testing.T) {
	// NOTE: this test mutates the process env via t.Setenv (the env
	// provider falls through to os.Getenv at Load time when
	// LoadFromConfig is called) so it does NOT call t.Parallel(). The
	// other tests in this file remain parallel.

	const sentinelProvider = "env"

	sentinelDataDir := t.TempDir()
	usersDir := t.TempDir()
	sentinelUsersFile := filepath.Join(usersDir, "users.yml")
	tokenHash := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	}("alice-token")
	usersYAML := "users:\n" +
		"  - id: 1\n" +
		"    name: alice\n" +
		"    token_prefix: tbs_test01\n" +
		"    token_hash: " + tokenHash + "\n" +
		"    disabled: false\n"
	if err := os.WriteFile(sentinelUsersFile, []byte(usersYAML), 0o600); err != nil {
		t.Fatalf("seed users.yml: %v", err)
	}

	// Deterministic 32-byte master key so the test can assert the
	// hex form does NOT appear in captured logs (negative-control
	// sentinel substring).
	masterKeyBytes := bytes.Repeat([]byte{0xAB}, 32)
	masterKeyHex := hex.EncodeToString(masterKeyBytes)
	t.Setenv("TABBY_SYNC_MASTER_KEY", masterKeyHex)

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
	if !strings.Contains(logs, `"msg":"master key loaded"`) {
		t.Errorf("logs missing 'master key loaded' line:\n%s", logs)
	}
	if !strings.Contains(logs, `"provider":"env"`) {
		t.Errorf("logs missing provider=env field:\n%s", logs)
	}
	// The master-key hex value must NOT appear anywhere in the logs.
	if strings.Contains(logs, masterKeyHex) {
		t.Errorf("logs leaked master key hex:\n%s", logs)
	}
	// And the auto-generation path must NOT have created an on-disk
	// master.key under cfg.DataDir when the env provider is selected.
	if _, err := os.Stat(filepath.Join(sentinelDataDir, "master.key")); !os.IsNotExist(err) {
		t.Errorf("env provider should not auto-generate on disk; stat err = %v", err)
	}
}

// TestRunServeMasterKeyWrongLength seeds a deliberately short
// master.key file under cfg.DataDir and asserts the master-key load
// fails with a redacted error: exit code 1, the structured failure
// message is present with provider=file, the master.key path is
// absent verbatim, and no other suspicious substring (the seeded
// key bytes, the data-dir path, the parent directory of the
// fixture) leaks. The keys package returns the bare
// keys.ErrInvalidLength sentinel for a wrong-length file (no path
// is wrapped in that error path), so the cli-layer scrubPaths pass
// is a no-op and the "<redacted>" marker is NOT guaranteed to
// appear; the no-leak contract is therefore enforced as
// path-absence + suspicious-substring-absence rather than as
// "<redacted>" presence.
func TestRunServeMasterKeyWrongLength(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()

	// Seed a 31-byte master.key so the file provider's length check
	// trips on first read.
	masterKeyPath := filepath.Join(dataDir, "master.key")
	seededKey := bytes.Repeat([]byte{0xAB}, 31)
	if err := os.WriteFile(masterKeyPath, seededKey, 0o600); err != nil {
		t.Fatalf("seed short master.key: %v", err)
	}

	usersDir := t.TempDir()
	usersFile := filepath.Join(usersDir, "users.yml")
	tokenHash := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	}("alice-token")
	usersYAML := "users:\n" +
		"  - id: 1\n" +
		"    name: alice\n" +
		"    token_prefix: tbs_test01\n" +
		"    token_hash: " + tokenHash + "\n" +
		"    disabled: false\n"
	if err := os.WriteFile(usersFile, []byte(usersYAML), 0o600); err != nil {
		t.Fatalf("seed users.yml: %v", err)
	}

	env := map[string]string{
		config.EnvAddr:              "127.0.0.1:0",
		config.EnvDataDir:           dataDir,
		config.EnvUsersFile:         usersFile,
		config.EnvMasterKeyProvider: "file",
		config.EnvLogLevel:          "info",
	}

	var stdout bytes.Buffer
	stderr := &safeBuffer{}

	code := cli.Run(context.Background(), []string{"tabby-sync", "serve"}, fakeEnv(env), &stdout, stderr)
	if code != 1 {
		t.Errorf("exit code = %d; want 1", code)
	}

	logs := stderr.String()
	if !strings.Contains(logs, `"msg":"failed to load master key"`) {
		t.Errorf("logs missing 'failed to load master key' message:\n%s", logs)
	}
	if !strings.Contains(logs, `"provider":"file"`) {
		t.Errorf("logs missing provider=file field:\n%s", logs)
	}

	// v1 review issue #6 for #10: tighten the no-leak assertion. The
	// previous shape softened the redaction check with t.Logf, which
	// meant a regression that started leaking the data-dir path or
	// the seeded key bytes (without also leaking masterKeyPath
	// verbatim) would silently slip through. The contract now is:
	// path-absence (authoritative) AND no other suspicious substring,
	// where "<redacted>" presence is asserted only when the wrapped
	// error actually had something to redact.
	leakedSubstrings := map[string]string{
		"master.key path":         masterKeyPath,
		"data-dir path":           dataDir,
		"users-file path":         usersFile,
		"users-file parent dir":   usersDir,
		"seeded master-key (hex)": hex.EncodeToString(seededKey),
		"master.key basename":     filepath.Base(masterKeyPath),
		"users.yml basename":      filepath.Base(usersFile),
	}
	for label, needle := range leakedSubstrings {
		// filepath.Base of "master.key" / "users.yml" are common
		// dictionary words on disk but the captured logs are JSON
		// and a regression that leaked the basename would surface
		// as a quoted string field; the substring search is good
		// enough to catch that without false positives because the
		// happy-path log fields use "<set>" / "<redacted>" /
		// "<unset>" placeholders, never the bare basename.
		if strings.Contains(logs, needle) {
			t.Errorf("logs leaked %s (%q):\n%s", label, needle, logs)
		}
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
	// Defence-in-depth against v2 review issue #3 for #6: even the
	// basename of the DB file must not survive the scrub. dataDir is a
	// strict prefix of dbPath, so a future ordering regression in
	// scrubPaths (substituting the shorter prefix first) would leave
	// "<redacted>/tabby-sync.db" in the output and pass the two
	// substring checks above. Asserting the basename's absence pins the
	// longest-first contract from the test side.
	if strings.Contains(logs, "tabby-sync.db") {
		t.Errorf("logs leaked DB file basename:\n%s", logs)
	}
	if !strings.Contains(logs, "<redacted>") {
		t.Errorf("logs missing <redacted> scrub marker:\n%s", logs)
	}
}

// TestRunServeUsersFileMissingExitsAndScrubsPath is the auth-side
// analogue of TestRunServeStoreOpenFailureRedactsPath: it points
// TABBY_SYNC_USERS_FILE at a never-created path so auth.LoadUsersFile
// fails, and asserts that the logged error message never echoes the
// configured users-file path. The auth loader strips the wrapped
// *fs.PathError before returning so the path-absence guarantee holds
// even if cli's scrubPaths pass were ever dropped, but the cli code
// path also feeds cfg.UsersFile to scrubPaths as defence-in-depth; a
// regression that bypassed BOTH guards would surface here.
func TestRunServeUsersFileMissingExitsAndScrubsPath(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	usersFile := filepath.Join(tmp, "deeply", "nested", "never-created.yml")

	env := map[string]string{
		config.EnvAddr:              "127.0.0.1:0",
		config.EnvDataDir:           t.TempDir(),
		config.EnvUsersFile:         usersFile,
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
	if !strings.Contains(logs, "failed to load users file") {
		t.Errorf("logs missing users-load failure marker:\n%s", logs)
	}
	if strings.Contains(logs, usersFile) {
		t.Errorf("logs leaked TABBY_SYNC_USERS_FILE (%q):\n%s", usersFile, logs)
	}
	// The redactPath() field on the same log line emits the "<set>"
	// marker; asserting its presence pins the structured-field
	// contract from the test side. (The "<redacted>" scrub marker is
	// not asserted here because the loader already strips the path
	// from the underlying error before scrubPaths sees it; both
	// guards remain in place so a regression that re-introduced the
	// path would still be caught by the substring check above.)
	if !strings.Contains(logs, `"users_file":"<set>"`) {
		t.Errorf("logs missing <set> redaction marker on users_file field:\n%s", logs)
	}
}
