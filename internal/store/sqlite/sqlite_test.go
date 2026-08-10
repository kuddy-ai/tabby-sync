package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/kuddy-ai/tabby-sync/internal/store"
	"github.com/kuddy-ai/tabby-sync/internal/store/sqlite"
)

// newDBPath returns a fresh per-test database path under t.TempDir() so
// every test runs against an isolated file and can safely set
// t.Parallel().
func newDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.db")
}

// strPtr returns a pointer to s. UpdateConfigPatch.Name is *string and
// Go does not let callers take the address of a string literal directly.
func strPtr(s string) *string { return &s }

// inspect opens a separate sql.DB pointed at the same file the Store
// under test was opened against, applying the same DSN pragmas the
// production code uses. The tests use this to query sqlite_master and
// PRAGMA values without reaching into the Store's unexported *sql.DB.
//
// _txlock is intentionally NOT mirrored on the inspection connection:
// the production-side option only controls how BeginTx renders its
// `BEGIN`, which is irrelevant to the read-only inspection queries
// here. Keeping the inspection DSN narrow also prevents a future
// regression that flips _txlock from quietly affecting any test that
// uses this helper.
func inspect(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := path + "?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("inspect open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenAppliesMigrationsOnFreshDB(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := newDBPath(t)

	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	db := inspect(t, path)

	// Both tables must exist.
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		tables = append(tables, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if !contains(tables, "configs") || !contains(tables, "schema_migrations") {
		t.Fatalf("expected configs and schema_migrations tables; got %v", tables)
	}

	// All four pragmas must read back the values applied at Open. The
	// inspection connection applies the same DSN pragmas as the
	// production Store, so it is a reasonable proxy for what every
	// connection in production sees.
	cases := []struct {
		pragma string
		want   string
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"busy_timeout", "5000"},
		{"synchronous", "1"},
	}
	for _, c := range cases {
		var got string
		if err := db.QueryRow("PRAGMA " + c.pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", c.pragma, err)
		}
		if got != c.want {
			t.Errorf("PRAGMA %s = %q; want %q", c.pragma, got, c.want)
		}
	}

	count := queryInt(t, db, `SELECT COUNT(*) FROM schema_migrations`)
	if count != 1 {
		t.Errorf("schema_migrations row count = %d; want 1", count)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := newDBPath(t)

	st1, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := st1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	st2, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer st2.Close()

	db := inspect(t, path)
	count := queryInt(t, db, `SELECT COUNT(*) FROM schema_migrations`)
	if count != 1 {
		t.Errorf("schema_migrations row count after reopen = %d; want 1", count)
	}
}

func TestCreateGetUpdateDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := sqlite.Open(ctx, newDBPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	const userID int64 = 42
	in := store.CreateConfigInput{
		Name:                "primary",
		ContentCiphertext:   []byte{0x01, 0x02, 0x03},
		ContentNonce:        []byte{0x10, 0x11, 0x12},
		LastUsedWithVersion: "1.0.0",
	}
	created, err := st.CreateConfig(ctx, userID, in)
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	if created.ID == 0 {
		t.Errorf("CreateConfig returned ID 0")
	}
	if created.UserID != userID {
		t.Errorf("created.UserID = %d; want %d", created.UserID, userID)
	}
	if created.Name != "primary" {
		t.Errorf("created.Name = %q; want %q", created.Name, "primary")
	}
	if created.LastUsedWithVersion != "1.0.0" {
		t.Errorf("created.LastUsedWithVersion = %q; want %q", created.LastUsedWithVersion, "1.0.0")
	}
	if created.CreatedAt.IsZero() || created.ModifiedAt.IsZero() {
		t.Errorf("timestamps not populated: %+v", created)
	}

	got, err := st.GetConfig(ctx, userID, created.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got.ID != created.ID || got.Name != created.Name {
		t.Errorf("GetConfig mismatch: got=%+v want=%+v", got, created)
	}

	list, err := st.ListConfigsByUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListConfigsByUser: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("ListConfigsByUser = %+v; want exactly the created row", list)
	}

	patch := store.UpdateConfigPatch{
		Name:                strPtr("renamed"),
		ContentCiphertext:   []byte{0xAA, 0xBB},
		ContentNonce:        []byte{0xCC, 0xDD},
		LastUsedWithVersion: strPtr(""),
	}
	updated, err := st.UpdateConfig(ctx, userID, created.ID, patch)
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}
	if updated.Name != "renamed" {
		t.Errorf("updated.Name = %q; want %q", updated.Name, "renamed")
	}
	if updated.LastUsedWithVersion != "" {
		t.Errorf("updated.LastUsedWithVersion = %q; want empty (NULL)", updated.LastUsedWithVersion)
	}
	if string(updated.ContentCiphertext) != string([]byte{0xAA, 0xBB}) {
		t.Errorf("updated.ContentCiphertext = %x; want aabb", updated.ContentCiphertext)
	}
	if !updated.ModifiedAt.After(created.ModifiedAt) {
		t.Errorf("ModifiedAt did not strictly advance: created=%v updated=%v", created.ModifiedAt, updated.ModifiedAt)
	}

	if err := st.DeleteConfig(ctx, userID, created.ID); err != nil {
		t.Fatalf("DeleteConfig: %v", err)
	}
	if _, err := st.GetConfig(ctx, userID, created.ID); !errors.Is(err, store.ErrConfigNotFound) {
		t.Errorf("GetConfig after delete = %v; want ErrConfigNotFound", err)
	}
}

func TestCrossUserIsolation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := sqlite.Open(ctx, newDBPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	cfgU1, err := st.CreateConfig(ctx, 1, store.CreateConfigInput{
		Name:              "u1",
		ContentCiphertext: []byte{0x01},
		ContentNonce:      []byte{0x02},
	})
	if err != nil {
		t.Fatalf("CreateConfig user 1: %v", err)
	}
	cfgU2, err := st.CreateConfig(ctx, 2, store.CreateConfigInput{
		Name:              "u2",
		ContentCiphertext: []byte{0x03},
		ContentNonce:      []byte{0x04},
	})
	if err != nil {
		t.Fatalf("CreateConfig user 2: %v", err)
	}

	// User 1 must not see user 2's row through Get/Update/Delete.
	if _, err := st.GetConfig(ctx, 1, cfgU2.ID); !errors.Is(err, store.ErrConfigNotFound) {
		t.Errorf("u1.Get(u2) = %v; want ErrConfigNotFound", err)
	}
	if _, err := st.UpdateConfig(ctx, 1, cfgU2.ID, store.UpdateConfigPatch{Name: strPtr("hacked")}); !errors.Is(err, store.ErrConfigNotFound) {
		t.Errorf("u1.Update(u2) = %v; want ErrConfigNotFound", err)
	}
	if err := st.DeleteConfig(ctx, 1, cfgU2.ID); !errors.Is(err, store.ErrConfigNotFound) {
		t.Errorf("u1.Delete(u2) = %v; want ErrConfigNotFound", err)
	}

	// User 2's row must remain untouched.
	roundTrip, err := st.GetConfig(ctx, 2, cfgU2.ID)
	if err != nil {
		t.Fatalf("GetConfig user 2: %v", err)
	}
	if roundTrip.Name != "u2" {
		t.Errorf("u2 row mutated by cross-user attempt: got name=%q", roundTrip.Name)
	}

	// List must scope to caller.
	list1, err := st.ListConfigsByUser(ctx, 1)
	if err != nil {
		t.Fatalf("ListConfigsByUser user 1: %v", err)
	}
	if len(list1) != 1 || list1[0].ID != cfgU1.ID {
		t.Errorf("ListConfigsByUser(1) = %+v; want exactly u1's row", list1)
	}
}

func TestUpdateInvalidPatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := sqlite.Open(ctx, newDBPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	created, err := st.CreateConfig(ctx, 1, store.CreateConfigInput{
		Name:              "n",
		ContentCiphertext: []byte{0x01},
		ContentNonce:      []byte{0x02},
	})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}

	// Only ciphertext set, nonce missing: must reject and not mutate.
	_, err = st.UpdateConfig(ctx, 1, created.ID, store.UpdateConfigPatch{
		ContentCiphertext: []byte{0xFF},
	})
	if !errors.Is(err, store.ErrInvalidPatch) {
		t.Errorf("UpdateConfig (cipher only) = %v; want ErrInvalidPatch", err)
	}

	// Verify the row is unchanged.
	got, err := st.GetConfig(ctx, 1, created.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if string(got.ContentCiphertext) != string([]byte{0x01}) {
		t.Errorf("row mutated despite invalid patch: %x", got.ContentCiphertext)
	}

	// Symmetric case: only nonce set.
	_, err = st.UpdateConfig(ctx, 1, created.ID, store.UpdateConfigPatch{
		ContentNonce: []byte{0xFF},
	})
	if !errors.Is(err, store.ErrInvalidPatch) {
		t.Errorf("UpdateConfig (nonce only) = %v; want ErrInvalidPatch", err)
	}
}

func TestUpdateNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := sqlite.Open(ctx, newDBPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	_, err = st.UpdateConfig(ctx, 1, 9999, store.UpdateConfigPatch{Name: strPtr("nope")})
	if !errors.Is(err, store.ErrConfigNotFound) {
		t.Errorf("UpdateConfig missing id = %v; want ErrConfigNotFound", err)
	}
}

// TestUpdateLastUsedWithVersionEmptyBecomesNull pins the documented
// empty-string-collapses-to-NULL behaviour of UpdateConfigPatch.
// The test asserts that:
//
//  1. a non-nil pointer to "" overwrites a previously-set version with
//     SQL NULL on disk and reads back as an empty Go string;
//  2. a nil pointer leaves the existing value untouched (this is the
//     "do not change" contract every patch field follows).
//
// The on-disk NULL is verified directly via a side-channel inspection
// connection so a future regression that silently writes the empty
// string into the column would still flip the assertion.
func TestUpdateLastUsedWithVersionEmptyBecomesNull(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := newDBPath(t)
	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	const userID int64 = 99
	created, err := st.CreateConfig(ctx, userID, store.CreateConfigInput{
		Name:                "v",
		ContentCiphertext:   []byte{0x01},
		ContentNonce:        []byte{0x02},
		LastUsedWithVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}

	// Clear via *string("").
	cleared, err := st.UpdateConfig(ctx, userID, created.ID, store.UpdateConfigPatch{
		LastUsedWithVersion: strPtr(""),
	})
	if err != nil {
		t.Fatalf("UpdateConfig (clear): %v", err)
	}
	if cleared.LastUsedWithVersion != "" {
		t.Errorf("after clear, LastUsedWithVersion = %q; want empty", cleared.LastUsedWithVersion)
	}

	// Side-channel: confirm the on-disk column is SQL NULL, not "".
	db := inspect(t, path)
	var raw sql.NullString
	if err := db.QueryRow(`SELECT last_used_with_version FROM configs WHERE id = ?`, created.ID).Scan(&raw); err != nil {
		t.Fatalf("inspect select: %v", err)
	}
	if raw.Valid {
		t.Errorf("on-disk column = %q (Valid=true); want SQL NULL", raw.String)
	}

	// Nil pointer must leave the (now-NULL) field alone.
	again, err := st.UpdateConfig(ctx, userID, created.ID, store.UpdateConfigPatch{
		Name: strPtr("renamed"),
	})
	if err != nil {
		t.Fatalf("UpdateConfig (no-touch): %v", err)
	}
	if again.LastUsedWithVersion != "" {
		t.Errorf("after no-touch update, LastUsedWithVersion = %q; want empty", again.LastUsedWithVersion)
	}
	if again.Name != "renamed" {
		t.Errorf("after no-touch update, Name = %q; want %q", again.Name, "renamed")
	}
}

func TestDeleteNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := sqlite.Open(ctx, newDBPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if err := st.DeleteConfig(ctx, 1, 9999); !errors.Is(err, store.ErrConfigNotFound) {
		t.Errorf("DeleteConfig missing id = %v; want ErrConfigNotFound", err)
	}
}

// TestOpenTightensDBFileMode pins the post-Open file mode of the main
// SQLite database file and any -wal / -shm sidecars to 0o600. SQLite
// otherwise creates these at the process umask (commonly 0o644 on
// Linux), which would expose encrypted credential blobs to other local
// users on a multi-user host. The test forces a sloppy umask so it
// catches regressions even on hosts whose default umask already yields
// 0o600. Skipped on Windows because os.Chmod's permission bits are a
// stub there; the chmod call in production is harmless on Windows but
// the assertion would not be meaningful.
func TestOpenTightensDBFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not meaningful on Windows")
	}

	old := syscallUmask(0o022)
	t.Cleanup(func() { _ = syscallUmask(old) })

	ctx := context.Background()
	path := newDBPath(t)

	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// Force at least one row into the configs table so SQLite has a
	// reason to materialise the -wal sidecar (the bookkeeping insert in
	// applyMigrations already does this, but a direct write makes the
	// expectation explicit).
	if _, err := st.CreateConfig(ctx, 7, store.CreateConfigInput{
		Name:              "warmup",
		ContentCiphertext: []byte{0x01},
		ContentNonce:      []byte{0x02},
	}); err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}

	mustBe600 := func(p string) {
		t.Helper()
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if got := info.Mode().Perm(); got != fs.FileMode(0o600) {
			t.Errorf("%s mode = %#o; want 0600", p, got)
		}
	}

	mustBe600(path)
	for _, suffix := range sqlite.DBSidecarSuffixes {
		p := path + suffix
		if _, err := os.Stat(p); err == nil {
			mustBe600(p)
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat sidecar %s: %v", p, err)
		}
	}
}

