package encrypted_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/kuddy-ai/tabby-sync/internal/crypto"
	"github.com/kuddy-ai/tabby-sync/internal/store"
	"github.com/kuddy-ai/tabby-sync/internal/store/encrypted"
	"github.com/kuddy-ai/tabby-sync/internal/store/sqlite"
)

// plaintextSentinel is a recognisable byte string the round-trip and
// no-leak tests use as the configuration content. If a regression ever
// stored plaintext in the SQLite ciphertext column, the substring
// search would find this string.
var plaintextSentinel = []byte("PLAINTEXT_SENTINEL_DO_NOT_LEAK_v1")

// newTestKey returns a fixed 32-byte master key. Tests use a fixed
// pattern so a regression that leaked the key bytes into logs would be
// trivially asserted with strings.Contains.
func newTestKey() []byte {
	k := make([]byte, crypto.KeySize)
	for i := range k {
		k[i] = byte(0xA5)
	}
	return k
}

// newWrapper opens a fresh SQLite store at t.TempDir() and wraps it in
// an encrypted.Store. The cleanup hook closes the wrapper (and through
// it the inner store).
func newWrapper(t *testing.T) (*encrypted.Store, string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	inner, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	w, err := encrypted.New(inner, newTestKey())
	if err != nil {
		_ = inner.Close()
		t.Fatalf("encrypted.New: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w, dbPath
}

// inspect opens a separate sql.DB pointed at the same file the wrapper
// is writing to so tests can inspect the on-disk ciphertext directly.
func inspect(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("inspect open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// readRowBytes reads (content_ciphertext, content_nonce) for a given
// row id directly from the DB.
func readRowBytes(t *testing.T, db *sql.DB, id int64) (ct, nonce []byte) {
	t.Helper()
	row := db.QueryRow(`SELECT content_ciphertext, content_nonce FROM configs WHERE id=?`, id)
	if err := row.Scan(&ct, &nonce); err != nil {
		t.Fatalf("read row %d: %v", id, err)
	}
	return ct, nonce
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	w, _ := newWrapper(t)

	created, err := w.CreateConfigPlaintext(ctx, 7, store.CreateConfigPlaintextInput{
		Name:                "alpha",
		Content:             plaintextSentinel,
		LastUsedWithVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !bytes.Equal(created.Content, plaintextSentinel) {
		t.Fatalf("Create returned content %q; want %q", created.Content, plaintextSentinel)
	}

	got, err := w.GetConfigPlaintext(ctx, 7, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.Content, plaintextSentinel) {
		t.Fatalf("Get returned content %q; want %q", got.Content, plaintextSentinel)
	}
	if got.Name != "alpha" {
		t.Errorf("Get name = %q; want alpha", got.Name)
	}
	if got.LastUsedWithVersion != "1.2.3" {
		t.Errorf("Get last_used = %q; want 1.2.3", got.LastUsedWithVersion)
	}

	list, err := w.ListConfigsByUserPlaintext(ctx, 7)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || !bytes.Equal(list[0].Content, plaintextSentinel) {
		t.Fatalf("List returned %v rows; want 1 with sentinel content", len(list))
	}
}

func TestDatabaseNeverStoresPlaintext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	w, dbPath := newWrapper(t)
	created, err := w.CreateConfigPlaintext(ctx, 1, store.CreateConfigPlaintextInput{
		Name:    "leak-check",
		Content: plaintextSentinel,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	db := inspect(t, dbPath)
	ct, nonce := readRowBytes(t, db, created.ID)
	if bytes.Contains(ct, plaintextSentinel) {
		t.Fatalf("ciphertext contains plaintext sentinel; len(ct)=%d", len(ct))
	}
	if bytes.Contains(nonce, plaintextSentinel) {
		t.Fatalf("nonce contains plaintext sentinel; len(nonce)=%d", len(nonce))
	}
	if len(nonce) != crypto.NonceSize {
		t.Errorf("nonce len = %d; want %d", len(nonce), crypto.NonceSize)
	}
}

func TestCrossUserDecryptFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	w, dbPath := newWrapper(t)

	// User A creates a row.
	createdA, err := w.CreateConfigPlaintext(ctx, 100, store.CreateConfigPlaintextInput{
		Name:    "a-row",
		Content: plaintextSentinel,
	})
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}

	// Read its ciphertext+nonce directly, then INSERT a synthetic row
	// for user B carrying the same bytes.
	db := inspect(t, dbPath)
	ct, nonce := readRowBytes(t, db, createdA.ID)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := db.Exec(
		`INSERT INTO configs (user_id, name, content_ciphertext, content_nonce, last_used_with_version, created_at, modified_at)
		 VALUES (?, ?, ?, ?, NULL, ?, ?)`,
		200, "b-replay", ct, nonce, now, now,
	)
	if err != nil {
		t.Fatalf("inject row: %v", err)
	}
	bID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("inject id: %v", err)
	}

	// Reading the synthetic row as user B must fail with ErrDecrypt
	// because the AAD binds (user, config) and user differs.
	_, err = w.GetConfigPlaintext(ctx, 200, bID)
	if !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("err = %v; want errors.Is(ErrDecrypt)", err)
	}
}

func TestCrossConfigDecryptFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	w, dbPath := newWrapper(t)

	// User creates two rows; we then overwrite row #2's ciphertext
	// with row #1's bytes so the AAD's configID no longer matches.
	row1, err := w.CreateConfigPlaintext(ctx, 1, store.CreateConfigPlaintextInput{
		Name:    "first",
		Content: plaintextSentinel,
	})
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	row2, err := w.CreateConfigPlaintext(ctx, 1, store.CreateConfigPlaintextInput{
		Name:    "second",
		Content: []byte("other-payload"),
	})
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}

	db := inspect(t, dbPath)
	ct, nonce := readRowBytes(t, db, row1.ID)
	if _, err := db.Exec(
		`UPDATE configs SET content_ciphertext=?, content_nonce=? WHERE id=?`,
		ct, nonce, row2.ID,
	); err != nil {
		t.Fatalf("overwrite row 2: %v", err)
	}

	// Reading row 2 must now fail: the bytes are valid but the AAD
	// configID does not match.
	if _, err := w.GetConfigPlaintext(ctx, 1, row2.ID); !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("err = %v; want errors.Is(ErrDecrypt)", err)
	}
}

func TestTamperedCiphertextFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	w, dbPath := newWrapper(t)
	created, err := w.CreateConfigPlaintext(ctx, 1, store.CreateConfigPlaintextInput{
		Name:    "tamper",
		Content: plaintextSentinel,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	db := inspect(t, dbPath)
	ct, _ := readRowBytes(t, db, created.ID)
	// Flip a byte in the middle of the ciphertext.
	ct[len(ct)/2] ^= 0x80
	if _, err := db.Exec(`UPDATE configs SET content_ciphertext=? WHERE id=?`, ct, created.ID); err != nil {
		t.Fatalf("tamper update: %v", err)
	}

	if _, err := w.GetConfigPlaintext(ctx, 1, created.ID); !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("err = %v; want errors.Is(ErrDecrypt)", err)
	}

	// List on a user whose row is tampered must also surface ErrDecrypt.
	if _, err := w.ListConfigsByUserPlaintext(ctx, 1); !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("List err = %v; want errors.Is(ErrDecrypt)", err)
	}
}

func TestUpdateReencrypts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	w, dbPath := newWrapper(t)
	created, err := w.CreateConfigPlaintext(ctx, 1, store.CreateConfigPlaintextInput{
		Name:    "update-target",
		Content: plaintextSentinel,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	db := inspect(t, dbPath)
	beforeCT, beforeNonce := readRowBytes(t, db, created.ID)

	newPT := []byte("new-payload-also-secret")
	updated, err := w.UpdateConfigPlaintext(ctx, 1, created.ID, store.UpdateConfigPlaintextPatch{
		Content: &newPT,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !bytes.Equal(updated.Content, newPT) {
		t.Fatalf("Update returned content %q; want %q", updated.Content, newPT)
	}

	afterCT, afterNonce := readRowBytes(t, db, created.ID)
	if bytes.Equal(beforeCT, afterCT) {
		t.Error("ciphertext unchanged after Update")
	}
	if bytes.Equal(beforeNonce, afterNonce) {
		t.Error("nonce unchanged after Update")
	}
	if bytes.Contains(afterCT, newPT) {
		t.Error("post-update ciphertext contains new plaintext as substring")
	}
}

// TestUpdateContentEmptyReencrypts pins the documented contract that
// a non-nil pointer to an empty slice re-encrypts the row to the
// empty plaintext (the resulting ciphertext is just the GCM auth
// tag) instead of being silently a no-op. v1 semantic review issue
// #1 for #8 + #9 flagged the prior `len(patch.Content) > 0` signal
// as conflating nil and an explicit empty slice; the fix promotes
// Content to *[]byte so the empty-plaintext path is exercisable
// from the API. The test asserts both the post-decrypt round-trip
// (Get returns []byte("") for the empty content) and the
// side-channel invariant that the on-disk ciphertext and nonce
// changed (a fresh nonce was used and the GCM tag was re-computed).
func TestUpdateContentEmptyReencrypts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	w, dbPath := newWrapper(t)
	created, err := w.CreateConfigPlaintext(ctx, 1, store.CreateConfigPlaintextInput{
		Name:    "clear-target",
		Content: plaintextSentinel,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	db := inspect(t, dbPath)
	beforeCT, beforeNonce := readRowBytes(t, db, created.ID)

	empty := []byte{}
	updated, err := w.UpdateConfigPlaintext(ctx, 1, created.ID, store.UpdateConfigPlaintextPatch{
		Content: &empty,
	})
	if err != nil {
		t.Fatalf("Update (empty content): %v", err)
	}
	if len(updated.Content) != 0 {
		t.Fatalf("Update returned content len = %d; want 0", len(updated.Content))
	}

	got, err := w.GetConfigPlaintext(ctx, 1, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Content) != 0 {
		t.Fatalf("Get returned content len = %d; want 0", len(got.Content))
	}

	afterCT, afterNonce := readRowBytes(t, db, created.ID)
	if bytes.Equal(beforeCT, afterCT) {
		t.Error("ciphertext unchanged after empty-content Update; the row was not re-encrypted")
	}
	if bytes.Equal(beforeNonce, afterNonce) {
		t.Error("nonce unchanged after empty-content Update; a fresh nonce should have been generated")
	}
}

// TestUpdateContentNilIsNoOp pins the symmetric contract: a nil
// Content pointer (the JSON field was absent) leaves the row's
// ciphertext and nonce untouched. Together with
// TestUpdateContentEmptyReencrypts this fixes the
// nil-vs-empty-slice ambiguity flagged by v1 semantic review issue
// #1 for #8 + #9.
func TestUpdateContentNilIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	w, dbPath := newWrapper(t)
	created, err := w.CreateConfigPlaintext(ctx, 1, store.CreateConfigPlaintextInput{
		Name:    "noop-target",
		Content: plaintextSentinel,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	db := inspect(t, dbPath)
	beforeCT, beforeNonce := readRowBytes(t, db, created.ID)

	renamed := "renamed"
	if _, err := w.UpdateConfigPlaintext(ctx, 1, created.ID, store.UpdateConfigPlaintextPatch{
		Name: &renamed,
	}); err != nil {
		t.Fatalf("Update (rename only): %v", err)
	}

	got, err := w.GetConfigPlaintext(ctx, 1, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.Content, plaintextSentinel) {
		t.Fatalf("Get content = %q; want %q (rename-only patch must not touch content)", got.Content, plaintextSentinel)
	}

	afterCT, afterNonce := readRowBytes(t, db, created.ID)
	if !bytes.Equal(beforeCT, afterCT) {
		t.Error("ciphertext changed after rename-only Update; nil Content must be a no-op")
	}
	if !bytes.Equal(beforeNonce, afterNonce) {
		t.Error("nonce changed after rename-only Update; nil Content must be a no-op")
	}
}

func TestListReturnsOnlyOwnRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	w, _ := newWrapper(t)
	if _, err := w.CreateConfigPlaintext(ctx, 1, store.CreateConfigPlaintextInput{
		Name: "alpha-1", Content: []byte("one"),
	}); err != nil {
		t.Fatalf("Create user 1: %v", err)
	}
	if _, err := w.CreateConfigPlaintext(ctx, 2, store.CreateConfigPlaintextInput{
		Name: "alpha-2", Content: []byte("two"),
	}); err != nil {
		t.Fatalf("Create user 2: %v", err)
	}

	list1, err := w.ListConfigsByUserPlaintext(ctx, 1)
	if err != nil {
		t.Fatalf("List user 1: %v", err)
	}
	if len(list1) != 1 || string(list1[0].Content) != "one" {
		t.Fatalf("List user 1 = %v; want one row with content 'one'", list1)
	}
	list2, err := w.ListConfigsByUserPlaintext(ctx, 2)
	if err != nil {
		t.Fatalf("List user 2: %v", err)
	}
	if len(list2) != 1 || string(list2[0].Content) != "two" {
		t.Fatalf("List user 2 = %v; want one row with content 'two'", list2)
	}
}

func TestNewRejectsWrongLengthKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	inner, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer inner.Close()

	for _, badLen := range []int{0, 1, 31, 33, 64} {
		_, err := encrypted.New(inner, make([]byte, badLen))
		if err == nil {
			t.Errorf("New(%d-byte key) returned nil error; want non-nil", badLen)
		}
	}
}

func TestNewRejectsNilInner(t *testing.T) {
	t.Parallel()
	if _, err := encrypted.New(nil, newTestKey()); err == nil {
		t.Fatal("New(nil inner) returned nil error; want non-nil")
	}
}

// mockStore is a deliberately-broken in-memory [store.Store] used to
// prove the two-step write rollback. CreateConfig succeeds; UpdateConfig
// always fails with the configured error; DeleteConfig records the call
// and reports success or the configured error. The other methods are
// implemented as no-ops sufficient to satisfy the interface.
type mockStore struct {
	createCalls []int64
	updateErr   error
	deleteCalls []int64
	deleteErr   error
	nextID      int64
	closed      bool
}

func (m *mockStore) CreateConfig(_ context.Context, _ int64, in store.CreateConfigInput) (store.Config, error) {
	m.nextID++
	m.createCalls = append(m.createCalls, m.nextID)
	return store.Config{
		ID:                m.nextID,
		Name:              in.Name,
		ContentCiphertext: in.ContentCiphertext,
		ContentNonce:      in.ContentNonce,
	}, nil
}

func (m *mockStore) GetConfig(_ context.Context, _, configID int64) (store.Config, error) {
	return store.Config{ID: configID}, nil
}

func (m *mockStore) ListConfigsByUser(_ context.Context, _ int64) ([]store.Config, error) {
	return nil, nil
}

func (m *mockStore) UpdateConfig(_ context.Context, _, _ int64, _ store.UpdateConfigPatch) (store.Config, error) {
	if m.updateErr != nil {
		return store.Config{}, m.updateErr
	}
	return store.Config{}, nil
}

func (m *mockStore) DeleteConfig(_ context.Context, _, configID int64) error {
	m.deleteCalls = append(m.deleteCalls, configID)
	return m.deleteErr
}

func (m *mockStore) Close() error {
	m.closed = true
	return nil
}

func TestCreateRollsBackOnSecondStepFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &mockStore{updateErr: errors.New("update failed (test)")}
	w, err := encrypted.New(mock, newTestKey())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close()

	_, err = w.CreateConfigPlaintext(ctx, 1, store.CreateConfigPlaintextInput{
		Name:    "rollback",
		Content: plaintextSentinel,
	})
	if err == nil {
		t.Fatal("CreateConfigPlaintext succeeded; want error from broken update")
	}

	if len(mock.createCalls) != 1 {
		t.Fatalf("createCalls = %v; want exactly one create", mock.createCalls)
	}
	insertedID := mock.createCalls[0]
	if len(mock.deleteCalls) != 1 || mock.deleteCalls[0] != insertedID {
		t.Fatalf("deleteCalls = %v; want exactly [%d] (rollback of orphan)",
			mock.deleteCalls, insertedID)
	}
}

func TestDecryptErrorsAreUnwrapped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	w, dbPath := newWrapper(t)
	created, err := w.CreateConfigPlaintext(ctx, 1, store.CreateConfigPlaintextInput{
		Name:    "unwrap",
		Content: plaintextSentinel,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Tamper directly via inspect.
	db := inspect(t, dbPath)
	ct, _ := readRowBytes(t, db, created.ID)
	ct[0] ^= 0x01
	if _, err := db.Exec(`UPDATE configs SET content_ciphertext=? WHERE id=?`, ct, created.ID); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	// errors.Is must see the bare sentinel without strings.Contains.
	_, err = w.GetConfigPlaintext(ctx, 1, created.ID)
	if !errors.Is(err, crypto.ErrDecrypt) {
		t.Fatalf("err = %v; want errors.Is(ErrDecrypt)", err)
	}
	if err != crypto.ErrDecrypt { // pin the unwrapped contract specifically
		t.Fatalf("err is wrapped (%T %v); want bare crypto.ErrDecrypt", err, err)
	}
}

func TestDeleteForwardsAndCloseClosesInner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := &mockStore{}
	w, err := encrypted.New(mock, newTestKey())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := w.DeleteConfig(ctx, 1, 42); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(mock.deleteCalls) != 1 || mock.deleteCalls[0] != 42 {
		t.Fatalf("deleteCalls = %v; want [42]", mock.deleteCalls)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !mock.closed {
		t.Fatal("inner.Close was not invoked")
	}
}
