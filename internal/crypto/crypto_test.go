package crypto_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/kuddy-ai/tabby-sync/internal/crypto"
)

// fixedKey is a deterministic 32-byte master key used by every
// round-trip test in this file. It is NOT a real key; the value is
// chosen so a leak into an error string or log line would produce a
// recognisable hex run.
func fixedKey() []byte {
	return bytes.Repeat([]byte{0xAB}, crypto.KeySize)
}

// fixedKeyHex is the hex form of fixedKey, used by negative-control
// substring assertions that pin the no-leak contract.
const fixedKeyHex = "abababababababababababababababababababababababababababababababab"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	t.Parallel()

	key := fixedKey()
	plaintext := []byte("PLAINTEXT_SENTINEL_DO_NOT_LEAK_v1")

	ct, nonce, err := crypto.Encrypt(key, 7, 13, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(nonce) != crypto.NonceSize {
		t.Fatalf("nonce len = %d, want %d", len(nonce), crypto.NonceSize)
	}
	got, err := crypto.Decrypt(key, 7, 13, ct, nonce)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, plaintext)
	}
}

func TestEncryptIsRandomised(t *testing.T) {
	t.Parallel()

	key := fixedKey()
	plaintext := []byte("same plaintext every time")

	const N = 8
	ciphertexts := make([][]byte, 0, N)
	nonces := make([][]byte, 0, N)
	for i := 0; i < N; i++ {
		ct, nonce, err := crypto.Encrypt(key, 1, 1, plaintext)
		if err != nil {
			t.Fatalf("Encrypt[%d]: %v", i, err)
		}
		ciphertexts = append(ciphertexts, ct)
		nonces = append(nonces, nonce)
	}
	for i := 0; i < N; i++ {
		for j := i + 1; j < N; j++ {
			if bytes.Equal(nonces[i], nonces[j]) {
				t.Fatalf("nonce reuse between iterations %d and %d", i, j)
			}
			if bytes.Equal(ciphertexts[i], ciphertexts[j]) {
				t.Fatalf("ciphertext reuse between iterations %d and %d", i, j)
			}
		}
	}
}