// TestUpdateConfigBumpsModifiedAtOnEveryCall pins the strictly-monotonic
// modified_at contract from issue #9: ten rapid successive UpdateConfig
// calls with no sleep between them MUST each return a strictly greater
// ModifiedAt than the previous call. The strict-greater guarantee is
// what lets clients diff-by-modified_at without losing edits when two
// PATCHes land inside the same wall-clock millisecond.
//
// The test deliberately does NOT call SetClockForTest so the assertion
// holds against the real wall clock; the strict-greater invariant is
// the production contract clients depend on.
//
// The test asserts ONLY `cur.After(prev)` and intentionally does NOT
// require a 1ms separation between consecutive timestamps. Although
// WAL fsync between PATCHes makes the per-iteration wall-clock delta
// well over 1ms on a typical Linux host, the production algorithm
// only promises strictly-greater (`max(now, old + 1ms)` keeps the
// candidate as-is when `now` is even one nanosecond past `old`), so
// pinning ≥1ms here would be over-strict.
func TestUpdateConfigBumpsModifiedAtOnEveryCall(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := sqlite.Open(ctx, newDBPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	const userID int64 = 7
	created, err := st.CreateConfig(ctx, userID, store.CreateConfigInput{
		Name:              "rapid",
		ContentCiphertext: []byte{0x01},
		ContentNonce:      []byte{0x02},
	})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}

	prev := created.ModifiedAt
	for i := 0; i < 10; i++ {
		updated, err := st.UpdateConfig(ctx, userID, created.ID, store.UpdateConfigPatch{
			Name: strPtr("rapid"),
		})
		if err != nil {
			t.Fatalf("UpdateConfig iter %d: %v", i, err)
		}
		if !updated.ModifiedAt.After(prev) {
			t.Fatalf("iter %d: ModifiedAt did not strictly advance: prev=%v got=%v", i, prev, updated.ModifiedAt)
		}
		prev = updated.ModifiedAt
	}
}

