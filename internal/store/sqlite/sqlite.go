// Package sqlite provides a SQLite-backed implementation of
// [store.Store] using the pure-Go modernc.org/sqlite driver, so the
// resulting binary builds with CGO_ENABLED=0.
//
// The package owns: opening (and creating) the database file at a
// caller-supplied path, applying embedded migrations, applying a fixed
// set of pragmas (journal_mode=WAL, foreign_keys=ON, busy_timeout=5000,
// synchronous=NORMAL) and verifying each one took effect, and
// implementing the per-user CRUD contract from the parent package.
//
// Per the store package contract, every WHERE clause includes
// `user_id = ?` and any cross-user lookup is reported as
// [store.ErrConfigNotFound]. The store layer never logs row contents,
// raw user input, or DSN values.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	// Driver registration only. modernc.org/sqlite registers itself as
	// "sqlite" (not "sqlite3"), so callers use sql.Open("sqlite", dsn).
	_ "modernc.org/sqlite"

	"github.com/kuddy-ai/tabby-sync/internal/store"
)

// Compile-time assertion that *Store satisfies the parent package's
// Store contract. Any drift between the interface and this
// implementation surfaces at build time, not at runtime.
var _ store.Store = (*Store)(nil)

// Store is a SQLite-backed [store.Store]. Construct one with [Open] and
// release it with [Store.Close].
//
// The underlying *sql.DB is configured with MaxOpenConns = 1 so all
// reads and writes are serialised on a single connection. SQLite is
// happiest as a single-writer database, and serialising here means the
// pragmas applied at Open are guaranteed to be in force for every
// subsequent statement (modernc.org/sqlite applies _pragma DSN options
// on every new connection, but pinning to one connection avoids any
// surprise about that).
type Store struct {
	db *sql.DB
}

// expectedPragmas lists the pragmas that Open applies and verifies. The
// values are the literal strings PRAGMA returns: SQLite reports
// journal_mode as a lowercase string, and foreign_keys / busy_timeout /
// synchronous as integers (rendered here as their decimal text form).
//
// foreign_keys=1 is enabled today even though the configs table has no
// FK declarations yet: the upcoming users table (issue #7) will land
// the configs.user_id -> users.id reference, and turning the pragma on
// at the DSN means it is already in force the moment that migration
// applies. v1 semantic review issue #6 for #6 flagged this as
// decorative-for-now; this comment pins the rationale so a future
// reader does not delete the pragma during cleanup.
var expectedPragmas = []struct {
	name string // pragma name (e.g. "journal_mode")
	want string // expected lower-case textual form returned by PRAGMA <name>
}{
	{name: "journal_mode", want: "wal"},
	{name: "foreign_keys", want: "1"},
	{name: "busy_timeout", want: "5000"},
	{name: "synchronous", want: "1"},
}

