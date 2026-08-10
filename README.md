[English](README.md) | [简体中文](README.zh-CN.md)

# tabby-sync

`tabby-sync` is a lightweight, self-hosted configuration-sync backend for
[Tabby Terminal](https://tabby.sh). It is a single Go binary backed by SQLite,
Bearer-token authentication, and AES-256-GCM encryption at rest.

> [`AGENTS.md`](./AGENTS.md) is the only repository-level instruction file for
> AI coding agents. Contributors should also read [`CONTRIBUTING.md`](./CONTRIBUTING.md)
> and [`SECURITY.md`](./SECURITY.md).

## Status

The project is on the stable **1.x** release line. See the
[latest GitHub Release](../../releases/latest), [`CHANGELOG.md`](./CHANGELOG.md),
and [`docs/ROADMAP.md`](./docs/ROADMAP.md) for current status and planned work.

Release binaries and the GHCR image are produced by workflows added after
1.6.0, so published artifacts are available starting with release 1.7.0.
Release Please maintains a release PR as changes land on `main`; a maintainer
merges that PR manually when a release should be published.

## Compatibility and API

The six Tabby-compatible application endpoints are mounted under `/api/1/`
and require a Bearer token. `GET /healthz` and browser CORS preflight requests
are handled without credentials.

- Wire-format reference: [`docs/API.md`](./docs/API.md)
- Current Tabby setup flow: [`docs/CLIENT_SETUP.md`](./docs/CLIENT_SETUP.md)
- Deployment guide: [`docs/DEPLOYMENT.md`](./docs/DEPLOYMENT.md)

## Features

- Tabby-compatible config create, list, read, update, and delete operations
- Idempotent PATCH handling that avoids multi-device sync feedback loops
- Per-user Bearer tokens stored as hashes in `users.yml`
- SQLite WAL storage with per-user scoping and a 50-config quota
- AES-256-GCM encryption at rest with HKDF-SHA256 per-user keys
- `serve`, `init`, `doctor`, `user add`, `user rm`, and `user rotate` commands
- Structured logs, request IDs, body limits, security headers, and rate limits
- Zero-touch first-user bootstrap in the Docker image
- Docker Compose and Caddy deployment examples

## Requirements

- Go 1.25.12 or a newer security-patched compatible toolchain (`go.mod` is the
  source of truth)
- Git 2.34+
- Docker Engine 24+ and Docker Compose v2 for the container deployment
- A trusted HTTPS endpoint for the Tabby client

The repository is Go-only. Do not add JavaScript, Python, Rust, or other
toolchains without a dedicated Issue and maintainer approval.

## Docker quick start

```bash
git clone https://github.com/kuddy-ai/tabby-sync.git
cd tabby-sync
cp .env.example .env

# Replace sync.example.com in Caddyfile, then start the stack.
docker compose up -d

# First boot creates one user and saves its one-time plaintext token here.
docker compose exec tabby-sync cat /data/token.txt

# After saving the token in Tabby, remove the plaintext copy.
docker compose exec tabby-sync rm /data/token.txt
```

The named `tabby-data` volume persists `tabby-sync.db`, `master.key`, and
`users.yml` across container rebuilds. Read the backup and restore warnings in
[`docs/DEPLOYMENT.md`](./docs/DEPLOYMENT.md) before operating real data.

## Runtime configuration

| Variable | Required for source run | Container default | Description |
| --- | --- | --- | --- |
| `TABBY_SYNC_ADDR` | no | `:8080` | Listen address |
| `TABBY_SYNC_DATA_DIR` | yes | `/data` | SQLite and key directory |
| `TABBY_SYNC_USERS_FILE` | yes | `/data/users.yml` | User credential-hash file |
| `TABBY_SYNC_MASTER_KEY_PROVIDER` | yes | `file` | `file` or `env` |
| `TABBY_SYNC_MASTER_KEY` | only for `env` | none | 64 hexadecimal characters; secret |
| `TABBY_SYNC_USER_NAME` | no | `default` | Docker first-boot user name only |
| `APP_LOG_LEVEL` | no | `info` | `error`, `warn`, `info`, or `debug` |

`.env.example` contains no real secrets. The Go binary does not load `.env`
files itself; export variables in the shell or use a process supervisor. Docker
Compose reads the repository `.env` file.

## Local development

Install the repository hooks once:

```bash
bash scripts/setup-hooks.sh
# PowerShell: ./scripts/setup-hooks.ps1
```

Run the checks used by CI:

```bash
go mod download
go mod verify
gofmt -s -l .
go vet ./...
go test -race -count=1 ./...
govulncheck ./...
gosec ./...
```

`govulncheck` and `gosec` should use the versions pinned in
`.github/workflows/ci.yml`.

## Building and release artifacts

Use the Makefile to include version, commit, and build-date metadata:

```bash
make build VERSION=1.7.0
./bin/tabby-sync version
```

For releases from 1.7.0 onward, GitHub Releases attach:

- Linux amd64
- Linux arm64
- Windows amd64
- `SHA256SUMS`

The GHCR workflow publishes Linux amd64 images with full semver, major/minor,
and `latest` tags only after a GitHub Release is published:

```bash
docker pull ghcr.io/kuddy-ai/tabby-sync:1.7.0
```

Pull-request and manual workflow runs build verification artifacts but do not
publish release binaries or images.

## Repository layout

```text
.
├── .github/                   Issue/PR templates and Actions workflows
├── .githooks/                 Local commit and push safeguards
├── cmd/tabby-sync/            Binary entry point
├── docs/                      API, deployment, crypto, and policy references
├── internal/                  Application packages
├── scripts/                   Hook installers
├── AGENTS.md                  Authoritative AI-agent rules
├── CONTRIBUTING.md            Issue, branch, commit, and PR workflow
├── SECURITY.md                Security reporting and operator responsibilities
├── Dockerfile / docker-compose.yml / Caddyfile
└── go.mod / go.sum
```

## Contributing and security

- Every change starts with an Issue and is submitted through a PR.
- Release-note PRs use the override format in
  [`docs/RELEASE_PLEASE_POLICY.md`](./docs/RELEASE_PLEASE_POLICY.md).
- Never commit runtime databases, `users.yml`, `.env`, tokens, master keys, or
  real customer data.
- Report vulnerabilities privately through the process in
  [`SECURITY.md`](./SECURITY.md), not in a public Issue.
- Logging rules are defined in [`docs/LOGGING_POLICY.md`](./docs/LOGGING_POLICY.md).

## License

[MIT](./LICENSE)