// TestUpdateConfigPreservesPrecisionWhenClockJumpsBack pins the
// max(now, old+1ms) fallback in UpdateConfig: when the injected clock
// returns a time strictly before the row's existing modified_at, the
// new modified_at MUST be exactly old + 1ms. Without this guard a
// backwards wall-clock skew (NTP jump, leap-second smear, suspended
// VM) would write a regressed timestamp and break the issue #9
// contract.
//
// Cannot run with t.Parallel because SetClockForTest mutates a
// package-global seam.
func TestUpdateConfigPreservesPrecisionWhenClockJumpsBack(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(ctx, newDBPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	const userID int64 = 11
	created, err := st.CreateConfig(ctx, userID, store.CreateConfigInput{
		Name:              "skew",
		ContentCiphertext: []byte{0x01},
		ContentNonce:      []byte{0x02},
	})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}

	// Inject a clock that returns a time strictly before the row's
	// existing modified_at. The fallback must kick in and use
	// old + 1ms as the new modified_at.
	jumpedBack := created.ModifiedAt.Add(-1 * time.Hour)
	sqlite.SetClockForTest(t, func() time.Time { return jumpedBack })

	updated, err := st.UpdateConfig(ctx, userID, created.ID, store.UpdateConfigPatch{
		Name: strPtr("skew"),
	})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	wantModifiedAt := created.ModifiedAt.Add(time.Millisecond)
	if !updated.ModifiedAt.Equal(wantModifiedAt) {
		t.Errorf("ModifiedAt = %v; want exactly old+1ms = %v", updated.ModifiedAt, wantModifiedAt)
	}
	if !updated.ModifiedAt.After(created.ModifiedAt) {
		t.Errorf("ModifiedAt did not strictly advance under backwards clock skew: created=%v updated=%v", created.ModifiedAt, updated.ModifiedAt)
	}
}