// Open creates (or opens) the SQLite database at dbPath, applies the
// fixed set of pragmas, runs every embedded migration that has not yet
// been recorded, and returns a ready-to-use [Store].
//
// The parent directory of dbPath is created with mode 0o750 if it does
// not already exist. The DSN supplies all four pragmas via
// `?_pragma=...` so they take effect immediately on every connection;
// each one is then verified with a follow-up PRAGMA query and a
// mismatch is reported as a wrapped error.
//
// If migrations or pragma verification fail, Open closes the underlying
// *sql.DB before returning the error.
func Open(ctx context.Context, dbPath string) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("sqlite.Open: dbPath must not be empty")
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		return nil, fmt.Errorf("sqlite.Open: ensure data dir: %w", err)
	}

	dsn := dbPath + "?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		// _txlock=immediate makes every BeginTx in this package
		// emit `BEGIN IMMEDIATE` (instead of the SQLite default
		// `BEGIN DEFERRED`) so the write lock is taken up-front
		// and the SELECT-of-modified_at + UPDATE in UpdateConfig
		// cannot race a concurrent writer slipping a new
		// modified_at between the read and the write. modernc.org/sqlite
		// silently ignores sql.TxOptions.Isolation, so the DSN-level
		// option is the only way to control begin mode; see
		// modernc.org/sqlite/sqlite.go's _txlock parser. Pinning
		// it here also keeps the contract honest if the
		// MaxOpenConns=1 serialisation below is ever relaxed.
		// Addresses v1 semantic review issue #2 for #8 + #9.
		"&_txlock=immediate"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite.Open: open db: %w", err)
	}

	// SQLite is a single-writer database; serialising on one connection
	// keeps semantics simple and ensures the per-connection pragmas
	// applied via the DSN are always in force.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	// Force the driver to actually open a connection so a bad path or
	// permission failure surfaces here rather than at first query time.
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite.Open: ping db: %w", err)
	}

	if err := verifyPragmas(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := applyMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite.Open: apply migrations: %w", err)
	}

	// Tighten file modes AFTER PingContext + applyMigrations because
	// SQLite creates the main DB file on first connection and creates
	// the -wal / -shm sidecars on the first WAL-mode write (which the
	// schema_migrations bookkeeping insert in applyMigrations forces).
	// MkdirAll already enforced 0o750 on the parent; the files
	// themselves would otherwise inherit the process umask (commonly
	// 0o644 on Linux), exposing encrypted credential blobs and the
	// in-flight WAL contents to any other local user. A chmod failure
	// fails Open closed: we close the db and propagate the wrapped
	// error so the operator sees the misconfiguration during startup
	// rather than running an exposed install. Addresses v1 semantic
	// review issue #2 for #6. The chmod call is a no-op on Windows;
	// the companion test t.Skips on that platform.
	if err := tightenDBFileMode(dbPath); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite.Open: tighten db file mode: %w", err)
	}

	return &Store{db: db}, nil
}

// DBSidecarSuffixes lists the suffixes SQLite appends to the main DB
// path for its WAL journal and shared-memory files. Exported so the
// regression test in package sqlite_test can stat exactly the set of
// paths the production code chmods, keeping both call sites pinned to
// a single source of truth.
var DBSidecarSuffixes = []string{"-wal", "-shm"}

// tightenDBFileMode sets the main DB file and any present -wal / -shm
// sidecars to 0o600 so encrypted credential blobs and WAL-resident
// in-flight transactions are not world-readable on a multi-user host.
// Sidecars that do not exist yet are silently skipped; a sidecar that
// exists but cannot be chmodded is reported as an error so the caller
// fails closed (the operator sees the misconfiguration during startup
// rather than running an exposed install). The implementation is
// deliberately tolerant of platforms where chmod has no real effect
// (Windows): the chmod call returns nil there because the underlying
// syscall is a stub, and the DB file is the only path the function
// strictly requires to exist.
func tightenDBFileMode(dbPath string) error {
	if err := os.Chmod(dbPath, 0o600); err != nil {
		return fmt.Errorf("chmod db file: %w", err)
	}
	for _, suffix := range DBSidecarSuffixes {
		p := dbPath + suffix
		if _, err := os.Stat(p); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat sidecar %s: %w", suffix, err)
		}
		if err := os.Chmod(p, 0o600); err != nil {
			return fmt.Errorf("chmod sidecar %s: %w", suffix, err)
		}
	}
	return nil
}

// verifyPragmas issues `PRAGMA <name>` against the open database for
// every entry in [expectedPragmas] and reports a wrapped error if any
// pragma's reported value does not match the expected one. Verifying
// after Open guards against a future driver change silently dropping a
// _pragma DSN option.
func verifyPragmas(ctx context.Context, db *sql.DB) error {
	for _, p := range expectedPragmas {
		var got string
		// PRAGMA names are validated by being members of expectedPragmas
		// (a hard-coded allowlist), so this is not a SQL injection
		// concern.
		row := db.QueryRowContext(ctx, "PRAGMA "+p.name)
		if err := row.Scan(&got); err != nil {
			return fmt.Errorf("sqlite.Open: read pragma %s: %w", p.name, err)
		}
		if !strings.EqualFold(got, p.want) {
			return fmt.Errorf("sqlite.Open: pragma %s = %q, want %q", p.name, got, p.want)
		}
	}
	return nil
}

