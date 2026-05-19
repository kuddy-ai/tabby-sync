package auth_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuddy-ai/tabby-sync/internal/auth"
)

// hashOf returns the lowercase hex SHA-256 of s, used by every users-file
// fixture in this test file so the live token plaintext stays inline
// next to the hash it produces.
func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// writeUsersFile writes the supplied YAML content into a fresh file in
// t.TempDir() and returns the absolute path. The helper centralises the
// t.Helper / WriteFile / mode boilerplate.
func writeUsersFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "users.yml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("seed users.yml: %v", err)
	}
	return p
}

func TestLoadUsersFileMinimalValid(t *testing.T) {
	t.Parallel()

	body := `users:
  - id: 1
    name: alice
    token_prefix: tbs_test01
    token_hash: ` + hashOf("alice-token") + `
    disabled: false
`
	store, err := auth.LoadUsersFile(writeUsersFile(t, body))
	if err != nil {
		t.Fatalf("LoadUsersFile: %v", err)
	}
	if store == nil {
		t.Fatal("store is nil")
	}
	u, err := store.Lookup("alice-token")
	if err != nil {
		t.Fatalf("Lookup(alice-token): %v", err)
	}
	if u.ID != 1 || u.Name != "alice" || u.TokenPrefix != "tbs_test01" || u.Disabled {
		t.Errorf("unexpected user: %+v", u)
	}
}

func TestLoadUsersFilePreservesExplicitIDs(t *testing.T) {
	// Acceptance criterion: ids come from the explicit "id" field, not
	// from array position. Listing 5/2/9 must round-trip as 5/2/9.
	t.Parallel()

	body := `users:
  - id: 5
    name: alice
    token_prefix: tbs_alice0
    token_hash: ` + hashOf("alice-token") + `
  - id: 2
    name: bob
    token_prefix: tbs_bob000
    token_hash: ` + hashOf("bob-token") + `
  - id: 9
    name: carol
    token_prefix: tbs_carol0
    token_hash: ` + hashOf("carol-token") + `
`
	store, err := auth.LoadUsersFile(writeUsersFile(t, body))
	if err != nil {
		t.Fatalf("LoadUsersFile: %v", err)
	}
	cases := map[string]int64{
		"alice-token": 5,
		"bob-token":   2,
		"carol-token": 9,
	}
	for token, wantID := range cases {
		u, err := store.Lookup(token)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", token, err)
		}
		if u.ID != wantID {
			t.Errorf("Lookup(%q).ID = %d; want %d", token, u.ID, wantID)
		}
	}
}

func TestLoadUsersFileRejectsUnknownField(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"top-level": `users:
  - id: 1
    name: alice
    token_prefix: tbs_test01
    token_hash: ` + hashOf("alice-token") + `
unexpected: 1
`,
		"per-user": `users:
  - id: 1
    name: alice
    token_prefix: tbs_test01
    token_hash: ` + hashOf("alice-token") + `
    extra: foo
`,
	}
	for label, body := range cases {
		body := body
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			path := writeUsersFile(t, body)
			store, err := auth.LoadUsersFile(path)
			if err == nil {
				t.Fatalf("LoadUsersFile returned nil error for unknown field (%s)", label)
			}
			if store != nil {
				t.Errorf("store should be nil on error; got %+v", store)
			}
			if strings.Contains(err.Error(), path) {
				t.Errorf("error leaks file path: %v", err)
			}
		})
	}
}

func TestLoadUsersFileRejectsZeroOrNegativeID(t *testing.T) {
	t.Parallel()

	for _, badID := range []string{"0", "-1"} {
		badID := badID
		t.Run("id="+badID, func(t *testing.T) {
			t.Parallel()
			body := `users:
  - id: ` + badID + `
    name: alice
    token_prefix: tbs_test01
    token_hash: ` + hashOf("alice-token") + `
`
			_, err := auth.LoadUsersFile(writeUsersFile(t, body))
			if err == nil {
				t.Fatalf("LoadUsersFile returned nil for id=%s", badID)
			}
			if !strings.Contains(err.Error(), "id") {
				t.Errorf("error should mention `id`; got %v", err)
			}
		})
	}
}

