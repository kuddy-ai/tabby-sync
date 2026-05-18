package auth_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuddy-ai/tabby-sync/internal/auth"
)

// fixtureA returns the path to a one-user file (alice / "alice-token").
func fixtureA(t *testing.T) string {
	t.Helper()
	body := `users:
  - id: 1
    name: alice
    token_prefix: tbs_alice0
    token_hash: ` + hashOf("alice-token") + `
`
	return writeUsersFile(t, body)
}

// fixtureB returns the path to a one-user file (bob / "bob-token").
func fixtureB(t *testing.T) string {
	t.Helper()
	body := `users:
  - id: 2
    name: bob
    token_prefix: tbs_bob000
    token_hash: ` + hashOf("bob-token") + `
`
	return writeUsersFile(t, body)
}

// fixtureDisabled returns a one-user file whose user is marked disabled.
func fixtureDisabled(t *testing.T) string {
	t.Helper()
	body := `users:
  - id: 3
    name: carol
    token_prefix: tbs_carol0
    token_hash: ` + hashOf("carol-token") + `
    disabled: true
`
	return writeUsersFile(t, body)
}

func TestLookupSuccess(t *testing.T) {
	t.Parallel()

	store, err := auth.LoadUsersFile(fixtureA(t))
	if err != nil {
		t.Fatalf("LoadUsersFile: %v", err)
	}
	u, err := store.Lookup("alice-token")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if u.ID != 1 || u.Name != "alice" {
		t.Errorf("unexpected user: %+v", u)
	}
}

func TestLookupWrongToken(t *testing.T) {
	t.Parallel()

	store, err := auth.LoadUsersFile(fixtureA(t))
	if err != nil {
		t.Fatalf("LoadUsersFile: %v", err)
	}
	_, err = store.Lookup("not-the-right-token")
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("err = %v; want ErrUnauthorized", err)
	}
}

func TestLookupWrongTokenVaryingLengths(t *testing.T) {
	t.Parallel()

	store, err := auth.LoadUsersFile(fixtureA(t))
	if err != nil {
		t.Fatalf("LoadUsersFile: %v", err)
	}
	for _, n := range []int{0, 1, 16, 4096} {
		token := strings.Repeat("x", n)
		// Defence against panics on the zero-hash fallback path.
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Lookup panicked at length %d: %v", n, r)
				}
			}()
			_, err := store.Lookup(token)
			if !errors.Is(err, auth.ErrUnauthorized) {
				t.Errorf("Lookup(len=%d): err = %v; want ErrUnauthorized", n, err)
			}
		}()
	}
}

func TestLookupDisabledUser(t *testing.T) {
	t.Parallel()

	store, err := auth.LoadUsersFile(fixtureDisabled(t))
	if err != nil {
		t.Fatalf("LoadUsersFile: %v", err)
	}
	_, err = store.Lookup("carol-token")
	if !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("err = %v; want ErrUnauthorized", err)
	}
}

func TestReloadAtomicSwap(t *testing.T) {
	t.Parallel()

	pathA := fixtureA(t)
	pathB := fixtureB(t)

	store, err := auth.LoadUsersFile(pathA)
	if err != nil {
		t.Fatalf("LoadUsersFile A: %v", err)
	}
	if _, err := store.Lookup("alice-token"); err != nil {
		t.Fatalf("pre-reload Lookup(alice): %v", err)
	}

	if err := store.Reload(pathB); err != nil {
		t.Fatalf("Reload(B): %v", err)
	}

	if _, err := store.Lookup("alice-token"); !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("post-reload Lookup(alice) err = %v; want ErrUnauthorized", err)
	}
	u, err := store.Lookup("bob-token")
	if err != nil {
		t.Fatalf("post-reload Lookup(bob): %v", err)
	}
	if u.Name != "bob" || u.ID != 2 {
		t.Errorf("post-reload Lookup(bob) returned %+v", u)
	}
}

func TestReloadFailureKeepsOldSnapshot(t *testing.T) {
	t.Parallel()

	store, err := auth.LoadUsersFile(fixtureA(t))
	if err != nil {
		t.Fatalf("LoadUsersFile: %v", err)
	}

	// Point at a path that does not exist.
	missing := filepath.Join(t.TempDir(), "nope.yml")
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("test setup expected missing path; got %v", err)
	}
	if err := store.Reload(missing); err == nil {
		t.Fatal("Reload(missing) returned nil error")
	}

	// The prior snapshot must still be intact.
	u, err := store.Lookup("alice-token")
	if err != nil {
		t.Fatalf("post-failed-reload Lookup(alice): %v", err)
	}
	if u.Name != "alice" || u.ID != 1 {
		t.Errorf("snapshot mutated after failed reload: %+v", u)
	}
}