// Close releases the underlying *sql.DB. It is safe to call on a nil
// receiver and on an already-closed Store; subsequent calls return the
// same terminal error from database/sql.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// nowFn is the clock seam every write path in this package routes
// through. Tests in package sqlite_test swap it for the duration of a
// single test via the in-package helper SetClockForTest in
// export_test.go; production code never reassigns it. Centralising the
// clock here means CreateConfig and UpdateConfig agree on a single
// source of "now", which the strictly-monotonic modified_at contract in
// UpdateConfig depends on (a backwards clock skew falls back to
// old + 1ms only when the seam returns a time at or before the row's
// existing modified_at).
var nowFn func() time.Time = time.Now

// nowRFC3339Nano returns nowFn() in UTC formatted as RFC3339Nano. It is
// extracted so the migrations runner and CreateConfig produce identical
// timestamps and so the format can be changed in one place if the
// schema ever evolves. UpdateConfig formats its own timestamp directly
// (it needs the parsed time.Time value to compute old + 1ms) and so
// does not call this helper.
func nowRFC3339Nano() string {
	return nowFn().UTC().Format(time.RFC3339Nano)
}

// CreateConfig inserts a new row owned by userID and returns it.
//
// ContentCiphertext and ContentNonce must both be non-empty;
// LastUsedWithVersion may be empty (stored as SQL NULL on disk). The
// row's CreatedAt and ModifiedAt are set to the same wall-clock instant
// in UTC.
func (s *Store) CreateConfig(ctx context.Context, userID int64, in store.CreateConfigInput) (store.Config, error) {
	if len(in.ContentCiphertext) == 0 || len(in.ContentNonce) == 0 {
		return store.Config{}, fmt.Errorf("%w: ciphertext and nonce are required", store.ErrInvalidPatch)
	}

	now := nowRFC3339Nano()

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO configs
            (user_id, name, content_ciphertext, content_nonce, last_used_with_version, created_at, modified_at)
         VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID,
		in.Name,
		in.ContentCiphertext,
		in.ContentNonce,
		nullIfEmpty(in.LastUsedWithVersion),
		now,
		now,
	)
	if err != nil {
		return store.Config{}, fmt.Errorf("sqlite: insert config: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return store.Config{}, fmt.Errorf("sqlite: last insert id: %w", err)
	}

	return s.GetConfig(ctx, userID, id)
}

