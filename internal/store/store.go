// Package store defines the persistence contract for tabby-sync.
//
// Storage is keyed by user. Every read, mutation and delete is scoped to a
// caller-supplied userID; an implementation MUST treat any row whose stored
// user_id does not match the caller's userID as if it did not exist and
// return [ErrConfigNotFound] from Get/Update/Delete in that case. This
// "cross-user lookups look like not-found" rule is the core isolation
// guarantee the store layer offers to higher layers.
//
// Implementations live in subpackages (for example internal/store/sqlite).
// The interface in this file is the only thing the rest of the codebase
// depends on, so swapping the backing store does not require touching
// callers.
//
// Per docs/LOGGING_POLICY.md and AGENTS.md §7, implementations MUST NOT
// log row contents (in particular ContentCiphertext and ContentNonce),
// raw user input, or anything else that could leak secrets. Errors should
// be wrapped with %w so callers can use errors.Is against the sentinel
// values defined here.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrConfigNotFound is returned by [Store] methods when the requested
// configuration row does not exist for the given user. Cross-user access
// (a Get/Update/Delete whose userID does not match the row's stored
// user_id) MUST also return this error so callers cannot probe the
// presence of other users' rows.
var ErrConfigNotFound = errors.New("store: config not found")

// ErrInvalidPatch is returned by [Store.UpdateConfig] when the supplied
// [UpdateConfigPatch] is internally inconsistent. In particular, the
// ciphertext and nonce must be set together: if either is non-nil and
// non-empty, both must be non-nil and non-empty.
var ErrInvalidPatch = errors.New("store: invalid update patch")

// Config is a single tabby-sync configuration row owned by a user.
//
// ContentCiphertext and ContentNonce are stored as raw bytes; they are
// produced by the crypto layer and are opaque to the store. Implementations
// MUST NOT log them or include them in error messages.
//
// LastUsedWithVersion is allowed to be the empty string, which represents
// "no value recorded yet". Implementations map an empty string to/from a
// SQL NULL on disk so the column type can stay nullable.
//
// CreatedAt and ModifiedAt are stored as RFC3339Nano strings on disk and
// surfaced as time.Time values on the Go side; the implementation owns
// the parsing and formatting and returns wrapped errors on parse failure.
type Config struct {
	ID                  int64
	UserID              int64
	Name                string
	ContentCiphertext   []byte
	ContentNonce        []byte
	LastUsedWithVersion string
	CreatedAt           time.Time
	ModifiedAt          time.Time
}

// CreateConfigInput is the payload accepted by [Store.CreateConfig].
//
// ContentCiphertext and ContentNonce MUST both be non-nil and non-empty.
// LastUsedWithVersion may be empty, in which case the row stores SQL NULL.
//
// Name is treated as opaque by the store layer: any non-NULL string,
// including the empty string, is accepted by the schema and by the
// SQLite implementation. Validation of Name (length limits, character
// restrictions, uniqueness) is the responsibility of the upcoming API
// layer (issue #8) where the user-facing error model lives, not the
// store. This boundary is called out here in response to v1 semantic
// review issue #5 for #6 so future readers do not mistake the lack of
// a store-layer guard for an oversight.
type CreateConfigInput struct {
	Name                string
	ContentCiphertext   []byte
	ContentNonce        []byte
	LastUsedWithVersion string
}

// UpdateConfigPatch describes a partial update to a configuration row.
//
// A nil pointer or a nil/empty byte slice means "do not change this
// field". The ciphertext and nonce must be supplied together: if either
// of ContentCiphertext or ContentNonce is non-nil and non-empty, the
// other must also be non-nil and non-empty, otherwise [Store.UpdateConfig]
// returns [ErrInvalidPatch].
//
// LastUsedWithVersion intentionally collapses the empty string and
// SQL NULL into a single state: passing a non-nil pointer to "" on an
// update writes SQL NULL on disk, and a NULL row reads back as the
// empty Go string. Callers that need to clear the field should pass
// a non-nil pointer to "". v1 semantic review issue #4 for #6
// flagged this as a one-way mapping; the store contract pins it as
// intentional so the upcoming API layer (issue #8) does not need to
// invent a tri-state for a field whose only consumer treats "" as the
// "no version recorded" signal anyway.
type UpdateConfigPatch struct {
	Name                *string
	ContentCiphertext   []byte
	ContentNonce        []byte
	LastUsedWithVersion *string
}

