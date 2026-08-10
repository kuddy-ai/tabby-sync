# Security Model

This document describes the protections and operational boundaries of
tabby-sync. Read it before deploying the service.

## Deployment model

tabby-sync is intended for individuals, small teams, and trusted internal
environments. It is not a public SaaS. The server operator and host are trusted
with plaintext configuration data while requests are processed.

## What tabby-sync protects

| Threat | Protection |
| --- | --- |
| Database copied without the master key | Config content is encrypted at rest with AES-256-GCM. |
| One user token is compromised | Tokens are independent; store operations remain scoped to that user ID. |
| Cross-user config probing | Reads and mutations include `user_id`; missing and cross-user rows both return 404. |
| Network sniffing | A trusted HTTPS reverse proxy protects data in transit. Current Tabby clients require HTTPS. |
| Log disclosure | Application logs omit Authorization values, token plaintext, config content, key bytes, request bodies, and raw credential paths. |
| Authenticated request flooding | Authenticated requests use an in-memory per-user token bucket of 60 requests per minute. |
| Config flooding | Each user is limited to 50 configs; additional creates return HTTP 409. |
| Oversized requests | The server rejects request bodies larger than 2 MiB before handlers run. |

## Known boundaries

- Failed Bearer authentication currently returns 401 before the rate limiter,
  so invalid-token guessing is not yet IP-limited. This is tracked in
  [#70](https://github.com/kuddy-ai/tabby-sync/issues/70).
- Rate limits are process-local and reset on restart. Multiple replicas do not
  share buckets.
- The current trusted-proxy boundary has known deployment constraints tracked
  in [#68](https://github.com/kuddy-ai/tabby-sync/issues/68).
- Encryption protects a copied database from disclosure without the key; it
  does not protect a running process or host that already has the key.

## What tabby-sync does not protect

| Threat | Why |
| --- | --- |
| Malicious server administrator | The operator can inspect memory, replace the binary, or read the master key. |
| Host or process compromise | Code execution in the service context can access decrypted data and credentials. |
| Compromised Tabby device | A stolen token grants that user read/write access. |
| Secrets stored inside Tabby config | The server decrypts config content during API requests. |
| Database and master key leaked together | The attacker has everything needed to decrypt stored content. |
| Untrusted third-party instance | The remote operator controls the plaintext-processing endpoint. |

## Cryptography

- AES-256-GCM authenticated encryption
- HKDF-SHA256 per-user subkeys
- AAD: one version byte, eight-byte user ID, eight-byte config ID
- Fresh 12-byte random nonce for each non-idempotent content write
- File-based or environment-provided 32-byte master key

The canonical envelope and recovery discussion is in
[`docs/CRYPTO.md`](./CRYPTO.md).

## Authentication

- Server-generated `tbs_` tokens contain 256 random bits.
- `users.yml` stores a SHA-256 hash and short display prefix, never the token.
- Credential comparison uses `crypto/subtle`; map lookup itself is not claimed
  to be constant-time.
- User-file changes require a server restart before they take effect.
- There is no password authentication, OAuth, OIDC, or public registration.

## Backup and recovery

- Back up `master.key`, `tabby-sync.db`, and `users.yml`.
- Store the master key separately from database backups.
- Protect backup copies at least as strongly as the live files.
- Test restoration periodically; a backup that has never been restored is not
  a verified recovery path.

See [`docs/DEPLOYMENT.md`](./DEPLOYMENT.md) and the open hardening work in
[#63](https://github.com/kuddy-ai/tabby-sync/issues/63) before relying on the
example commands for production recovery.