// GetConfig returns the row identified by configID IFF it is owned by
// userID. A row that exists but belongs to a different user is reported
// as [store.ErrConfigNotFound].
func (s *Store) GetConfig(ctx context.Context, userID, configID int64) (store.Config, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, user_id, name, content_ciphertext, content_nonce,
                last_used_with_version, created_at, modified_at
           FROM configs
          WHERE id = ? AND user_id = ?`,
		configID, userID,
	)
	cfg, err := scanConfig(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.Config{}, store.ErrConfigNotFound
		}
		return store.Config{}, err
	}
	return cfg, nil
}

// ListConfigsByUser returns every row owned by userID in ascending ID
// order. An empty result set is returned as an empty slice and a nil
// error.
func (s *Store) ListConfigsByUser(ctx context.Context, userID int64) ([]store.Config, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, name, content_ciphertext, content_nonce,
                last_used_with_version, created_at, modified_at
           FROM configs
          WHERE user_id = ?
          ORDER BY id ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list configs: %w", err)
	}
	defer rows.Close()

	out := make([]store.Config, 0)
	for rows.Next() {
		cfg, err := scanConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list configs: %w", err)
	}
	return out, nil
}

// UpdateConfig applies patch to the row identified by configID IFF it
// is owned by userID. Cross-user access is reported as
// [store.ErrConfigNotFound]. A patch where exactly one of
// ContentCiphertext / ContentNonce is set is reported as
// [store.ErrInvalidPatch] and the row is left untouched.
//
// ModifiedAt is normally bumped on every successful update, even if no
// other column changed value, and is strictly greater than the row's prior
// ModifiedAt. The encrypted-store idempotency path can instead request a
// metadata-only PreserveModifiedAt update guarded by ExpectedModifiedAt.
// That compare-and-set runs inside the same BEGIN IMMEDIATE transaction;
// a mismatch returns [store.ErrConfigChanged] without mutating the row.
func (s *Store) UpdateConfig(ctx context.Context, userID, configID int64, patch store.UpdateConfigPatch) (store.Config, error) {
	hasCipher := len(patch.ContentCiphertext) > 0
	hasNonce := len(patch.ContentNonce) > 0
	if hasCipher != hasNonce {
		// Reject the patch BEFORE acquiring a write lock so an invalid
		// patch never even opens a transaction.
		return store.Config{}, fmt.Errorf("%w: ciphertext and nonce must be set together", store.ErrInvalidPatch)
	}
	if patch.PreserveModifiedAt {
		if patch.ExpectedModifiedAt == nil || patch.Name != nil || hasCipher {
			return store.Config{}, fmt.Errorf("%w: preserve-modified-at requires expected timestamp and metadata-only patch", store.ErrInvalidPatch)
		}
	} else if patch.ExpectedModifiedAt != nil {
		return store.Config{}, fmt.Errorf("%w: expected timestamp requires preserve-modified-at", store.ErrInvalidPatch)
	}

	// The Open DSN sets `_txlock=immediate` so this BeginTx emits
	// `BEGIN IMMEDIATE`, which acquires the write lock up-front
	// and keeps the SELECT-of-modified_at and the UPDATE on the
	// same connection without giving any other writer a chance to
	// slip a new modified_at between the read and the write.
	// (modernc.org/sqlite silently ignores sql.TxOptions.Isolation,
	// so passing sql.LevelSerializable here is decorative; the DSN
	// option is what actually controls begin mode. The
	// `MaxOpenConns=1` pool also serialises writers today, but the
	// explicit IMMEDIATE keeps the contract self-evident if the
	// pool is ever relaxed.) Addresses v1 semantic review issue
	// #2 for #8 + #9.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return store.Config{}, fmt.Errorf("sqlite: begin update tx: %w", err)
	}
	// Rollback is a no-op once Commit has succeeded (database/sql
	// returns sql.ErrTxDone, which we deliberately ignore here).
	defer func() { _ = tx.Rollback() }()

	var oldModifiedAt string
	if err := tx.QueryRowContext(ctx,
		`SELECT modified_at FROM configs WHERE id = ? AND user_id = ?`,
		configID, userID,
	).Scan(&oldModifiedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return store.Config{}, store.ErrConfigNotFound
		}
		return store.Config{}, fmt.Errorf("sqlite: read modified_at: %w", err)
	}
	old, err := time.Parse(time.RFC3339Nano, oldModifiedAt)
	if err != nil {
		return store.Config{}, fmt.Errorf("sqlite: parse old modified_at: %w", err)
	}

	if patch.PreserveModifiedAt && !old.Equal(patch.ExpectedModifiedAt.UTC()) {
		return store.Config{}, store.ErrConfigChanged
	}

	setClauses := make([]string, 0, 4)
	args := make([]any, 0, 6)
	if !patch.PreserveModifiedAt {
		candidate := nowFn().UTC()
		if !candidate.After(old) {
			// Wall clock did not advance (or jumped backwards). Step
			// strictly past the row's existing modified_at by exactly 1ms
			// so subsequent reads still see strictly-monotonic timestamps.
			candidate = old.Add(time.Millisecond)
		}
		setClauses = append(setClauses, "modified_at = ?")
		args = append(args, candidate.Format(time.RFC3339Nano))
	}

	if patch.Name != nil {
		setClauses = append(setClauses, "name = ?")
		args = append(args, *patch.Name)
	}
	if hasCipher {
		setClauses = append(setClauses, "content_ciphertext = ?", "content_nonce = ?")
		args = append(args, patch.ContentCiphertext, patch.ContentNonce)
	}
	if patch.LastUsedWithVersion != nil {
		setClauses = append(setClauses, "last_used_with_version = ?")
		args = append(args, nullIfEmpty(*patch.LastUsedWithVersion))
	}

	if len(setClauses) > 0 {
		args = append(args, configID, userID)
		// #nosec G202 -- setClauses contains only the hardcoded column fragments above; all values remain parameterized.
		stmt := "UPDATE configs SET " + strings.Join(setClauses, ", ") + " WHERE id = ? AND user_id = ?"
		if _, err := tx.ExecContext(ctx, stmt, args...); err != nil {
			return store.Config{}, fmt.Errorf("sqlite: update config: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return store.Config{}, fmt.Errorf("sqlite: commit update: %w", err)
	}
	// Re-load via GetConfig outside the transaction. A later concurrent
	// writer may already be visible here, which is acceptable: callers
	// receive at least the state whose conditional update just committed.
	return s.GetConfig(ctx, userID, configID)
}

// DeleteConfig removes the row identified by configID IFF it is owned
// by userID. Cross-user access and missing rows both report
// [store.ErrConfigNotFound].
func (s *Store) DeleteConfig(ctx context.Context, userID, configID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM configs WHERE id = ? AND user_id = ?`,
		configID, userID,
	)
	if err != nil {
		return fmt.Errorf("sqlite: delete config: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: rows affected: %w", err)
	}
	if affected == 0 {
		return store.ErrConfigNotFound
	}
	return nil
}

