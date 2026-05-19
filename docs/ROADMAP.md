# Roadmap

## Project Positioning

tabby-sync is a self-hosted configuration sync backend for
[Tabby Terminal](https://tabby.sh). It targets **individuals, small teams,
and trusted internal environments**. It is **not** a public SaaS, does not
offer public registration, and should not be operated by untrusted third
parties.

---

## v0.1 — Scope

The first release delivers a **minimal, secure, self-hostable** sync
backend that a single user or small team can deploy behind a reverse proxy
and point their Tabby client at.

### What v0.1 includes

- Tabby-compatible config sync HTTP API (`/api/1/`)
- Bearer-token authentication via `users.yml`
- SQLite storage with WAL mode
- AES-256-GCM encrypted-at-rest configuration content
- Per-user key derivation (HKDF-SHA256)
- CLI management (`serve`, `version`, `help`)
- Request body size limits and security headers
- Structured JSON logging with sensitive-field redaction
- Docker / Docker Compose deployment example
- Caddy reverse-proxy example
- CI pipeline (test, vet, gofmt, govulncheck, gosec)
- release-please automated changelog and versioning

### What v0.1 does NOT include

The following are explicitly **out of scope** for the first release to keep
the project focused and avoid premature complexity:

| Category | Item |
|----------|------|
| Auth | Public/open registration |
| Auth | OAuth / OIDC |
| Admin | Admin HTTP API |
| Admin | Admin Web UI |
| Storage | PostgreSQL backend |
| Storage | Configuration history / versioning |
| Features | Configuration sharing between users |
| Features | Configuration conflict merging |
| Features | Email notifications |
| Features | Account/token recovery mechanism |
| Multi-tenancy | Public SaaS multi-tenant capability |
| Security | KMS integration |
| Security | Complex audit hash chains |
| Security | Complex rate-limiting / anti-fraud system |
| Release | GoReleaser |
| Release | cosign signing |
| Release | SBOM generation |

---

## Future Considerations

The following items **may** be considered for post-v0.1 releases based on
user feedback and project needs. No timeline is committed.

### Likely next

- `init`, `user add`, `user rm`, `user rotate`, `doctor` CLI commands
- Per-user config quota enforcement (e.g. 50 configs)
- Per-token in-memory rate limiting
- End-to-end integration tests simulating Tabby client sync flow
- Comprehensive documentation (security model, deployment guide, client setup)

### Possibly later

- PostgreSQL as an alternative storage backend
- KMS-backed master key management
- More complete audit logging
- More sophisticated release pipeline (GoReleaser, cosign, SBOM)
- Richer deployment documentation (Kubernetes, Helm)
- Stricter compatibility test suite against multiple Tabby client versions
- SIGHUP-based `users.yml` hot-reload

### Explicitly not planned

- Public SaaS offering
- Zero-knowledge / full end-to-end encryption claims
- VPN deployment recommendations
- Interactive TUI
- Mobile client

---

## Guiding Principles

1. **Security over features** — every new surface is a new attack vector.
2. **Honesty over marketing** — document what we protect and what we don't.
3. **Simplicity over generality** — serve the self-hosted use case well
   before trying to serve everyone.
4. **Compatibility over innovation** — match Tabby client expectations
   exactly; don't invent new protocols.
