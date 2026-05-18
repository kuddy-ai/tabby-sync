# SQLite migrations

Each `*.sql` file in this directory is one applied-once unit of schema
change for the tabby-sync SQLite store. The runner that consumes them
lives in `../migrations.go`.

## Filename rules

- `NNN_short_name.sql` where `NNN` is a zero-padded three-digit version.
- Versions MUST start at `001` and increase contiguously. A gap or
  duplicate fails closed at startup.
- `short_name` matches `^[a-z0-9._-]+$`. Anything else is rejected.

## Migration content rules (read before adding a new file)

The runner splits each file on top-level `;` characters via the helper
`splitSQLStatements` in `../migrations.go`. The splitter is deliberately
simple, which means the SQL inside each file MUST NOT contain:

- Triggers, `CREATE PROCEDURE`/`CREATE FUNCTION` blocks, or any other
  construct that embeds `;` inside a `BEGIN ... END` body.
- String literals that contain `;` (for example a seed `INSERT` whose
  value is `'a;b'`).
- `--` line comments that contain `;`. Block comments (`/* ... */`)
  are also unsafe for the same reason.

If you genuinely need any of the above, replace `splitSQLStatements`
with a real tokenizer (or switch to one statement per file with no
embedded `;`) in the same change. The current splitter exists because
the bootstrap migration is plain DDL with none of the constructs
above; do not add a migration that violates this assumption without
upgrading the splitter first, or the failure mode will be "half a
migration applies, the other half fails with a syntax error and the
transaction rolls back the partial work".

The runner wraps each migration in its own transaction together with
the bookkeeping insert into `schema_migrations`, so a failed migration
leaves the database in a consistent state at the previous version.

This document was added in response to v1 semantic review issue #3 for
issue #6 to make the constraint visible at the place where future
migrations actually get authored.
