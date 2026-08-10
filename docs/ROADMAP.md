# Roadmap

## Project position

tabby-sync is a self-hosted configuration-sync backend for
[Tabby Terminal](https://tabby.sh). It targets individuals, small teams, and
trusted internal environments. It is not a public SaaS and does not provide
public registration, billing isolation, or protection from a hostile server
operator.

## Current 1.x / `main` baseline

The following capabilities are implemented on the current `main` branch. See
the changelog and Releases page to determine which published version contains
each change:

- Tabby-compatible `/api/1/` config-sync API
- Hashed Bearer-token credentials in `users.yml`
- SQLite WAL persistence with per-user isolation and config quotas
- AES-256-GCM encryption at rest with per-user HKDF-SHA256 keys
- Idempotent config updates for stable multi-device sync clocks
- User-management and diagnostic CLI commands
- Structured logging, request IDs, size limits, security headers, and rate limits
- Docker, Docker Compose, Caddy, and zero-touch first-user bootstrap
- Compatibility, race, vulnerability, static-analysis, and container smoke tests
- Manually approved Release Please PRs and release-triggered artifact publishing

## Tracked reliability and security work

The issue tracker is the source of truth for active implementation work. The
project has no known open reliability or security follow-ups at the time of
this update. Closed Issues and the changelog describe completed work; this
document should not duplicate a release-by-release plan.

## Possible future directions

- PostgreSQL as an optional storage backend
- KMS-backed master-key providers
- User-file reload without a process restart
- Stronger compatibility coverage across multiple Tabby versions
- Signed images, provenance, and SBOM publication
- Additional deployment examples when there is a demonstrated operator need

## Explicit non-goals

- Public SaaS hosting or open registration
- Application-level perimeter defense for direct exposure to hostile public
  traffic, including unauthenticated IP throttling and hostile proxy networks
- Zero-knowledge or end-to-end-encryption claims
- A browser-based admin interface without a separately reviewed threat model
- Configuration conflict merging that diverges from Tabby client semantics
- Storing account-recovery secrets that can decrypt data without the master key

## Guiding principles

1. Security over surface area.
2. Compatibility over protocol invention.
3. Honest operational limits over marketing claims.
4. Simple, reversible deployment and upgrade paths.
