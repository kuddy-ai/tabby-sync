// Package auth implements tabby-sync's Bearer-token authentication.
//
// The package owns the on-disk users.yml schema, a strict YAML loader,
// an in-memory [UserStore] with constant-time SHA-256 token lookup, and
// the HTTP [Bearer] middleware that consumes the store. The companion
// [Middleware] type alias and the no-op [None] constructor in doc.go
// remain in place for callers that wire the auth slot of the middleware
// chain before a real authenticator is available (notably tests and the
// /healthz bypass).
//
// Reload contract. [UserStore.Reload] swaps the live snapshot
// atomically and is safe to call concurrently with [UserStore.Lookup].
// The hook is API-only as of issue #7: cli.runServe does NOT wire it
// to SIGHUP, an admin endpoint, or a file watcher, so a running
// server is stuck on whatever snapshot it loaded at startup and an
// operator must restart the process to pick up users.yml edits. A
// follow-up issue will introduce a runtime trigger.
//
// Hash choice rationale. Tokens stored in users.yml are server-issued
// opaque secrets with at least 128 bits of entropy; they are not
// user-chosen passwords. The threat model that motivates bcrypt or
// argon2 (offline brute force of low-entropy human passwords) does not
// apply, and the per-request cost of those KDFs would dominate every
// authenticated call. SHA-256 plus crypto/subtle.ConstantTimeCompare
// is the appropriate construction for fixed-cost lookup of high-entropy
// secrets, and AGENTS.md §2 requires human confirmation for crypto
// changes; the orchestrator brief for issue #7 is that confirmation.
package auth

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrUnauthorized is returned by [UserStore.Lookup] and surfaced by the
// HTTP middleware when a request cannot be associated with a known,
// enabled user. The sentinel is intentionally generic: callers MUST NOT
// distinguish "no such token" from "disabled user" from "wrong scheme"
// in any response or log line.
var ErrUnauthorized = errors.New("auth: unauthorized")

// User is the public, immutable view of a user record loaded from
// users.yml. Callers receive value-typed copies from the store so a
// downstream handler cannot mutate the live snapshot.
type User struct {
	// ID is the stable, positive, file-assigned identifier for the
	// user. It comes from the explicit "id" YAML field; it is NOT
	// derived from array position.
	ID int64
	// Name is the display name (also used as a unique key during
	// validation). It is logged on the success path of the auth
	// middleware to attribute requests; it MUST NOT be a secret.
	Name string
	// TokenPrefix is a short, human-readable fragment of the token
	// (typically the first ~8 characters of a tbs_-prefixed token)
	// used by operators to identify a credential without revealing
	// it. It is NEVER logged on the failure path.
	TokenPrefix string
	// TokenHash is the lowercase hex-encoded SHA-256 of the token
	// plaintext bytes. It is NEVER logged.
	TokenHash string
	// Disabled gates whether the user can authenticate. A disabled
	// user always returns ErrUnauthorized from Lookup, even when the
	// supplied token hashes to TokenHash.
	Disabled bool
}

// usersFile is the on-disk YAML schema. It is unexported because callers
// should reach the parsed data via [LoadUsersFile] / [UserStore].
type usersFile struct {
	Users []userEntry `yaml:"users"`
}

// userEntry is the per-user YAML schema. Field tags are the contract
// with operators editing users.yml; keep docs/users.yml.example in sync.
type userEntry struct {
	ID          int64  `yaml:"id"`
	Name        string `yaml:"name"`
	TokenPrefix string `yaml:"token_prefix"`
	TokenHash   string `yaml:"token_hash"`
	Disabled    bool   `yaml:"disabled"`
}

// hashPattern matches a lowercase 64-character hex string. The loader
// normalises the on-disk value with [strings.ToLower] before matching
// so a mixed-case hash is accepted but stored canonically lowercase.
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LoadUsersFile reads path, decodes it as a strict YAML users file, and
// returns a *UserStore populated with the parsed entries. The decoder
// runs with KnownFields(true) so any unknown YAML field surfaces as a
// non-nil error.
//
// Error messages are deliberately terse: they name the offending field
// (id, name, token_prefix, token_hash) but never echo the on-disk path,
// the offending hash value, or any token plaintext. Callers that want
// path scrubbing on top (cli.runServe) further substitute the configured
// users-file path out of the error string before logging.
func LoadUsersFile(path string) (*UserStore, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path comes from the env-validated config; loader does not echo it.
	if err != nil {
		// os.ReadFile returns a *fs.PathError whose Error() echoes the
		// path verbatim. We strip the path out by wrapping only the
		// underlying syscall error so the message LoadUsersFile emits
		// can be logged as-is without leaking the on-disk users-file
		// location. errors.Is against the original is still preserved
		// because we wrap with %w.
		var perr *fs.PathError
		if errors.As(err, &perr) {
			return nil, fmt.Errorf("auth: read users file: %w", perr.Err)
		}
		return nil, fmt.Errorf("auth: read users file: %w", err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var uf usersFile
	if err := dec.Decode(&uf); err != nil {
		return nil, fmt.Errorf("auth: decode users file: %w", err)
	}

	users, err := validateAndNormalise(uf.Users)
	if err != nil {
		return nil, err
	}

	return newUserStore(users), nil
}

// validateAndNormalise enforces every users.yml invariant the loader is
// responsible for. It returns a slice of validated [User] values in the
// order they appeared on disk so iteration order is stable in tests.
//
// Errors mention only the offending field name (id, name, token_prefix,
// token_hash); they MUST NOT include the on-disk file path or the
// offending hash value, because the resulting message is logged as-is
// upstream (after path scrubbing) and operators need to be able to find
// the bad record without leaking the credential material into stderr.
func validateAndNormalise(entries []userEntry) ([]User, error) {
	users := make([]User, 0, len(entries))
	seenID := make(map[int64]struct{}, len(entries))
	seenName := make(map[string]struct{}, len(entries))

	for i, e := range entries {
		if e.ID < 1 {
			return nil, fmt.Errorf("auth: users[%d]: id must be a positive integer", i)
		}
		if _, dup := seenID[e.ID]; dup {
			return nil, fmt.Errorf("auth: users[%d]: id is duplicated", i)
		}
		seenID[e.ID] = struct{}{}

		name := strings.TrimSpace(e.Name)
		if name == "" {
			return nil, fmt.Errorf("auth: users[%d]: name must not be empty", i)
		}
		if _, dup := seenName[name]; dup {
			return nil, fmt.Errorf("auth: users[%d]: name is duplicated", i)
		}
		seenName[name] = struct{}{}

		prefix := strings.TrimSpace(e.TokenPrefix)
		if prefix == "" {
			return nil, fmt.Errorf("auth: users[%d]: token_prefix must not be empty", i)
		}

		hash := strings.ToLower(strings.TrimSpace(e.TokenHash))
		if !hashPattern.MatchString(hash) {
			// Do NOT echo the offending hash value into the error.
			return nil, fmt.Errorf("auth: users[%d]: token_hash must be 64 lowercase hex characters", i)
		}

		users = append(users, User{
			ID:          e.ID,
			Name:        name,
			TokenPrefix: prefix,
			TokenHash:   hash,
			Disabled:    e.Disabled,
		})
	}

	return users, nil
}