// ErrQuotaExceeded is returned by higher layers when a user has reached
// the maximum number of allowed configurations. Currently the API layer
// checks the quota and returns HTTP 409 directly; this sentinel is
// retained for future use by middleware or store-layer enforcement via
// errors.Is.
var ErrQuotaExceeded = errors.New("store: config quota exceeded")

// ErrContentTooLarge is returned when a config content payload exceeds
// the maximum allowed size (2 MB). Currently the API layer validates
// content size and returns HTTP 413 directly; this sentinel is retained
// for future use by middleware or store-layer enforcement via errors.Is.
var ErrContentTooLarge = errors.New("store: content too large")

// Store is the persistence contract every backend must satisfy.
//
// Every method that takes a userID scopes its work to that user. In
// particular Get/Update/Delete MUST return [ErrConfigNotFound] when the
// addressed row exists but belongs to a different user, so callers cannot
// distinguish "row does not exist" from "row exists but is not yours".
//
// Implementations MUST NOT log row contents, raw user input, or anything
// derived from them.
type Store interface {
	// CreateConfig inserts a new configuration row for userID and
	// returns the persisted [Config], including its assigned ID and
	// the implementation-chosen CreatedAt/ModifiedAt timestamps.
	//
	// Implementations MUST validate that ContentCiphertext and
	// ContentNonce are both non-empty.
	CreateConfig(ctx context.Context, userID int64, in CreateConfigInput) (Config, error)

	// GetConfig returns the configuration row identified by configID
	// IFF it is owned by userID. Cross-user access (configID exists
	// but belongs to a different user) MUST return [ErrConfigNotFound]
	// rather than the actual row.
	GetConfig(ctx context.Context, userID, configID int64) (Config, error)

	// ListConfigsByUser returns every configuration row owned by
	// userID, ordered deterministically (implementations use ascending
	// ID). It returns an empty slice and a nil error when the user has
	// no rows.
	ListConfigsByUser(ctx context.Context, userID int64) ([]Config, error)

	// UpdateConfig applies the non-nil/non-empty fields of patch to
	// the configuration row identified by configID, IFF that row is
	// owned by userID. Cross-user access MUST return
	// [ErrConfigNotFound]. A patch where exactly one of
	// ContentCiphertext / ContentNonce is set MUST return
	// [ErrInvalidPatch] without mutating the row. Implementations
	// always bump ModifiedAt to "now" on a successful update and
	// return the freshly-loaded row.
	UpdateConfig(ctx context.Context, userID, configID int64, patch UpdateConfigPatch) (Config, error)

	// DeleteConfig removes the configuration row identified by
	// configID IFF it is owned by userID. Cross-user access and
	// missing rows both MUST return [ErrConfigNotFound].
	DeleteConfig(ctx context.Context, userID, configID int64) error

	// CountConfigsByUser returns the number of configuration rows owned
	// by userID. It is used for quota enforcement before creating a new
	// config.
	CountConfigsByUser(ctx context.Context, userID int64) (int, error)

	// Close releases any resources held by the store (database
	// connections, file handles, etc.). It is safe to call Close more
	// than once; implementations should make subsequent calls no-ops
	// or return the same terminal error.
	Close() error
}

// CreateConfigPlaintextInput is the payload accepted by
// [EncryptedStore.CreateConfigPlaintext]. Unlike [CreateConfigInput],
// callers pass the configuration content as PLAINTEXT bytes; the
// encrypted-store wrapper encrypts them before they reach the
// underlying [Store].
//
// Content may be empty (resulting in a row whose ciphertext is just
// the GCM authentication tag); LastUsedWithVersion may be empty,
// which the underlying [Store] persists as SQL NULL.
type CreateConfigPlaintextInput struct {
	Name                string
	Content             []byte
	LastUsedWithVersion string
}

// UpdateConfigPlaintextPatch describes a partial update to a
// configuration row at the plaintext layer.
//
// Every field is a pointer so the wrapper can distinguish "field
// absent in the patch" (nil pointer, do not change) from "field
// supplied with an empty value" (non-nil pointer to an empty
// string / empty byte slice, set to empty). When Content is
// non-nil the encrypted-store wrapper re-encrypts the supplied
// plaintext under the same (userID, configID) AAD as the original
// write, generating a fresh random nonce, and forwards both the
// ciphertext and the nonce to the underlying [Store.UpdateConfig];
// in particular Content = &[]byte{} re-encrypts the empty
// plaintext (the resulting ciphertext is just the GCM auth tag)
// instead of being a silent no-op. Addresses v1 semantic review
// issue #1 for #8 + #9.
type UpdateConfigPlaintextPatch struct {
	Name                *string
	Content             *[]byte
	LastUsedWithVersion *string
}

