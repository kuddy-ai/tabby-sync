package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

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
	if updated.ModifiedAt.Before(created.ModifiedAt) {
		t.Errorf("ModifiedAt regressed: created=%v updated=%v", created.ModifiedAt, updated.ModifiedAt)
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