// TestUpdateConfigBumpsModifiedAtEvenWithEmptyPatch pins that an
// all-nil/all-empty patch still advances modified_at. The empty-patch
// case exists because clients sometimes touch a row without changing
// any field (e.g. to refresh "last seen" semantics in a future revision)
// and issue #9 requires that every successful UpdateConfig return a
// strictly greater modified_at than the row had before.
func TestUpdateConfigBumpsModifiedAtEvenWithEmptyPatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := sqlite.Open(ctx, newDBPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	const userID int64 = 13
	created, err := st.CreateConfig(ctx, userID, store.CreateConfigInput{
		Name:              "noop",
		ContentCiphertext: []byte{0x01},
		ContentNonce:      []byte{0x02},
	})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}

	updated, err := st.UpdateConfig(ctx, userID, created.ID, store.UpdateConfigPatch{})
	if err != nil {
		t.Fatalf("UpdateConfig (empty patch): %v", err)
	}
	if !updated.ModifiedAt.After(created.ModifiedAt) {
		t.Errorf("empty-patch ModifiedAt did not strictly advance: created=%v updated=%v", created.ModifiedAt, updated.ModifiedAt)
	}
	// Confirm no other fields changed value.
	if updated.Name != created.Name {
		t.Errorf("empty-patch Name changed: %q -> %q", created.Name, updated.Name)
	}
	if string(updated.ContentCiphertext) != string(created.ContentCiphertext) {
		t.Errorf("empty-patch ContentCiphertext changed: %x -> %x", created.ContentCiphertext, updated.ContentCiphertext)
	}
	if string(updated.ContentNonce) != string(created.ContentNonce) {
		t.Errorf("empty-patch ContentNonce changed: %x -> %x", created.ContentNonce, updated.ContentNonce)
	}
	if updated.LastUsedWithVersion != created.LastUsedWithVersion {
		t.Errorf("empty-patch LastUsedWithVersion changed: %q -> %q", created.LastUsedWithVersion, updated.LastUsedWithVersion)
	}
}

