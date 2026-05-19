# Security Model

This document describes what tabby-sync protects and what it does **not**
protect. Read this before deploying in any environment.

## Project Positioning

tabby-sync is a **self-hosted** configuration sync backend for
[Tabby Terminal](https://tabby.sh). It is designed for individuals, small
teams, and trusted internal environments. It is **not** a public SaaS.

## What tabby-sync Protects

| Threat | Mitigation |
|--------|-----------|
| **Database file leaked on its own** | Config content is encrypted at rest with AES-256-GCM. Without the master key, ciphertext blobs are unreadable. |
| **Single token compromise** | Each user authenticates with an independent Bearer token. A leaked token exposes only that user's configs; other users are unaffected. |
| **Cross-user access (horizontal privilege escalation)** | Every store query is scoped by `user_id`. The API returns 404 for any row not owned by the authenticated user — indistinguishable from "row does not exist". |
| **Transport-layer sniffing** | HTTPS via Caddy (or any TLS-terminating reverse proxy) encrypts data in transit. |
| **Log file leaked** | Logs never contain Authorization headers, token plaintext, config content, master key bytes, decrypted plaintext, or full request bodies. Filesystem paths are redacted. |
| **Brute-force / scanning** | Per-token and per-IP in-memory rate limiting (60 requests/minute). Oversized payloads rejected at the middleware layer (2 MB body cap). |
| **Abuse via config flooding** | Per-user quota of 50 configs. Rejected with HTTP 409 when exceeded. |

## What tabby-sync Does NOT Protect

| Threat | Why |
|--------|-----|
| **Malicious server administrator** | The operator who controls the host can read memory, swap the binary, or extract the master key. There is no protection against a hostile admin. |
| **Running server compromised (RCE)** | If an attacker gains code execution on the server process, they can read the master key from memory and decrypt all configs. |
| **Client device compromised** | tabby-sync trusts the client. Malware on the user's machine can steal the token and read/write configs freely. |
| **User stores sensitive data in config plaintext** | tabby-sync encrypts content at rest, but the server decrypts it transiently during API calls. The operator can observe plaintext in memory. Users should not store high-value secrets (passwords, private keys) in Tabby configs without additional client-side encryption. |
| **Token, master key, or DB backup leaked by the user** | tabby-sync cannot prevent an operator from mishandling credentials or backups. |
| **Untrusted third-party instance** | Do NOT use an instance operated by someone you don't trust. The operator has full access. |

## Encryption Design

- **Algorithm**: AES-256-GCM (authenticated encryption)
- **Key derivation**: HKDF-SHA256 per-user subkey from the master key
- **AAD (Additional Authenticated Data)**: 1-byte version + 8-byte user ID + 8-byte config ID (prevents cross-user/cross-config replay)
- **Nonce**: 12 bytes, freshly random per write
- **Master key storage**: file (`master.key`, mode 0600) or environment variable (64 hex characters)

See [`docs/CRYPTO.md`](./CRYPTO.md) for the full envelope specification.

## Authentication Design

- Bearer tokens: server-issued, 128+ bits of entropy
- Storage: SHA-256 hash + short prefix in `users.yml`
- Lookup: constant-time compare via `crypto/subtle`
- No password-based auth, no OAuth/OIDC (v0.1)

## What This Is NOT

- **Not zero-knowledge**: The server decrypts configs to serve them. The operator can see plaintext in memory.
- **Not end-to-end encrypted**: Encryption is at-rest only; the server is a trusted party during API operations.
- **Not a public SaaS**: No multi-tenancy, no public registration, no billing isolation.

## Backup and Recovery

- **Back up the master key separately from the database**. If the master key is lost, all encrypted configs become permanently unrecoverable.
- Back up `tabby-sync.db` and `users.yml` regularly.
- Store backups in a separate location from the running server.
- Test restoration periodically.

## Recommendations

1. Deploy behind HTTPS (Caddy auto-TLS or your own reverse proxy).
2. Restrict network access to trusted clients only.
3. Keep the master key backup in a secure, offline location.
4. Rotate user tokens periodically via `tabby-sync user rotate`.
5. Monitor access logs for unexpected patterns.
6. Keep the binary and dependencies up to date.
