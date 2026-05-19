# Client Setup

This guide explains how to configure [Tabby Terminal](https://tabby.sh) to
sync settings with your self-hosted tabby-sync instance.

## Prerequisites

- Tabby Terminal installed (version 1.0.197+ recommended)
- A running tabby-sync instance accessible via HTTPS
- A Bearer token provided by your administrator

## Configuration Steps

### 1. Open Tabby Settings

Launch Tabby and navigate to: **Settings → Config Sync**

### 2. Set the Sync Host

Enter your tabby-sync instance URL:

```
https://sync.example.com
```

Do **not** include a trailing slash or the `/api/1/` path — Tabby appends
the API prefix automatically.

### 3. Set the Token

Paste the Bearer token your administrator provided. This is the full
token string (not just the prefix shown in `users.yml`).

The token is displayed only once when the administrator creates your
account. If you've lost it, ask for a token rotation.

### 4. Enable Sync

Toggle the sync switch to **On**. Tabby will immediately attempt to
connect and pull your configuration.

### 5. Verify

After enabling sync, check that:

- The sync status shows "Connected" or similar
- Your configs appear in the sync list
- Changes made on one device appear on others after a few seconds

## How Sync Works

1. **Initial sync**: Tabby creates a config entry via `POST /api/1/configs`
2. **Ongoing**: On each settings change, Tabby sends the full config via
   `PATCH /api/1/configs/{id}`
3. **Pull**: On startup, Tabby fetches the latest config via
   `GET /api/1/configs/{id}` and compares `modified_at`
4. **Conflict resolution**: Last-write-wins based on `modified_at` timestamp

## Multiple Devices

Each device uses the **same token** and syncs against the same config entry.
When you change settings on Device A, Device B picks up the change on its
next sync cycle (typically within seconds if Tabby is running).

## Troubleshooting

| Symptom | Solution |
|---------|----------|
| "Unauthorized" or 401 error | Verify your token is correct and not disabled |
| "Connection refused" | Check that the sync URL is reachable from your network |
| "Certificate error" | Ensure the server has valid TLS (or add an exception for self-signed certs in development) |
| Sync not updating | Check if the server is healthy: `curl https://sync.example.com/healthz` |
| "Rate limit exceeded" | You're syncing too frequently; wait a minute and retry |

## Security Notes

- Your token is a secret. Do not share it or commit it to version control.
- The sync server encrypts your config at rest, but the server operator
  can see your config in memory during API calls.
- Do not store highly sensitive secrets (SSH keys, passwords) directly in
  your Tabby config. Use references or environment variables instead.
- Only use a tabby-sync instance you trust. Do not connect to instances
  operated by unknown third parties.

## Token Rotation

If you suspect your token has been compromised:

1. Contact your administrator
2. They will run `tabby-sync user rotate <your-name>`
3. You'll receive a new token (shown only once)
4. Update the token in Tabby Settings → Config Sync
5. The old token is immediately invalidated