func TestUpdateConfigPreservesModifiedAtForMetadataOnlyCAS(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := sqlite.Open(ctx, newDBPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	created, err := st.CreateConfig(ctx, 21, store.CreateConfigInput{
		Name:                "metadata",
		ContentCiphertext:   []byte{0x01},
		ContentNonce:        []byte{0x02},
		LastUsedWithVersion: "1.0.234",
	})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	version := "1.0.235"
	expected := created.ModifiedAt
	updated, err := st.UpdateConfig(ctx, 21, created.ID, store.UpdateConfigPatch{
		LastUsedWithVersion: &version,
		PreserveModifiedAt:  true,
		ExpectedModifiedAt:  &expected,
	})
	if err != nil {
		t.Fatalf("metadata-only UpdateConfig: %v", err)
	}
	if !updated.ModifiedAt.Equal(created.ModifiedAt) {
		t.Errorf("ModifiedAt changed: before=%v after=%v", created.ModifiedAt, updated.ModifiedAt)
	}
	if updated.LastUsedWithVersion != version {
		t.Errorf("LastUsedWithVersion = %q; want %q", updated.LastUsedWithVersion, version)
	}
}

func TestUpdateConfigPreserveModifiedAtRejectsStaleCAS(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := sqlite.Open(ctx, newDBPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	created, err := st.CreateConfig(ctx, 22, store.CreateConfigInput{
		Name:                "before",
		ContentCiphertext:   []byte{0x01},
		ContentNonce:        []byte{0x02},
		LastUsedWithVersion: "1.0.234",
	})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	concurrentName := "concurrent"
	concurrent, err := st.UpdateConfig(ctx, 22, created.ID, store.UpdateConfigPatch{Name: &concurrentName})
	if err != nil {
		t.Fatalf("concurrent UpdateConfig: %v", err)
	}

	version := "1.0.235"
	expected := created.ModifiedAt
	_, err = st.UpdateConfig(ctx, 22, created.ID, store.UpdateConfigPatch{
		LastUsedWithVersion: &version,
		PreserveModifiedAt:  true,
		ExpectedModifiedAt:  &expected,
	})
	if !errors.Is(err, store.ErrConfigChanged) {
		t.Fatalf("stale metadata update err = %v; want ErrConfigChanged", err)
	}
	got, err := st.GetConfig(ctx, 22, created.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if got.Name != concurrentName || !got.ModifiedAt.Equal(concurrent.ModifiedAt) {
		t.Errorf("stale CAS mutated concurrent state: got=%+v", got)
	}
	if got.LastUsedWithVersion != created.LastUsedWithVersion {
		t.Errorf("stale CAS changed version: got %q want %q", got.LastUsedWithVersion, created.LastUsedWithVersion)
	}
}

func TestCreateConfigWithIDPreservesMonotonicTimestamp(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(ctx, newDBPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	fixed := time.Date(2026, 8, 10, 12, 0, 0, 123456789, time.UTC)
	sqlite.SetClockForTest(t, func() time.Time { return fixed })
	created, err := st.CreateConfigWithID(ctx, 30, func(configID int64) (store.CreateConfigInput, error) {
		return store.CreateConfigInput{
			Name:              "clock",
			ContentCiphertext: []byte{0xA1},
			ContentNonce:      []byte{0xB2},
		}, nil
	})
	if err != nil {
		t.Fatalf("CreateConfigWithID: %v", err)
	}
	if !created.CreatedAt.Equal(fixed) {
		t.Fatalf("CreatedAt = %v; want %v", created.CreatedAt, fixed)
	}
	wantModified := fixed.Add(time.Millisecond)
	if !created.ModifiedAt.Equal(wantModified) {
		t.Fatalf("ModifiedAt = %v; want %v", created.ModifiedAt, wantModified)
	}
}

func TestCreateConfigWithIDRollsBackBuilderFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := newDBPath(t)
	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	buildErr := errors.New("builder failed (test)")
	buildCalls := 0
	_, err = st.CreateConfigWithID(ctx, 31, func(configID int64) (store.CreateConfigInput, error) {
		buildCalls++
		if configID <= 0 {
			t.Errorf("reserved configID = %d; want positive", configID)
		}
		return store.CreateConfigInput{}, buildErr
	})
	if !errors.Is(err, buildErr) {
		t.Fatalf("CreateConfigWithID error = %v; want builder marker", err)
	}
	if buildCalls != 1 {
		t.Fatalf("builder calls = %d; want exactly 1", buildCalls)
	}

	if got, err := st.CountConfigsByUser(ctx, 31); err != nil || got != 0 {
		t.Fatalf("count after builder failure = %d, %v; want 0, nil", got, err)
	}
	db := inspect(t, path)
	if got := queryInt(t, db, `SELECT COUNT(*) FROM configs`); got != 0 {
		t.Fatalf("persisted rows after builder failure = %d; want 0", got)
	}
}

func TestCreateConfigWithIDRollsBackInjectedFinalisationFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := newDBPath(t)
	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	db := inspect(t, path)
	if _, err := db.Exec(`
        CREATE TRIGGER inject_atomic_create_failure
        AFTER UPDATE OF content_ciphertext ON configs
        BEGIN
            SELECT RAISE(ABORT, 'injected atomic create failure');
        END`); err != nil {
		t.Fatalf("create failure-injection trigger: %v", err)
	}

	_, err = st.CreateConfigWithID(ctx, 32, func(configID int64) (store.CreateConfigInput, error) {
		return store.CreateConfigInput{
			Name:                "finalised-before-failure",
			ContentCiphertext:   []byte{0xA1},
			ContentNonce:        []byte{0xB2},
			LastUsedWithVersion: "1.2.3",
		}, nil
	})
	if err == nil {
		t.Fatal("CreateConfigWithID succeeded; want injected finalisation failure")
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM configs`); got != 0 {
		t.Fatalf("persisted rows after finalisation failure = %d; want 0", got)
	}
}

func TestCreateConfigWithIDHidesReservedRowFromConcurrentReader(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := newDBPath(t)
	writer, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer writer.Close()
	reader, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer reader.Close()

	const userID int64 = 33
	reserved := make(chan int64, 1)
	release := make(chan struct{})
	type createResult struct {
		cfg store.Config
		err error
	}
	done := make(chan createResult, 1)
	go func() {
		cfg, createErr := writer.CreateConfigWithID(ctx, userID, func(configID int64) (store.CreateConfigInput, error) {
			reserved <- configID
			<-release
			return store.CreateConfigInput{
				Name:                "atomic",
				ContentCiphertext:   []byte{0xCA, 0xFE},
				ContentNonce:        []byte{0xBA, 0xBE},
				LastUsedWithVersion: "2.0.0",
			}, nil
		})
		done <- createResult{cfg: cfg, err: createErr}
	}()

	var reservedID int64
	select {
	case reservedID = <-reserved:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("timed out waiting for reserved ID")
	}

	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	rows, readErr := reader.ListConfigsByUser(readCtx, userID)
	cancel()
	close(release)

	var result createResult
	select {
	case result = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for atomic create to commit")
	}
	if readErr != nil {
		t.Fatalf("concurrent ListConfigsByUser: %v", readErr)
	}
	if len(rows) != 0 {
		t.Fatalf("concurrent reader observed %d uncommitted rows; want 0", len(rows))
	}
	if result.err != nil {
		t.Fatalf("CreateConfigWithID: %v", result.err)
	}
	if result.cfg.ID != reservedID || result.cfg.UserID != userID {
		t.Fatalf("created identity = (%d, %d); want (%d, %d)", result.cfg.UserID, result.cfg.ID, userID, reservedID)
	}
	if !result.cfg.ModifiedAt.After(result.cfg.CreatedAt) {
		t.Fatalf("create timestamp is not monotonic: created=%v modified=%v", result.cfg.CreatedAt, result.cfg.ModifiedAt)
	}

	rows, err = reader.ListConfigsByUser(ctx, userID)
	if err != nil {
		t.Fatalf("post-commit ListConfigsByUser: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != reservedID || rows[0].Name != "atomic" {
		t.Fatalf("post-commit rows = %+v; want one finalised row", rows)
	}
	otherRows, err := reader.ListConfigsByUser(ctx, userID+1)
	if err != nil || len(otherRows) != 0 {
		t.Fatalf("cross-user list = %+v, %v; want empty, nil", otherRows, err)
	}
}

// TestModifiedAtRoundTripsThroughRFC3339 pins that the on-disk
// modified_at format (RFC3339Nano) parses cleanly with both
// time.RFC3339Nano and the stricter time.RFC3339, so typical clients
// using the standard ISO8601 / RFC3339 layout can read the value the
// API surfaces without bespoke handling. This is part of the issue #9
// contract: monotonicity at millisecond precision MUST NOT come at the
// cost of breaking standard parsers.
func TestModifiedAtRoundTripsThroughRFC3339(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := newDBPath(t)
	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	const userID int64 = 17
	created, err := st.CreateConfig(ctx, userID, store.CreateConfigInput{
		Name:              "rfc3339",
		ContentCiphertext: []byte{0x01},
		ContentNonce:      []byte{0x02},
	})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}

	// Read the raw on-disk string via a side-channel inspection
	// connection so the assertion is on the persisted form, not on
	// whatever Go re-renders from time.Time.
	db := inspect(t, path)
	var raw string
	if err := db.QueryRow(`SELECT modified_at FROM configs WHERE id = ?`, created.ID).Scan(&raw); err != nil {
		t.Fatalf("inspect select: %v", err)
	}

	if _, err := time.Parse(time.RFC3339Nano, raw); err != nil {
		t.Errorf("modified_at %q failed time.RFC3339Nano parse: %v", raw, err)
	}
	if _, err := time.Parse(time.RFC3339, raw); err != nil {
		t.Errorf("modified_at %q failed time.RFC3339 parse: %v", raw, err)
	}
}

// queryInt runs a single-row, single-column scan and returns the int.
func queryInt(t *testing.T, db *sql.DB, q string) int {
	t.Helper()
	var v int
	if err := db.QueryRow(q).Scan(&v); err != nil {
		t.Fatalf("QueryRow %q: %v", q, err)
	}
	return v
}

// contains reports whether haystack contains needle.
func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