func TestLoadUsersFileRejectsDuplicateID(t *testing.T) {
	t.Parallel()

	body := `users:
  - id: 1
    name: alice
    token_prefix: tbs_alice0
    token_hash: ` + hashOf("alice-token") + `
  - id: 1
    name: bob
    token_prefix: tbs_bob000
    token_hash: ` + hashOf("bob-token") + `
`
	_, err := auth.LoadUsersFile(writeUsersFile(t, body))
	if err == nil {
		t.Fatal("LoadUsersFile returned nil for duplicate id")
	}
	if !strings.Contains(err.Error(), "id") {
		t.Errorf("error should mention `id`; got %v", err)
	}
}

func TestLoadUsersFileRejectsDuplicateName(t *testing.T) {
	t.Parallel()

	body := `users:
  - id: 1
    name: alice
    token_prefix: tbs_alice0
    token_hash: ` + hashOf("alice-token") + `
  - id: 2
    name: alice
    token_prefix: tbs_alice2
    token_hash: ` + hashOf("alice-token-2") + `
`
	_, err := auth.LoadUsersFile(writeUsersFile(t, body))
	if err == nil {
		t.Fatal("LoadUsersFile returned nil for duplicate name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error should mention `name`; got %v", err)
	}
}

func TestLoadUsersFileRejectsEmptyName(t *testing.T) {
	t.Parallel()

	body := `users:
  - id: 1
    name: ""
    token_prefix: tbs_test01
    token_hash: ` + hashOf("alice-token") + `
`
	_, err := auth.LoadUsersFile(writeUsersFile(t, body))
	if err == nil {
		t.Fatal("LoadUsersFile returned nil for empty name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error should mention `name`; got %v", err)
	}
}

func TestLoadUsersFileRejectsBadHash(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"too-short": strings.Repeat("a", 63),
		"too-long":  strings.Repeat("a", 65),
		"non-hex":   strings.Repeat("z", 64),
	}
	for label, badHash := range cases {
		badHash := badHash
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			body := `users:
  - id: 1
    name: alice
    token_prefix: tbs_test01
    token_hash: ` + badHash + `
`
			_, err := auth.LoadUsersFile(writeUsersFile(t, body))
			if err == nil {
				t.Fatalf("LoadUsersFile returned nil for bad hash (%s)", label)
			}
			if !strings.Contains(err.Error(), "token_hash") {
				t.Errorf("error should mention `token_hash`; got %v", err)
			}
			if strings.Contains(err.Error(), badHash) {
				t.Errorf("error leaks the offending hash value: %v", err)
			}
		})
	}
}

func TestLoadUsersFileRejectsEmptyTokenPrefix(t *testing.T) {
	t.Parallel()

	body := `users:
  - id: 1
    name: alice
    token_prefix: ""
    token_hash: ` + hashOf("alice-token") + `
`
	_, err := auth.LoadUsersFile(writeUsersFile(t, body))
	if err == nil {
		t.Fatal("LoadUsersFile returned nil for empty token_prefix")
	}
	if !strings.Contains(err.Error(), "token_prefix") {
		t.Errorf("error should mention `token_prefix`; got %v", err)
	}
}

func TestLoadUsersFileMalformedYAMLDoesNotPanic(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"binary-garbage": "\x00\x01\x02\xff\xfe\xfd\n",
		"broken-yaml":    "users:\n  - id: 1\n    name: alice\n   token_prefix: oops_indent\n",
	}
	for label, body := range cases {
		body := body
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			path := writeUsersFile(t, body)
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("LoadUsersFile panicked on %s: %v", label, r)
				}
			}()
			_, err := auth.LoadUsersFile(path)
			if err == nil {
				t.Fatalf("LoadUsersFile returned nil error on %s", label)
			}
		})
	}
}

func TestLoadUsersFileMissingFileReturnsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	missing := filepath.Join(dir, "nope-this-does-not-exist.yml")
	store, err := auth.LoadUsersFile(missing)
	if err == nil {
		t.Fatal("LoadUsersFile returned nil error for missing file")
	}
	if store != nil {
		t.Errorf("store should be nil on error; got %+v", store)
	}
	if strings.Contains(err.Error(), missing) {
		t.Errorf("error leaks the missing path: %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v; want a wrapped os.ErrNotExist", err)
	}
}
