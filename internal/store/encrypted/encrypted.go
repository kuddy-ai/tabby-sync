// Package encrypted is the AES-256-GCM envelope sitting on top of a
// [store.Store]. It satisfies [store.EncryptedStore] by encrypting every
// plaintext byte the caller supplies before it reaches the underlying
// [store.Store], and by decrypting every row read back. The package is
// the single seam between the persistence layer and the cryptographic
// envelope defined by [github.com/kuddy-ai/tabby-sync/internal/crypto];
// the SQLite layer below stays oblivious to the encryption boundary
// (it only sees opaque ciphertext+nonce blobs) and any future API
// layer above only ever sees plaintext bytes.
//
// Logging. Per docs/LOGGING_POLICY.md and AGENTS.md §7 this package
// emits no log records and MUST NOT echo plaintext, ciphertext,
// nonce, or master-key bytes in error strings. Tests pin this
// invariant.
package encrypted

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/kuddy-ai/tabby-sync/internal/crypto"
	"github.com/kuddy-ai/tabby-sync/internal/store"
)

// Compile-time assertion that *Store satisfies the parent package's
// EncryptedStore contract. Drift between the interface and this
// implementation surfaces at build time.
var _ store.EncryptedStore = (*Store)(nil)

// Store is the encrypted-store wrapper. Construct one with [New]; a
// single instance is intended to live for the duration of the process
// and to be shared across goroutines (the underlying [store.Store] and
// the crypto helpers are themselves goroutine-safe).
type Store struct {
	inner     store.Store
	masterKey []byte
}

// New wraps inner with an AES-256-GCM envelope keyed by masterKey.
// masterKey MUST have exactly [crypto.KeySize] bytes; any other length
// is rejected with a descriptive error and no panic. The wrapper
// retains a defensive copy of masterKey for its lifetime, so the
// caller is free to zero its own copy after [New] returns.
func New(inner store.Store, masterKey []byte) (*Store, error) {
	if inner == nil {
		return nil, errors.New("encrypted: nil inner store")
	}
	if len(masterKey) != crypto.KeySize {
		return nil, fmt.Errorf("encrypted: master key has wrong length")
	}
	cp := make([]byte, crypto.KeySize)
	copy(cp, masterKey)
	return &Store{inner: inner, masterKey: cp}, nil
}

// Close releases the underlying [store.Store] and zeroes the wrapper's
// copy of the master key. Subsequent calls return whatever the inner
// store reports for its second close; the master-key wipe runs on
// every call.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	for i := range s.masterKey {
		s.masterKey[i] = 0
	}
	if s.inner == nil {
		return nil
	}
	return s.inner.Close()
}

