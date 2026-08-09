# Tabby Client Setup

This guide was verified against Tabby 1.0.235. Tabby UI wording may change in
later releases; the API paths remain documented in [`API.md`](./API.md).

## Prerequisites

- A current Tabby desktop release
- A tabby-sync endpoint with a certificate trusted by the operating system
- The full `tbs_...` token supplied by the administrator

Tabby refuses plaintext config-sync hosts. The URL must begin with `https://`.

## Configure the connection

1. Open **Settings → Config sync**.
2. Enter the sync host, for example `https://sync.example.com`.
   Do not add `/api/1`; Tabby appends it. A trailing slash is normalized by the
   client, but omitting it keeps the value unambiguous.
3. Paste the full value into **Secret sync token**.
4. Wait for the connection indicator to turn successful. Tabby verifies the
   token with `GET /api/1/user` and loads the remote config list.

## Select the first sync direction

The client is not enabled until a remote config ID is selected. Choose one of
these explicit first-sync actions:

- **Upload as a new config**: creates a remote row and uploads the local config.
- **Upload/Replace** on an existing row: overwrites that remote config with the
  current local config.
- **Download** on an existing row: overwrites the local config with the remote
  content.

Read the confirmation dialog carefully; upload and download intentionally have
opposite overwrite directions.

After a row is active, enable **Sync automatically**. Local config changes are
uploaded promptly. Tabby checks the remote `modified_at` value once per minute
and downloads when it observes a newer semantic config change.

## Multiple devices

Devices for the same user can use the same token and select the same remote
config ID. The server preserves `modified_at` for byte-identical name/content
uploads, which prevents an applied remote change from causing an endless
download/reapply/upload loop.

Concurrent real edits are last-write-wins. tabby-sync does not merge conflicting
YAML structures or retain config history.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Connection fails with 401 | Confirm the full token, user status, and that the server was restarted after token changes. |
| Host is rejected | Use `https://`; current Tabby clients reject plaintext HTTP. |
| Certificate error | Install a certificate trusted by the OS/Electron runtime; an untrusted self-signed certificate is not a production setup. |
| No configs appear | Verify the token, then query `GET /api/1/configs` with the same credentials. |
| Remote changes seem delayed | Automatic remote polling runs once every 60 seconds. |
| HTTP 429 | Wait for the `Retry-After` period and check for a runaway sync loop. |
| Repeated remote-change messages | Confirm the server is version 1.7.0 or newer and contains the idempotent PATCH fix tracked in Issue #62. |

The unauthenticated liveness check is `GET https://sync.example.com/healthz` and
returns `ok`. It does not verify database decryption or the supplied user token.

## Token rotation

1. Run `tabby-sync user rotate <name-or-id>` with the same runtime paths used by
   the server.
2. Save the new token immediately; it is shown once.
3. Restart tabby-sync so it reloads `users.yml`.
4. Replace the token on every device.

The old token remains accepted by an already-running process until step 3. After
the restart, the old token returns 401.

## Security notes

- Treat the token as a password and never paste it into Issues or logs.
- The server encrypts data at rest but processes plaintext in memory.
- Use only a server and TLS endpoint you trust.
- Avoid embedding high-value private keys or passwords directly in the synced
  Tabby configuration.
