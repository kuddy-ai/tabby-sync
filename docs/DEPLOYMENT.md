# Deployment Guide

This guide covers deploying tabby-sync with Docker Compose and Caddy for
automatic TLS. For alternative setups, adapt the principles below.

## Prerequisites

- Docker Engine 24+ and Docker Compose v2
- A domain name pointing to your server (for auto-TLS)
- Ports 80 and 443 available (Caddy needs these for ACME challenges)

## Quick Start

```bash
# 1. Clone the repository
git clone https://github.com/kuddy-ai/tabby-sync.git
cd tabby-sync

# 2. Create your environment file
cp .env.example .env
# Edit .env — no secrets go here; they're set in docker-compose.yml

# 3. Update the Caddyfile with your domain
# Replace "sync.example.com" in Caddyfile with your actual domain

# 4. Start everything
docker compose up -d

# 5. Check health
curl https://sync.example.com/healthz
# Should return: ok

# 6. Retrieve your auto-generated token (single-user deployments)
docker compose exec tabby-sync cat /data/token.txt
# Paste this into Tabby desktop > Settings > Config sync.

# 7. (Optional but recommended) Remove the plaintext copy now that you've
# saved it in Tabby. token.txt isn't needed for the server to keep running
# - only users.yml's hash is - and leaving it in place means it ends up in
# any future backup/snapshot of the volume.
docker compose exec tabby-sync rm /data/token.txt
```