// CountConfigsByUser returns the number of configuration rows owned by
// userID. Used for quota enforcement before creating a new config.
func (s *Store) CountConfigsByUser(ctx context.Context, userID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM configs WHERE user_id = ?`,
		userID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("sqlite: count configs: %w", err)
	}
	return count, nil
}

// rowScanner is the subset of *sql.Row / *sql.Rows used by [scanConfig].
// Accepting either type keeps the row decoder usable from both
// QueryRow-style and Query-style call sites without duplicating the
// column list.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanConfig decodes a single row produced by the SELECT in GetConfig
// or ListConfigsByUser. It maps SQL NULL last_used_with_version to an
// empty Go string and parses the timestamp columns as RFC3339Nano,
// wrapping any parse error so callers can attribute it.
func scanConfig(rs rowScanner) (store.Config, error) {
	var (
		cfg        store.Config
		lastUsed   sql.NullString
		createdAt  string
		modifiedAt string
	)
	if err := rs.Scan(
		&cfg.ID,
		&cfg.UserID,
		&cfg.Name,
		&cfg.ContentCiphertext,
		&cfg.ContentNonce,
		&lastUsed,
		&createdAt,
		&modifiedAt,
	); err != nil {
		return store.Config{}, err
	}
	if lastUsed.Valid {
		cfg.LastUsedWithVersion = lastUsed.String
	}
	t, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return store.Config{}, fmt.Errorf("sqlite: parse created_at: %w", err)
	}
	cfg.CreatedAt = t
	t, err = time.Parse(time.RFC3339Nano, modifiedAt)
	if err != nil {
		return store.Config{}, fmt.Errorf("sqlite: parse modified_at: %w", err)
	}
	cfg.ModifiedAt = t
	return cfg, nil
}

// nullIfEmpty wraps s in a sql.NullString so an empty Go string is
// persisted as SQL NULL. The reverse mapping happens in [scanConfig].
func nullIfEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
