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

# 3. Create the data directory and users file
mkdir -p data
cp docs/users.yml.example data/users.yml
# Edit data/users.yml with your user entries (see docs/users.yml.example)

# 4. Update the Caddyfile with your domain
# Replace "sync.example.com" in Caddyfile with your actual domain

# 5. Start everything
docker compose up -d

# 6. Check health
curl https://sync.example.com/healthz
# Should return: ok
```

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

See [`docs/users.yml.example`](./users.yml.example) for the schema. Each
user needs:

- `id`: unique positive integer
- `name`: display name
- `token_prefix`: first ~8 characters of the token (for identification)
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

```bash
# Stop the container (ensures SQLite WAL is checkpointed)
docker compose stop tabby-sync

# Copy critical files
cp data/tabby-sync.db backups/tabby-sync-$(date +%Y%m%d).db
cp data/master.key backups/master-$(date +%Y%m%d).key
cp data/users.yml backups/users-$(date +%Y%m%d).yml

# Restart
docker compose start tabby-sync
```

**Store master key backups separately from database backups.** If both are
compromised together, the attacker can decrypt all configs.

## Restore

```bash
docker compose down
cp backups/tabby-sync-YYYYMMDD.db data/tabby-sync.db
cp backups/master-YYYYMMDD.key data/master.key
cp backups/users-YYYYMMDD.yml data/users.yml
docker compose up -d
```

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