The image bootstraps itself on first boot: if `/data/users.yml` doesn't
already exist when the container starts, the Go `bootstrap` command validates
the requested display name with the same Unicode rules used by the server,
generates one random-token user, and writes the plaintext token to
`/data/token.txt` (mode 600) inside the volume. The token is never printed to
container logs. See [Users File](#users-file) below for multi-user setups,
which still use `docs/users.yml.example` as a schema reference.

## Configuration

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `TABBY_SYNC_ADDR` | No | `:8080` | Listen address inside the container |
| `TABBY_SYNC_DATA_DIR` | Yes | — | Path to data directory (`/data` in container) |
| `TABBY_SYNC_USERS_FILE` | Yes | — | Path to `users.yml` (`/data/users.yml` in container) |
| `TABBY_SYNC_MASTER_KEY_PROVIDER` | Yes | — | `file` or `env` |
| `TABBY_SYNC_MASTER_KEY` | Only if provider=env | — | 64 hex characters |
| `APP_LOG_LEVEL` | No | `info` | One of: `error`, `warn`, `info`, `debug` |

### Master Key

On first startup with `TABBY_SYNC_MASTER_KEY_PROVIDER=file`, tabby-sync
auto-generates a 32-byte master key at `${TABBY_SYNC_DATA_DIR}/master.key`
(mode 0600). **Back this up immediately.** Loss of the master key means
permanent loss of all encrypted config data.

Alternatively, set `TABBY_SYNC_MASTER_KEY_PROVIDER=env` and supply the key
as 64 hex characters in `TABBY_SYNC_MASTER_KEY`.

### Users File

**Single user:** nothing to do — the entrypoint auto-generates
`/data/users.yml` with one random-token user on first boot if the file
doesn't already exist, and writes the plaintext token to `/data/token.txt`
(mode 600) so you can retrieve it with
`docker compose exec tabby-sync cat /data/token.txt`. This only happens
ahead of the `serve` subcommand, so `docker compose run tabby-sync version`
etc. won't create credentials as a side effect.

The generated user's display name defaults to `default`; set
`TABBY_SYNC_USER_NAME` (e.g. in `docker-compose.yml` or `.env`) before
first boot to use a different name instead of editing `users.yml` by
hand afterwards.

**Multiple users:** create `users.yml` yourself *before* first boot (the
auto-bootstrap only fires when the file is missing) — see
[`docs/users.yml.example`](./users.yml.example) for the schema. The default
`docker-compose.yml` uses a named volume (`tabby-data:/data`), not a host
bind mount, so there's no `./data` directory to drop the file into directly;
seed the volume with a throwaway container instead:

Seed it through the `tabby-sync` service image itself (not a plain `alpine`
container), so the file lands owned by the same non-root `tabby` user the
server runs as, consistent with how the entrypoint's own auto-bootstrap
writes it:

```bash
docker compose run --rm -T --no-deps --entrypoint sh tabby-sync \
  -c 'cat > /data/users.yml && chmod 600 /data/users.yml' \
  < docs/users.yml.example
# Now edit the seeded file's placeholder hash/entries before starting:
docker compose run --rm -T --no-deps --entrypoint sh tabby-sync -c 'cat /data/users.yml'
```

(If you've changed `docker-compose.yml` to use a host bind mount instead,
e.g. `./data:/data`, you don't need the throwaway container — just
`cp docs/users.yml.example ./data/users.yml` directly on the host.)

Each user needs:

- `id`: unique positive integer
- `name`: display name
- `token_prefix`: first 12 characters of the token (for identification)
- `token_hash`: SHA-256 hex of the full token
- `disabled`: `false` (or `true` to revoke access)

**Important**: After editing `users.yml`, restart the container for changes
to take effect.

## Docker Compose Architecture

```
┌─────────────────────────────────────────────┐
│  Internet                                   │
└─────────────┬───────────────────────────────┘
              │ :443 (TLS)
┌─────────────▼───────────────────────────────┐
│  Caddy (reverse proxy + auto-TLS)           │
│  - Obtains Let's Encrypt certificates       │
│  - Adds HSTS header                         │
└─────────────┬───────────────────────────────┘
              │ :8080 (plain HTTP, internal)
┌─────────────▼───────────────────────────────┐
│  tabby-sync (Go binary, non-root)           │
│  - Bearer-token auth                        │
│  - AES-256-GCM encryption at rest           │
│  - SQLite WAL storage                       │
└─────────────┬───────────────────────────────┘
              │
┌─────────────▼───────────────────────────────┐
│  /data volume (persistent)                  │
│  - tabby-sync.db (SQLite)                   │
│  - master.key                               │
│  - users.yml                                │
└─────────────────────────────────────────────┘
```

## Without Caddy (BYO Reverse Proxy)

If you already have nginx, Traefik, or another TLS-terminating proxy:

1. Remove the `caddy` service from `docker-compose.yml`
2. Point your proxy to `tabby-sync:8080`
3. Ensure your proxy adds HSTS and forwards `X-Request-Id`

## Backup

Use the repository script rather than resolving Docker's physical volume name.
It accesses `/data` through the Compose `tabby-sync` service, so it works with
the default project-scoped named volume and with a host bind mount.

```bash
scripts/backup.sh "backups/tabby-sync-$(date +%Y%m%d-%H%M%S)"
```

If the service is running, the script stops it so SQLite can checkpoint its WAL
and restarts it before returning. It builds a new backup directory atomically:

```text
tabby-sync-YYYYMMDD-HHMMSS/
├── tabby-sync.db
├── master.key
└── users.yml
```

The directory is mode `0700` and each file is mode `0600`. A missing or failed
copy removes the temporary directory and leaves no destination that could be
mistaken for a valid backup. `token.txt` is deliberately excluded.

The resulting directory contains everything needed to decrypt the database and
must be protected as sensitive data. **Store an additional master-key copy
separately from database backups.** If both are compromised together, the
attacker can decrypt all configs.

## Restore

Restore through the Compose service mount as well:

```bash
scripts/restore.sh backups/tabby-sync-YYYYMMDD-HHMMSS
docker compose up -d
curl https://sync.example.com/healthz
```

The script validates the three required non-empty files and the 32-byte master
key before changing `/data`. It stops a running `tabby-sync` service, stages the
new files with mode `0600`, preserves the old files for rollback, runs
`tabby-sync doctor`, and restores the previous files if validation fails. A
service that was running is restarted automatically; the explicit
`docker compose up -d` above also covers restoration into a fresh project
volume.

A successful restore removes any old `/data/token.txt`, because that plaintext
copy may not match the restored `users.yml`. Existing tokens saved in Tabby
continue to work when they match the restored credential hashes.

## Monitoring

- **Health endpoint**: `GET /healthz` returns `ok` (unauthenticated)
- **Container healthcheck**: Built into the Dockerfile and docker-compose
- **Logs**: `docker compose logs -f tabby-sync` (JSON structured logs)

## Upgrading

```bash
git pull
docker compose build
docker compose up -d
```

The SQLite schema is migrated automatically on startup. No manual migration
steps are needed.

## Troubleshooting

| Symptom | Check |
|---------|-------|
| Container exits immediately | `docker compose logs tabby-sync` — look for missing env vars or permissions |
| 401 on all requests | Verify `users.yml` exists and has correct token hashes |
| "failed to load master key" | Check `TABBY_SYNC_MASTER_KEY_PROVIDER` and file permissions |
| Caddy certificate errors | Ensure DNS points to the server and ports 80/443 are open |
| "rate limit exceeded" (429) | Wait 60 seconds or check for runaway client loops |
