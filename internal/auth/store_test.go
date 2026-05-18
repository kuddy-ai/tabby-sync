package auth_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

// TestLookupRejectsWrongTokenAtSeveralInputLengths exercises Lookup
// against a handful of input sizes (0, 1, 16, 4096 bytes). Note that
// SHA-256 normalises every input to a fixed-length 64-character hex
// digest before the constant-time compare, so all four cases hit the
// SAME code path: this test is not a length-branch coverage test, it
// is a panic-safety pin for the zero-hash sentinel fallback. A future
// refactor that introduced length-dependent branches would need its
// own coverage test on top.
func TestLookupRejectsWrongTokenAtSeveralInputLengths(t *testing.T) {
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

// TestReloadConcurrentLookupContention pins the v1 review's fix for
// issue #5: prove the atomic.Pointer snapshot swap holds up under
// contention. A pool of goroutines hammers Lookup against a token that
// is valid in fixture A and invalid in fixture B while a separate
// goroutine alternates Reload(A)/Reload(B) for the duration of the
// run. The race detector catches data races; this test catches torn
// snapshots (a Lookup that reads byHash from one snapshot and Disabled
// from another) by asserting Lookup only ever returns the canonical A
// or B user, never an inconsistent record.
//
// The test is intentionally bounded by an iteration count rather than
// a wall-clock deadline so it stays deterministic in CI.
func TestReloadConcurrentLookupContention(t *testing.T) {
	t.Parallel()

	pathA := fixtureA(t)
	pathB := fixtureB(t)

	store, err := auth.LoadUsersFile(pathA)
	if err != nil {
		t.Fatalf("LoadUsersFile A: %v", err)
	}

	const (
		readers      = 16
		swaps        = 200
		readsPerLoop = 50
	)

	var (
		wg       sync.WaitGroup
		stop     atomic.Bool
		seenA    atomic.Int64
		seenB    atomic.Int64
		seenMiss atomic.Int64
	)

	// Reader pool: each goroutine alternates between looking up alice
	// and bob until the swapper signals stop. The accepted outcomes
	// are (i) the canonical A user, (ii) the canonical B user, or
	// (iii) ErrUnauthorized (token not in the current snapshot).
	// Anything else (e.g. an A id paired with a B name) would be a
	// torn read and fails the test.
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				for j := 0; j < readsPerLoop; j++ {
					for _, token := range []string{"alice-token", "bob-token"} {
						u, err := store.Lookup(token)
						switch {
						case errors.Is(err, auth.ErrUnauthorized):
							seenMiss.Add(1)
						case err != nil:
							t.Errorf("Lookup(%q) unexpected error: %v", token, err)
							return
						case u.ID == 1 && u.Name == "alice" && token == "alice-token":
							seenA.Add(1)
						case u.ID == 2 && u.Name == "bob" && token == "bob-token":
							seenB.Add(1)
						default:
							t.Errorf("torn snapshot: token=%q user=%+v", token, u)
							return
						}
					}
				}
			}
		}()
	}

	// Swapper: alternate Reload(A) and Reload(B) a fixed number of
	// times, then stop the readers. The swapper is the timing master;
	// the readers are bounded only by stop.
	for s := 0; s < swaps; s++ {
		path := pathA
		if s%2 == 1 {
			path = pathB
		}
		if err := store.Reload(path); err != nil {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("Reload iteration %d: %v", s, err)
		}
	}
	stop.Store(true)
	wg.Wait()

	// Sanity: at least one of each outcome must have been observed,
	// otherwise the test did not actually exercise contention.
	if seenA.Load() == 0 {
		t.Errorf("contention test never observed alice during Reload(A) windows")
	}
	if seenB.Load() == 0 {
		t.Errorf("contention test never observed bob during Reload(B) windows")
	}
	if seenMiss.Load() == 0 {
		t.Errorf("contention test never observed an ErrUnauthorized miss")
	}
}
