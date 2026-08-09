// Package crypto implements the tabby-sync envelope used to encrypt
// configuration content at rest.
//
// Envelope construction. A 32-byte master key is loaded once at process
// start by [github.com/kuddy-ai/tabby-sync/internal/keys] and is the
// only long-lived secret. For every encryption a 32-byte per-user
// subkey is derived via HKDF-SHA256 with salt=nil and the info string
// `tabby-sync/v1/user/{userID}`. The plaintext is sealed with
// AES-256-GCM under that subkey using a freshly generated 12-byte
// nonce from crypto/rand. The Additional Authenticated Data (AAD) is
// the canonical 17-byte triple
//
//	aad[0]    = CryptoVersion (uint8, currently 1)
//	aad[1:9]  = big-endian int64 userID
//	aad[9:17] = big-endian int64 configID
//
// which binds every ciphertext to the (version, user, config) tuple it
// was produced for. Decryption MUST supply the same userID and
// configID; any deviation, a tampered nonce or ciphertext, a wrong
// master key, or a short input all fail closed with [ErrDecrypt]
// returned UNWRAPPED so callers can reach the sentinel via
// [errors.Is] without strings.Contains.
//
// Threat model. The envelope protects content at rest. It does NOT
// protect against a compromised binary or a compromised host: the
// master key is held in process memory while the server runs, and the
// derived subkey is held on the heap for the duration of an
// encrypt/decrypt call. Both helpers run a `defer zero(subkey)` as
// best-effort hygiene so the subkey does not linger longer than
// necessary, but Go's garbage collector and CPU caches make a
// guaranteed scrub impossible from pure Go.
//
// Logging. Per docs/LOGGING_POLICY.md and AGENTS.md §7 this package
// does NOT log. Errors returned by exported helpers MUST NOT include
// plaintext, ciphertext, nonce, master-key bytes, subkey bytes, or any
// hex thereof; tests pin this with negative-substring assertions.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

// CryptoVersion is the current envelope version. It is encoded as the
// first byte of the AAD so a future version bump can re-key without
// an out-of-band schema migration: rows written under v1 stay
// decryptable while v2 rows carry the new prefix.
const CryptoVersion uint8 = 1

// NonceSize is the AES-GCM nonce length in bytes. Go's
// crypto/cipher.NewGCM hardcodes the same value; the constant is
// exported so callers (and tests) do not need to import the cipher
// package just to size a nonce buffer.
const NonceSize = 12

// KeySize is the master-key length in bytes (256 bits, the AES-256
// requirement). The keys package and the encrypted-store wrapper
// validate inputs against this constant.
const KeySize = 32

// AADSize is the canonical Additional Authenticated Data length, in
// bytes, used by [BuildAAD], [Encrypt], and [Decrypt]:
// 1 byte version || 8 bytes userID || 8 bytes configID.
const AADSize = 1 + 8 + 8

// ErrDecrypt is the single sentinel returned by [Decrypt] on every
// failure path: wrong master key, wrong userID/configID, tampered
// ciphertext, tampered nonce, short nonce, or any other condition
// that prevents the envelope from opening. The error is returned
// UNWRAPPED so callers can use [errors.Is] without resorting to
// string matching, and so the upstream HTTP layer can map every
// decrypt failure to one generic response without leaking which
// specific check failed.
var ErrDecrypt = errors.New("crypto: decryption failed")

// hkdfInfo formats the per-user HKDF info string. Centralised here so
// every callsite sees the same v1 layout and a future version bump can
// only happen in one place.
func hkdfInfo(userID int64) string {
	return fmt.Sprintf("tabby-sync/v1/user/%d", userID)
}

// BuildAAD returns the 17-byte canonical AAD for the supplied
// (CryptoVersion, userID, configID) triple. The byte layout is
// pinned by [AADSize] and by the package-level test fixture, and
// MUST NOT be changed without bumping [CryptoVersion].
func BuildAAD(userID, configID int64) []byte {
	aad := make([]byte, AADSize)
	aad[0] = CryptoVersion
	binary.BigEndian.PutUint64(aad[1:9], uint64(userID))    // #nosec G115 -- the canonical AAD deliberately preserves the signed ID bit pattern.
	binary.BigEndian.PutUint64(aad[9:17], uint64(configID)) // #nosec G115 -- the canonical AAD deliberately preserves the signed ID bit pattern.
	return aad
}

