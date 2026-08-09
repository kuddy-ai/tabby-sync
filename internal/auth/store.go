package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"sync/atomic"
)

// UserStore is the in-memory snapshot of users.yml consumed by the
// Bearer middleware. The snapshot is held behind an atomic.Pointer so
// [Reload] can swap in a freshly loaded set of users without blocking
// in-flight Lookups: a Lookup that started before Reload finishes
// continues to observe the previous snapshot until it returns.
type UserStore struct {
	snap atomic.Pointer[userSnapshot]
}

// userSnapshot is an immutable bundle of the parsed user records keyed
// by lowercase token hash plus the same records in file order. Both
// views are populated together so a swap is atomic from a consumer's
// perspective.
type userSnapshot struct {
	byHash  map[string]*User
	ordered []*User
}

// zeroHashHex is the lowercase hex encoding of a 32-byte zero buffer.
// It is the constant-time-comparison sentinel used on the miss path of
// [UserStore.Lookup]: when a request's token hash is not in the map we
// still run subtle.ConstantTimeCompare against zeroHashHex so the
// wall-clock cost of a wrong-token call is comparable to that of a
// right-token call. The sentinel is a package-level value so a future
// reviewer can point at the exact bytes the miss path is comparing
// against.
var zeroHashHex = func() string {
	var z [sha256.Size]byte
	return hex.EncodeToString(z[:])
}()

// newUserStore wraps a freshly validated slice of [User] values into a
// *UserStore. The caller owns the input slice; newUserStore takes a
// defensive copy so future mutations to the caller's slice cannot reach
// the live snapshot.
func newUserStore(users []User) *UserStore {
	s := &UserStore{}
	s.snap.Store(newUserSnapshot(users))
	return s
}

// newUserSnapshot builds a userSnapshot from a slice the loader has
// already validated. It defensive-copies each user value so the
// snapshot does not alias caller-owned memory and panics only on
// invariants the caller already enforced (duplicate hashes); that
// panic is a programmer-error guard, not a runtime error path.
func newUserSnapshot(users []User) *userSnapshot {
	snap := &userSnapshot{
		byHash:  make(map[string]*User, len(users)),
		ordered: make([]*User, 0, len(users)),
	}
	for i := range users {
		u := users[i] // copy
		key := u.TokenHash
		if _, dup := snap.byHash[key]; dup {
			// Defensive: validateAndNormalise enforces uniqueness of
			// ids and names but two users could conceivably share a
			// token_hash if an operator copy-pasted a record. We
			// reject that here rather than silently overwriting a
			// previous entry, because allowing the overwrite would
			// hide a credential collision behind first-wins ordering.
			panic("auth: duplicate token_hash in users snapshot")
		}
		snap.byHash[key] = &u
		snap.ordered = append(snap.ordered, &u)
	}
	return snap
}

// Lookup returns the [User] whose TokenHash matches sha256(token), or
// [ErrUnauthorized] otherwise. The function performs exactly one
// crypto/subtle.ConstantTimeCompare call on every code path, comparing
// against either the matched user's hash or [zeroHashHex] when no
// candidate exists. This equalises the post-lookup compare cost
// regardless of whether the hashed token was a map hit or a miss.
//
// Note that the byHash map lookup itself is NOT constant-time: a Go
// map does data-dependent hashing and bucket probing, so a map hit and
// a map miss leave a measurable nanosecond-scale timing delta.
// Disabled-user hits also pay one additional struct-field load and
// branch over the unknown-hash path. With ≥128-bit token entropy a
// brute-force search seeded by these deltas is not feasible, but a
// reader who relies on this code path being uniformly constant-time
// across map membership should not. The constant-time discipline this
// function delivers is on the credential compare itself; the rest of
// the lookup is best-effort timing-equivalent.
//
// Disabled users always return ErrUnauthorized even when the supplied
// token hashes correctly.
func (s *UserStore) Lookup(token string) (User, error) {
	snap := s.snap.Load()

	sum := sha256.Sum256([]byte(token))
	hashHex := hex.EncodeToString(sum[:])

	candidate, ok := snap.byHash[hashHex]
	// Always do exactly one constant-time compare. On a miss, compare
	// hashHex to the zero sentinel so the wall-clock cost matches the
	// hit path; the result of that compare is necessarily 0.
	expected := zeroHashHex
	if ok {
		expected = candidate.TokenHash
	}
	matched := subtle.ConstantTimeCompare([]byte(hashHex), []byte(expected)) == 1

	if !ok || !matched {
		return User{}, ErrUnauthorized
	}
	if candidate.Disabled {
		return User{}, ErrUnauthorized
	}
	return *candidate, nil
}

// Reload re-parses path via [LoadUsersFile] and atomically swaps in the
// resulting snapshot. If LoadUsersFile returns an error, Reload returns
// that error unchanged and the existing snapshot is preserved, so a
// failed reload never empties the live store.
//
// Reload is safe to call concurrently with [UserStore.Lookup]: in-flight
// Lookups continue to see the old snapshot through their local pointer
// load until they finish; new Lookups started after the swap see the
// new snapshot.
//
// Reload is not wired to SIGHUP, an admin endpoint, or a file watcher.
// A running server keeps the snapshot loaded at startup, so operators who
// edit users.yml on disk MUST restart the process. Other package callers may
// invoke Reload programmatically.
func (s *UserStore) Reload(path string) error {
	fresh, err := LoadUsersFile(path)
	if err != nil {
		return err
	}
	s.snap.Store(fresh.snap.Load())
	return nil
}

// UserCount returns the number of users in the live snapshot. It is
// the only operational metric exposed by UserStore; the cli layer logs
// it on startup so operators can confirm a non-empty users.yml loaded
// without that log line ever naming a user.
func (s *UserStore) UserCount() int {
	snap := s.snap.Load()
	if snap == nil {
		return 0
	}
	return len(snap.ordered)
}