func TestDecryptWrongUserIDFailsClosed(t *testing.T) {
	t.Parallel()

	key := fixedKey()
	ct, nonce, err := crypto.Encrypt(key, 7, 13, []byte("hello"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	_, err = crypto.Decrypt(key, 8, 13, ct, nonce)
	if !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("err = %v, want errors.Is(ErrDecrypt)", err)
	}
}

func TestDecryptWrongConfigIDFailsClosed(t *testing.T) {
	t.Parallel()

	key := fixedKey()
	ct, nonce, err := crypto.Encrypt(key, 7, 13, []byte("hello"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	_, err = crypto.Decrypt(key, 7, 14, ct, nonce)
	if !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("err = %v, want errors.Is(ErrDecrypt)", err)
	}
}

func TestDecryptTamperedCiphertextFailsClosed(t *testing.T) {
	t.Parallel()

	key := fixedKey()
	ct, nonce, err := crypto.Encrypt(key, 7, 13, []byte("tabby tabby tabby"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	tampered := append([]byte(nil), ct...)
	tampered[0] ^= 0x01
	_, err = crypto.Decrypt(key, 7, 13, tampered, nonce)
	if !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("err = %v, want errors.Is(ErrDecrypt)", err)
	}
}

func TestDecryptTamperedNonceFailsClosed(t *testing.T) {
	t.Parallel()

	key := fixedKey()
	ct, nonce, err := crypto.Encrypt(key, 7, 13, []byte("nonce check"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	tampered := append([]byte(nil), nonce...)
	tampered[0] ^= 0x01
	_, err = crypto.Decrypt(key, 7, 13, ct, tampered)
	if !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("err = %v, want errors.Is(ErrDecrypt)", err)
	}
}

func TestDecryptWrongMasterKeyFailsClosed(t *testing.T) {
	t.Parallel()

	good := fixedKey()
	bad := bytes.Repeat([]byte{0xCD}, crypto.KeySize)
	ct, nonce, err := crypto.Encrypt(good, 7, 13, []byte("alpha"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	_, err = crypto.Decrypt(bad, 7, 13, ct, nonce)
	if !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("err = %v, want errors.Is(ErrDecrypt)", err)
	}
}

func TestEncryptDecryptEmptyPlaintext(t *testing.T) {
	t.Parallel()

	key := fixedKey()
	ct, nonce, err := crypto.Encrypt(key, 1, 1, nil)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// AES-GCM tag is 16 bytes; an empty plaintext yields exactly the tag.
	if len(ct) != 16 {
		t.Fatalf("ciphertext len = %d, want 16 (GCM tag only)", len(ct))
	}
	got, err := crypto.Decrypt(key, 1, 1, ct, nonce)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("decrypted len = %d, want 0", len(got))
	}
}

func TestDeriveSubkeyDeterministic(t *testing.T) {
	t.Parallel()

	key := fixedKey()
	a, err := crypto.DeriveSubkey(key, 42)
	if err != nil {
		t.Fatalf("DeriveSubkey #1: %v", err)
	}
	b, err := crypto.DeriveSubkey(key, 42)
	if err != nil {
		t.Fatalf("DeriveSubkey #2: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("subkey is not deterministic for a fixed (key, userID)")
	}
	if len(a) != crypto.KeySize {
		t.Fatalf("subkey len = %d, want %d", len(a), crypto.KeySize)
	}
}

func TestDeriveSubkeyDistinctAcrossUsers(t *testing.T) {
	t.Parallel()

	key := fixedKey()
	users := []int64{1, 2, 3}
	subkeys := make([][]byte, len(users))
	for i, uid := range users {
		sk, err := crypto.DeriveSubkey(key, uid)
		if err != nil {
			t.Fatalf("DeriveSubkey(%d): %v", uid, err)
		}
		subkeys[i] = sk
	}
	for i := 0; i < len(subkeys); i++ {
		for j := i + 1; j < len(subkeys); j++ {
			if bytes.Equal(subkeys[i], subkeys[j]) {
				t.Fatalf("subkeys for users %d and %d collide", users[i], users[j])
			}
		}
	}
}

// TestDeriveSubkeyFixedVector pins the v1 HKDF derivation to specific
// 32-byte outputs computed against (fixedKey, userID=1) and
// (fixedKey, userID=42) with salt=nil and info string
// "tabby-sync/v1/user/{userID}". The round-trip and "deterministic"
// tests in this file would all still pass if a future refactor
// silently changed the info string (round-trip stays consistent if
// both sides change together), so this fixture is the only thing
// that catches such a drift in CI before it lands on disk and
// bricks every existing row. See v1 review nice-to-have #1 for #10.
//
// The vectors were generated by running HKDF-SHA256 against the
// canonical master key (32 bytes of 0xAB) and the v1 info template;
// any change to KeySize, the SHA-256 substitution, the salt-nil
// choice, or the info template MUST update CryptoVersion and these
// vectors together, AND a migration story for existing rows must be
// documented in docs/CRYPTO.md before the change lands.
func TestDeriveSubkeyFixedVector(t *testing.T) {
	t.Parallel()

	key := fixedKey()

	cases := []struct {
		userID int64
		want   string // hex
	}{
		{1, "744f52660826b37e9ac3d8c9ac902cd4401b1870d18e6c7563429e7a8fdcd43f"},
		{42, "0eb7d949a792f806b4685b564f1ef43dea6251b67142c9ffba91e1a33e5bd530"},
	}
	for _, c := range cases {
		got, err := crypto.DeriveSubkey(key, c.userID)
		if err != nil {
			t.Fatalf("DeriveSubkey(%d): %v", c.userID, err)
		}
		if hex.EncodeToString(got) != c.want {
			t.Fatalf("DeriveSubkey(%d) = %s; want %s (HKDF info or salt drift?)",
				c.userID, hex.EncodeToString(got), c.want)
		}
	}
}

func TestBuildAADByteLayout(t *testing.T) {
	t.Parallel()

	got := crypto.BuildAAD(0x0102030405060708, 0x1112131415161718)
	want := []byte{
		0x01,
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("BuildAAD layout drift\n got=% x\nwant=% x", got, want)
	}
	if len(got) != crypto.AADSize {
		t.Fatalf("AAD len = %d, want %d", len(got), crypto.AADSize)
	}
}

func TestEncryptRejectsWrongLengthMasterKey(t *testing.T) {
	t.Parallel()

	short := bytes.Repeat([]byte{0xAB}, crypto.KeySize-1)
	_, _, err := crypto.Encrypt(short, 1, 1, []byte("x"))
	if err == nil {
		t.Fatal("Encrypt with short key returned nil error")
	}
	if errors.Is(err, crypto.ErrDecrypt) {
		t.Fatal("Encrypt misuse must NOT collapse to ErrDecrypt")
	}
}

func TestDecryptRejectsWrongLengthNonce(t *testing.T) {
	t.Parallel()

	key := fixedKey()
	ct, _, err := crypto.Encrypt(key, 1, 1, []byte("x"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	_, err = crypto.Decrypt(key, 1, 1, ct, make([]byte, crypto.NonceSize-1))
	if !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt for short nonce", err)
	}
}

// TestErrorStringsLeakNothing pins the no-leak contract: every error
// path the package exposes is asserted against the most likely leak
// fixtures. A regression that started %w-wrapping the GCM open error
// or echoing key bytes into a fmt message would surface here.
func TestErrorStringsLeakNothing(t *testing.T) {
	t.Parallel()

	plaintext := []byte("PLAINTEXT_SENTINEL_DO_NOT_LEAK_v1")
	plaintextHex := hex.EncodeToString(plaintext)

	// Encrypt under the good key, then mutate every input to force
	// each Decrypt failure path and inspect the error string.
	good := fixedKey()
	bad := bytes.Repeat([]byte{0xCD}, crypto.KeySize)
	badHex := strings.Repeat("cd", crypto.KeySize)

	ct, nonce, err := crypto.Encrypt(good, 7, 13, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ctHex := hex.EncodeToString(ct)
	nonceHex := hex.EncodeToString(nonce)

	// Build a list of (label, error) pairs covering every decrypt
	// failure plus the Encrypt misuse path.
	tampered := append([]byte(nil), ct...)
	tampered[0] ^= 0x01
	tamperedNonce := append([]byte(nil), nonce...)
	tamperedNonce[0] ^= 0x01

	cases := []struct {
		name string
		err  error
	}{}

	_, e1 := crypto.Decrypt(good, 8, 13, ct, nonce)
	cases = append(cases, struct {
		name string
		err  error
	}{"wrong-userID", e1})
	_, e2 := crypto.Decrypt(good, 7, 14, ct, nonce)
	cases = append(cases, struct {
		name string
		err  error
	}{"wrong-configID", e2})
	_, e3 := crypto.Decrypt(good, 7, 13, tampered, nonce)
	cases = append(cases, struct {
		name string
		err  error
	}{"tampered-ct", e3})
	_, e4 := crypto.Decrypt(good, 7, 13, ct, tamperedNonce)
	cases = append(cases, struct {
		name string
		err  error
	}{"tampered-nonce", e4})
	_, e5 := crypto.Decrypt(bad, 7, 13, ct, nonce)
	cases = append(cases, struct {
		name string
		err  error
	}{"wrong-key", e5})
	_, _, e6 := crypto.Encrypt(bytes.Repeat([]byte{0xAB}, crypto.KeySize-1), 1, 1, plaintext)
	cases = append(cases, struct {
		name string
		err  error
	}{"short-key-encrypt", e6})

	leaks := []struct {
		name  string
		value string
	}{
		{"plaintext", string(plaintext)},
		{"plaintext-hex", plaintextHex},
		{"good-key-hex", fixedKeyHex},
		{"bad-key-hex", badHex},
		{"ciphertext-hex", ctHex},
		{"nonce-hex", nonceHex},
	}
	for _, c := range cases {
		if c.err == nil {
			t.Errorf("%s: expected non-nil error", c.name)
			continue
		}
		msg := c.err.Error()
		for _, l := range leaks {
			if strings.Contains(msg, l.value) {
				t.Errorf("%s: error string leaks %s; msg=%q", c.name, l.name, msg)
			}
		}
	}
}