// DeriveSubkey returns a fresh 32-byte AES key derived from masterKey
// for the supplied userID via HKDF-SHA256 with salt=nil and info
// `tabby-sync/v1/user/{userID}`. The result is deterministic for a
// given (masterKey, userID) tuple, which is what makes a row
// re-decryptable across server restarts.
//
// DeriveSubkey is exported so tests can pin the derivation contract.
// Production callers should prefer [Encrypt] and [Decrypt], which run
// DeriveSubkey internally and zero the returned subkey on return.
func DeriveSubkey(masterKey []byte, userID int64) ([]byte, error) {
	if len(masterKey) != KeySize {
		return nil, fmt.Errorf("crypto: master key has wrong length")
	}
	r := hkdf.New(sha256.New, masterKey, nil, []byte(hkdfInfo(userID)))
	subkey := make([]byte, KeySize)
	if _, err := io.ReadFull(r, subkey); err != nil {
		// HKDF over SHA-256 supplies up to 255*32 bytes; reading 32
		// is well within bounds, so this branch is defensive only.
		return nil, fmt.Errorf("crypto: derive subkey: %w", err)
	}
	return subkey, nil
}

// Encrypt seals plaintext under a per-user subkey derived from
// masterKey, returning the AES-GCM ciphertext (with the 16-byte tag
// appended, per Go's crypto/cipher convention) and the freshly
// generated 12-byte random nonce. The (userID, configID) pair is
// bound into the AAD so a row's ciphertext cannot be replayed under a
// different identity without failing [Decrypt].
//
// Encrypt validates that masterKey has length [KeySize] and rejects
// other lengths with a generic, non-[ErrDecrypt] error so a misuse
// is distinguishable from a tampered ciphertext. The plaintext slice
// MAY be empty; the resulting ciphertext is then exactly the GCM tag.
//
// The derived 32-byte subkey is wiped via [zero] on return as
// best-effort hygiene; see the package doc for the limits of that
// guarantee.
func Encrypt(masterKey []byte, userID, configID int64, plaintext []byte) (ciphertext, nonce []byte, err error) {
	subkey, err := DeriveSubkey(masterKey, userID)
	if err != nil {
		return nil, nil, err
	}
	defer zero(subkey)

	block, err := aes.NewCipher(subkey)
	if err != nil {
		// aes.NewCipher only fails on an invalid key length; we
		// already enforced KeySize above, so this branch is
		// defensive.
		return nil, nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("crypto: new gcm: %w", err)
	}

	nonce = make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("crypto: random nonce: %w", err)
	}

	aad := BuildAAD(userID, configID)
	ciphertext = gcm.Seal(nil, nonce, plaintext, aad) // #nosec G407 -- nonce is freshly populated from crypto/rand.Reader above.
	return ciphertext, nonce, nil
}

// Decrypt opens a ciphertext produced by [Encrypt] under the same
// (masterKey, userID, configID) tuple. It returns the plaintext on
// success and [ErrDecrypt] on any failure: wrong key, wrong userID,
// wrong configID, tampered ciphertext, tampered nonce, short nonce,
// or any other condition that prevents the GCM Open from succeeding.
//
// The error is returned UNWRAPPED so callers can use
// [errors.Is](err, ErrDecrypt) and so the HTTP layer can map every
// decrypt failure to one generic response without disclosing which
// specific check failed.
func Decrypt(masterKey []byte, userID, configID int64, ciphertext, nonce []byte) (plaintext []byte, err error) {
	if len(nonce) != NonceSize {
		return nil, ErrDecrypt
	}
	subkey, err := DeriveSubkey(masterKey, userID)
	if err != nil {
		// A wrong-length master key is the only DeriveSubkey error
		// path; collapse it into ErrDecrypt so the failure surface
		// remains uniform from the caller's point of view (HTTP
		// 500 mapping today, do-not-distinguish-from-tampering
		// guarantee always).
		return nil, ErrDecrypt
	}
	defer zero(subkey)

	block, err := aes.NewCipher(subkey)
	if err != nil {
		return nil, ErrDecrypt
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrDecrypt
	}

	aad := BuildAAD(userID, configID)
	plaintext, err = gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// zero overwrites b with zero bytes. It is best-effort hygiene for
// the derived subkey: the Go runtime does not guarantee the write is
// not optimised away or that another copy of the slice does not
// already exist on the stack/heap, but the call is cheap and reduces
// the heap-residency window. The package doc spells out the
// limitations.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
