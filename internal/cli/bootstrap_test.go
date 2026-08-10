package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kuddy-ai/tabby-sync/internal/auth"
	"github.com/kuddy-ai/tabby-sync/internal/cli"
	"github.com/kuddy-ai/tabby-sync/internal/config"
)

func TestRunBootstrapUsesServerUnicodeNameSemantics(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	usersFile := filepath.Join(dataDir, "users.yml")
	env := map[string]string{
		config.EnvDataDir:   dataDir,
		config.EnvUsersFile: usersFile,
	}

	var stdout, stderr bytes.Buffer
	code := cli.Run(
		context.Background(),
		[]string{"tabby-sync", "bootstrap", "\u00a0alice\u2003"},
		fakeEnv(env),
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("exit code = %d; want 0; stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); got != "bootstrap credentials created\n" {
		t.Errorf("stdout = %q; want generic success message", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q; want empty", stderr.String())
	}

	tokenPath := filepath.Join(dataDir, "token.txt")
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if !strings.HasPrefix(token, "tbs_") || len(token) != 68 {
		t.Fatalf("generated token shape is invalid")
	}
	if strings.Contains(stdout.String(), token) || strings.Contains(stderr.String(), token) {
		t.Fatal("bootstrap output leaked the plaintext token")
	}

	users, err := auth.LoadUsersFile(usersFile)
	if err != nil {
		t.Fatalf("LoadUsersFile: %v", err)
	}
	user, err := users.Lookup(token)
	if err != nil {
		t.Fatalf("Lookup(generated token): %v", err)
	}
	if user.Name != "alice" {
		t.Errorf("normalised user name = %q; want alice", user.Name)
	}

	if runtime.GOOS != "windows" {
		for _, path := range []string{usersFile, tokenPath} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat bootstrap file: %v", err)
			}
			if got := info.Mode().Perm(); got != 0o600 {
				t.Errorf("bootstrap file mode = %#o; want 0600", got)
			}
		}
	}

	usersBefore, err := os.ReadFile(usersFile)
	if err != nil {
		t.Fatalf("read users before repeat: %v", err)
	}
	tokenBefore := append([]byte(nil), tokenBytes...)
	stdout.Reset()
	stderr.Reset()
	code = cli.Run(
		context.Background(),
		[]string{"tabby-sync", "bootstrap", "different-name"},
		fakeEnv(env),
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("repeat exit code = %d; want 0; stderr=%q", code, stderr.String())
	}
	usersAfter, _ := os.ReadFile(usersFile)
	tokenAfter, _ := os.ReadFile(tokenPath)
	if !bytes.Equal(usersBefore, usersAfter) || !bytes.Equal(tokenBefore, tokenAfter) {
		t.Fatal("repeat bootstrap changed existing credentials")
	}
}

func TestRunBootstrapRejectsUnicodeWhitespaceWithoutFiles(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"\u00a0", "\u2003", " \t\u00a0\u2003\n"} {
		name := name
		t.Run("unicode-whitespace", func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			usersFile := filepath.Join(dataDir, "users.yml")
			env := map[string]string{
				config.EnvDataDir:   dataDir,
				config.EnvUsersFile: usersFile,
			}

			var stdout, stderr bytes.Buffer
			code := cli.Run(
				context.Background(),
				[]string{"tabby-sync", "bootstrap", name},
				fakeEnv(env),
				&stdout,
				&stderr,
			)
			if code != 2 {
				t.Fatalf("exit code = %d; want 2", code)
			}
			if !strings.Contains(stderr.String(), "name must not be empty") {
				t.Errorf("stderr = %q; want empty-name error", stderr.String())
			}
			for _, path := range []string{usersFile, filepath.Join(dataDir, "token.txt")} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("invalid bootstrap left %s behind", filepath.Base(path))
				}
			}
		})
	}
}