// CreateConfigPlaintext encrypts in.Content and persists the resulting
// row via the underlying [store.Store].
//
// SQLite assigns the configID via AUTOINCREMENT, so the wrapper cannot
// know the canonical AAD until the row has been inserted. The
// implementation therefore takes a two-step path:
//
//  1. Encrypt the plaintext with a placeholder AAD (configID=0) and
//     insert the row.
//  2. Re-encrypt the same plaintext under the assigned configID and
//     update the row in place.
//
// If step 2 fails synchronously, the wrapper attempts to delete the
// orphaned row so the database does not retain a record that no
// future read could open. A failure to delete is appended to the
// returned error but does not change the primary failure mode.
//
// Known window: a process kill, host crash, or power loss BETWEEN
// step 1 and step 2 leaves the row on disk with the placeholder
// (userID, 0) AAD, which no future read can open. A concurrent
// same-user Get or List that races a Create can also observe the
// placeholder row and surface [crypto.ErrDecrypt] until the UPDATE
// lands; this transient window closes as soon as step 2 returns.
//
// Closing the orphan window in the wrapper alone is not possible
// without either extending the [store.Store] interface with a
// transaction primitive or moving the two-step write into the
// SQLite implementation. The atomic implementation is tracked in issue #71;
// until it lands, docs/CRYPTO.md ("Two-Step Write" / "Power-loss orphan
// window" / "Operator recovery for an orphan row") is the documented escape
// hatch.
func (s *Store) CreateConfigPlaintext(ctx context.Context, userID int64, in store.CreateConfigPlaintextInput) (store.ConfigWithPlaintext, error) {
	// Step 1: encrypt with placeholder configID=0 and insert.
	ct1, nonce1, err := crypto.Encrypt(s.masterKey, userID, 0, in.Content)
	if err != nil {
		return store.ConfigWithPlaintext{}, fmt.Errorf("encrypted: encrypt placeholder: %w", err)
	}
	row, err := s.inner.CreateConfig(ctx, userID, store.CreateConfigInput{
		Name:                in.Name,
		ContentCiphertext:   ct1,
		ContentNonce:        nonce1,
		LastUsedWithVersion: in.LastUsedWithVersion,
	})
	if err != nil {
		return store.ConfigWithPlaintext{}, err
	}

	// Step 2: re-encrypt under the canonical (userID, configID=row.ID)
	// AAD and update the row. If anything in step 2 fails, attempt to
	// delete the orphaned row so the database does not keep an
	// undecryptable record around.
	ct2, nonce2, err := crypto.Encrypt(s.masterKey, userID, row.ID, in.Content)
	if err != nil {
		delErr := s.inner.DeleteConfig(ctx, userID, row.ID)
		if delErr != nil && !errors.Is(delErr, store.ErrConfigNotFound) {
			return store.ConfigWithPlaintext{}, fmt.Errorf("encrypted: encrypt: %w (rollback failed: %v)", err, delErr)
		}
		return store.ConfigWithPlaintext{}, fmt.Errorf("encrypted: encrypt: %w", err)
	}
	updated, err := s.inner.UpdateConfig(ctx, userID, row.ID, store.UpdateConfigPatch{
		ContentCiphertext: ct2,
		ContentNonce:      nonce2,
	})
	if err != nil {
		delErr := s.inner.DeleteConfig(ctx, userID, row.ID)
		if delErr != nil && !errors.Is(delErr, store.ErrConfigNotFound) {
			return store.ConfigWithPlaintext{}, fmt.Errorf("encrypted: finalise insert: %w (rollback failed: %v)", err, delErr)
		}
		return store.ConfigWithPlaintext{}, fmt.Errorf("encrypted: finalise insert: %w", err)
	}

	// Re-attach the plaintext we already have rather than calling
	// Decrypt again on the freshly-written row: we know the
	// plaintext exactly and skipping the round-trip avoids one AES
	// decryption per Create call.
	return store.ConfigWithPlaintext{
		ID:                  updated.ID,
		UserID:              updated.UserID,
		Name:                updated.Name,
		Content:             append([]byte(nil), in.Content...),
		LastUsedWithVersion: updated.LastUsedWithVersion,
		CreatedAt:           updated.CreatedAt,
		ModifiedAt:          updated.ModifiedAt,
	}, nil
}

// GetConfigPlaintext reads the row via the underlying [store.Store]
// and decrypts it under the configured master key. Cross-user access
// surfaces as [store.ErrConfigNotFound]; a row whose ciphertext does
// not open under the supplied (userID, configID) AAD surfaces as
// [crypto.ErrDecrypt] returned UNWRAPPED so callers can use
// [errors.Is].
func (s *Store) GetConfigPlaintext(ctx context.Context, userID, configID int64) (store.ConfigWithPlaintext, error) {
	row, err := s.inner.GetConfig(ctx, userID, configID)
	if err != nil {
		return store.ConfigWithPlaintext{}, err
	}
	pt, err := crypto.Decrypt(s.masterKey, userID, configID, row.ContentCiphertext, row.ContentNonce)
	if err != nil {
		// Return the bare sentinel so errors.Is(err, crypto.ErrDecrypt)
		// works without strings.Contains; this is the contract pinned
		// by the package doc and exercised by encrypted_test.go.
		return store.ConfigWithPlaintext{}, crypto.ErrDecrypt
	}
	return toPlaintext(row, pt), nil
}

// ListConfigsByUserPlaintext returns every row owned by userID in
// ascending ID order, decrypted under the configured master key. The
// first decrypt failure aborts the iteration with [crypto.ErrDecrypt]
// returned UNWRAPPED.
//
// Policy (v1, fail-closed): a tampered, replayed, or orphaned row
// MUST NOT silently disappear from the caller's view. A single
// undecryptable row therefore bricks the whole list path until the
// row is removed; this is intentional. Skip-and-tag and
// structured-failure variants were considered, but the public API intentionally
// keeps the fail-closed default. See docs/CRYPTO.md ("List Failure Policy") for
// the full discussion and the recovery procedure; issue #71 tracks removal of
// the placeholder-row failure window.
func (s *Store) ListConfigsByUserPlaintext(ctx context.Context, userID int64) ([]store.ConfigWithPlaintext, error) {
	rows, err := s.inner.ListConfigsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]store.ConfigWithPlaintext, 0, len(rows))
	for _, row := range rows {
		pt, derr := crypto.Decrypt(s.masterKey, userID, row.ID, row.ContentCiphertext, row.ContentNonce)
		if derr != nil {
			return nil, crypto.ErrDecrypt
		}
		out = append(out, toPlaintext(row, pt))
	}
	return out, nil
}

