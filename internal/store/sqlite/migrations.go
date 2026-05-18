package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// migrationsFS embeds the SQL migration files shipped with this binary.
// New migrations are added by dropping a file named NNN_short_name.sql
// into the migrations/ directory, where NNN is a zero-padded three digit
// version number that strictly increases.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationFilenamePattern is the strict allowlist for migration
// filenames: a three-digit version, an underscore, a kebab/snake-case
// name, and a .sql extension. Anything else is rejected so a stray file
// dropped into the embed root cannot silently become a migration.
var migrationFilenamePattern = regexp.MustCompile(`^([0-9]{3})_([a-z0-9._-]+)\.sql$`)

// migration is one applied-once unit of schema change, materialised from
// a single .sql file under migrations/.
type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads every *.sql file under migrations/ from fsys,
// parses the version prefix, and returns the migrations sorted in
// ascending version order. Duplicate or non-monotonic versions are
// rejected so a copy-paste mistake fails closed at startup.
func loadMigrations(fsys embed.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	var migs []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationFilenamePattern.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("invalid migration filename: %q", e.Name())
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", e.Name(), err)
		}
		body, err := fs.ReadFile(fsys, "migrations/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", e.Name(), err)
		}
		migs = append(migs, migration{
			version: version,
			name:    m[2],
			sql:     string(body),
		})
	}

	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })

	for i := range migs {
		expected := i + 1
		if migs[i].version != expected {
			return nil, fmt.Errorf("migration versions must start at 001 and be contiguous; got version %d at index %d", migs[i].version, i)
		}
	}

	return migs, nil
}

// applyMigrations brings the database up to the latest embedded
// migration. It is idempotent: a second call is a no-op once every
// migration has been recorded in schema_migrations.
//
// Each migration is applied inside its own transaction together with the
// schema_migrations bookkeeping insert. If anything fails, the
// transaction is rolled back and the error is wrapped and returned.
func applyMigrations(ctx context.Context, db *sql.DB) error {
	migs, err := loadMigrations(migrationsFS)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}

	// Bootstrap the bookkeeping table outside a migration transaction so
	// the very first run has somewhere to record its progress. The
	// definition mirrors the one in 001_init.sql so the two cannot drift.
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := loadAppliedVersions(ctx, db)
	if err != nil {
		return fmt.Errorf("load applied migrations: %w", err)
	}

	for _, m := range migs {
		if _, ok := applied[m.version]; ok {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("apply migration %03d_%s: %w", m.version, m.name, err)
		}
	}
	return nil
}

// loadAppliedVersions returns the set of migration versions already
// recorded in schema_migrations.
func loadAppliedVersions(ctx context.Context, db *sql.DB) (map[int]struct{}, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]struct{})
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return applied, nil
}

// applyMigration runs a single migration inside a transaction. The .sql
// file is split on top-level semicolons so each statement is executed
// individually; this is the most portable approach across drivers and
// keeps the failure-attribution clean if a statement fails.
func applyMigration(ctx context.Context, db *sql.DB, m migration) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			// Rollback errors are intentionally swallowed so the original
			// migration error reaches the caller unchanged.
			_ = tx.Rollback()
		}
	}()

	for _, stmt := range splitSQLStatements(m.sql) {
		if _, err = tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec statement: %w", err)
		}
	}

	if _, err = tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version,
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// splitSQLStatements splits raw SQL on top-level semicolons. Trailing
// whitespace and empty statements are dropped so trailing newlines or
// blank lines between statements do not produce empty Exec calls. The
// splitter is intentionally simple: tabby-sync's migrations are plain
// DDL with no string literals containing semicolons, no triggers, and
// no procedural blocks.
func splitSQLStatements(raw string) []string {
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}
