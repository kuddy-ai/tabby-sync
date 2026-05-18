# Changelog

## [Unreleased]

### Added

- SQLite-backed `internal/store` layer: per-user `Config` CRUD interface (`CreateConfig`, `GetConfig`, `ListConfigsByUser`, `UpdateConfig`, `DeleteConfig`, `Close`), embedded migrations runner with a `schema_migrations` ledger, strict per-user row scoping that returns `store.ErrConfigNotFound` on cross-user access, and four pragmas applied at Open (`journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`, `synchronous=NORMAL`) verified after the DSN options are applied. Adds a single new dependency `modernc.org/sqlite v1.50.1` (pure-Go, CGO-free); the database file lives at `${TABBY_SYNC_DATA_DIR}/tabby-sync.db` and is opened by `runServe` before the listener binds (#6).
- HTTP middleware stack and server hardening: per-request `X-Request-Id` (with strict inbound allowlist), panic recovery with structured logging, JSON access log (excludes Authorization/Cookie/body), 1 MiB request body size cap (`middleware.DefaultMaxBodyBytes`), conservative security headers (`X-Content-Type-Options`, `Referrer-Policy`, `Cache-Control`, `Content-Security-Policy: frame-ancestors 'none'`, `X-Frame-Options: DENY`), `MaxHeaderBytes = 1 MiB`, and an `internal/auth.Middleware` placeholder contract with a no-op `auth.None()` constructor (#5).
- Initial Go project skeleton: `cmd/tabby-sync` entry point, `internal/{api,auth,config,server,store,store/sqlite,crypto,keys,version}` packages, `serve` / `version` / `help` subcommands, environment-driven configuration, slog structured logging, and a baseline `Makefile` (#4).
