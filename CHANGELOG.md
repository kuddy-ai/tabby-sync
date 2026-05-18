# Changelog

## [Unreleased]

### Added

- HTTP middleware stack and server hardening: per-request `X-Request-Id` (with strict inbound allowlist), panic recovery with structured logging, JSON access log (excludes Authorization/Cookie/body), 1 MiB request body size cap (`middleware.DefaultMaxBodyBytes`), conservative security headers (`X-Content-Type-Options`, `Referrer-Policy`, `Cache-Control`, `Content-Security-Policy: frame-ancestors 'none'`, `X-Frame-Options: DENY`), `MaxHeaderBytes = 1 MiB`, and an `internal/auth.Middleware` placeholder contract with a no-op `auth.None()` constructor (#5).
- Initial Go project skeleton: `cmd/tabby-sync` entry point, `internal/{api,auth,config,server,store,store/sqlite,crypto,keys,version}` packages, `serve` / `version` / `help` subcommands, environment-driven configuration, slog structured logging, and a baseline `Makefile` (#4).