// UpdateConfigPlaintext applies the non-nil fields of patch to the row
// identified by configID. Name and plaintext content are compared before
// encryption so a semantically identical PATCH preserves the ciphertext,
// nonce and ModifiedAt. A last-used-version-only change is persisted as
// metadata without advancing ModifiedAt.
//
// The no-op path uses an optimistic ModifiedAt check in the underlying
// store. If a real update lands after the comparison, ErrConfigChanged
// causes the wrapper to reload and re-evaluate the caller's patch instead
// of accidentally swallowing that concurrent change. See issue #62.
func (s *Store) UpdateConfigPlaintext(ctx context.Context, userID, configID int64, patch store.UpdateConfigPlaintextPatch) (store.ConfigWithPlaintext, error) {
	for {
		if err := ctx.Err(); err != nil {
			return store.ConfigWithPlaintext{}, err
		}

		current, err := s.GetConfigPlaintext(ctx, userID, configID)
		if err != nil {
			return store.ConfigWithPlaintext{}, err
		}

		nameChanged := patch.Name != nil && *patch.Name != current.Name
		contentChanged := patch.Content != nil && !bytes.Equal(*patch.Content, current.Content)
		if nameChanged || contentChanged {
			innerPatch := store.UpdateConfigPatch{
				LastUsedWithVersion: patch.LastUsedWithVersion,
			}
			if nameChanged {
				innerPatch.Name = patch.Name
			}
			if contentChanged {
				ct, nonce, err := crypto.Encrypt(s.masterKey, userID, configID, *patch.Content)
				if err != nil {
					return store.ConfigWithPlaintext{}, fmt.Errorf("encrypted: encrypt update: %w", err)
				}
				innerPatch.ContentCiphertext = ct
				innerPatch.ContentNonce = nonce
			}
			if _, err := s.inner.UpdateConfig(ctx, userID, configID, innerPatch); err != nil {
				return store.ConfigWithPlaintext{}, err
			}
			return s.GetConfigPlaintext(ctx, userID, configID)
		}

		expected := current.ModifiedAt
		innerPatch := store.UpdateConfigPatch{
			PreserveModifiedAt: true,
			ExpectedModifiedAt: &expected,
		}
		if patch.LastUsedWithVersion != nil && *patch.LastUsedWithVersion != current.LastUsedWithVersion {
			innerPatch.LastUsedWithVersion = patch.LastUsedWithVersion
		}
		if _, err := s.inner.UpdateConfig(ctx, userID, configID, innerPatch); err != nil {
			if errors.Is(err, store.ErrConfigChanged) {
				continue
			}
			return store.ConfigWithPlaintext{}, err
		}
		return s.GetConfigPlaintext(ctx, userID, configID)
	}
}

// DeleteConfig forwards directly to the underlying [store.Store]. No
// encryption is involved.
func (s *Store) DeleteConfig(ctx context.Context, userID, configID int64) error {
	return s.inner.DeleteConfig(ctx, userID, configID)
}

// CountConfigsByUser forwards directly to the underlying [store.Store].
// No encryption is involved.
func (s *Store) CountConfigsByUser(ctx context.Context, userID int64) (int, error) {
	return s.inner.CountConfigsByUser(ctx, userID)
}

// toPlaintext copies the metadata fields from a [store.Config] into a
// [store.ConfigWithPlaintext] and attaches the supplied plaintext. The
// ciphertext / nonce fields are intentionally dropped.
func toPlaintext(row store.Config, pt []byte) store.ConfigWithPlaintext {
	return store.ConfigWithPlaintext{
		ID:                  row.ID,
		UserID:              row.UserID,
		Name:                row.Name,
		Content:             pt,
		LastUsedWithVersion: row.LastUsedWithVersion,
		CreatedAt:           row.CreatedAt,
		ModifiedAt:          row.ModifiedAt,
	}
}