// ConfigWithPlaintext mirrors [Config] but exposes the decrypted
// configuration content directly, replacing the opaque
// ContentCiphertext / ContentNonce pair surfaced by the underlying
// [Store]. The encrypted-store wrapper produces this type from a
// successful Decrypt; callers above the wrapper never see the
// ciphertext or the nonce.
//
// Implementations MUST NOT log the Content bytes (per
// docs/LOGGING_POLICY.md and AGENTS.md §7).
type ConfigWithPlaintext struct {
	ID                  int64
	UserID              int64
	Name                string
	Content             []byte
	LastUsedWithVersion string
	CreatedAt           time.Time
	ModifiedAt          time.Time
}

// EncryptedStore is the plaintext-shaped view of a [Store] that runs
// the AES-256-GCM envelope on the way in and out.
//
// The wrapper-supplied implementation encrypts callers' plaintext
// before forwarding it to the underlying [Store] and decrypts every
// row it reads back. Decryption failures (wrong master key, tampered
// row, cross-user/cross-config replays) surface as the bare
// crypto.ErrDecrypt sentinel returned UNWRAPPED so callers can use
// errors.Is(err, crypto.ErrDecrypt). The store package intentionally
// does NOT import internal/crypto; the contract is documented here
// instead so that the encrypted-store wrapper is the single seam
// between the persistence layer and the cryptographic envelope.
//
// Implementations MUST NOT log row contents (plaintext, ciphertext,
// or nonces) and MUST NOT include them in error messages.
type EncryptedStore interface {
	// CreateConfigPlaintext encrypts in.Content under the
	// (masterKey, userID, assignedConfigID) tuple and inserts the
	// resulting ciphertext+nonce pair via the underlying [Store],
	// returning the persisted row with its plaintext re-attached.
	// The wrapper assigns the configID by writing once with a
	// placeholder AAD, reading the row back, and updating it in
	// place with the canonical AAD; if that second write fails the
	// wrapper deletes the orphaned row to avoid leaving an
	// undecryptable record on disk.
	CreateConfigPlaintext(ctx context.Context, userID int64, in CreateConfigPlaintextInput) (ConfigWithPlaintext, error)

	// GetConfigPlaintext returns the row identified by configID IFF
	// it is owned by userID, decrypted under the configured master
	// key. Cross-user access (the row exists but belongs to another
	// user) returns [ErrConfigNotFound]; a row that exists but
	// cannot be opened under the supplied (userID, configID) AAD
	// returns crypto.ErrDecrypt unwrapped.
	GetConfigPlaintext(ctx context.Context, userID, configID int64) (ConfigWithPlaintext, error)

	// ListConfigsByUserPlaintext returns every row owned by userID
	// in ascending ID order, decrypted under the configured master
	// key. The first decrypt failure aborts the iteration with
	// crypto.ErrDecrypt unwrapped; the returned slice up to that
	// point is discarded.
	ListConfigsByUserPlaintext(ctx context.Context, userID int64) ([]ConfigWithPlaintext, error)

	// UpdateConfigPlaintext applies the non-nil/non-empty fields of
	// patch to the row identified by configID, IFF that row is
	// owned by userID. When patch.Content is non-nil the wrapper
	// re-encrypts under the existing (userID, configID) AAD with a
	// fresh random nonce; in particular Content = &[]byte{} stores
	// the empty plaintext and is NOT treated as a no-op. Cross-user
	// access returns [ErrConfigNotFound]; a successful update
	// returns the freshly-loaded row with its plaintext re-attached.
	UpdateConfigPlaintext(ctx context.Context, userID, configID int64, patch UpdateConfigPlaintextPatch) (ConfigWithPlaintext, error)

	// DeleteConfig removes the row identified by configID IFF it is
	// owned by userID. The contract mirrors [Store.DeleteConfig];
	// no encryption is involved.
	DeleteConfig(ctx context.Context, userID, configID int64) error

	// CountConfigsByUser returns the number of configuration rows
	// owned by userID. Used for quota enforcement.
	CountConfigsByUser(ctx context.Context, userID int64) (int, error)

	// Close releases the underlying [Store] (and any wrapper-owned
	// resources). It is safe to call more than once.
	Close() error
}
