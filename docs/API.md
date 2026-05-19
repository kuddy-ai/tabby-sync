# tabby-sync HTTP API (v1)

This document is the wire-format reference for the Tabby-compatible
config sync API mounted under `/api/1/`. Every endpoint is gated by
the Bearer middleware; the only unauthenticated route on the server
is `GET /healthz`.

## Authentication

Every request to `/api/1/*` MUST carry an `Authorization` header of the
form

```
Authorization: Bearer <token>
```

The `Bearer ` scheme prefix is matched case-insensitively per
RFC 7235. A missing header, an unrecognised scheme, an unknown token,
or a token that resolves to a disabled user all return `401
Unauthorized` with a generic body and a `WWW-Authenticate` header; the
server intentionally does not distinguish these failure modes.

## Error shape

Every 4xx and 5xx response except `204 No Content` carries a JSON body
of the form

```json
{ "error": "<code>" }
```

with `Content-Type: application/json; charset=utf-8`. The codes are
stable and lowercase:

| Code              | Meaning                                                              |
| ----------------- | -------------------------------------------------------------------- |
| `unauthorized`    | Auth middleware rejected the request (401).                          |
| `bad request`     | Malformed JSON or unknown JSON field (400).                          |
| `invalid request` | Body parsed but is semantically invalid (empty name, all-nil patch). |
| `not found`       | Row missing, owned by another user, or `{id}` not a positive integer (404). |
| `internal error`  | Unexpected server error; details are logged, never echoed (500).     |

Cross-user access is intentionally indistinguishable from a missing
row: an unauthenticated probe cannot use response shape to enumerate
ids belonging to other users. For the same reason, a path id that
cannot resolve to a valid row (non-numeric, zero, or negative) also
returns `404 not found` rather than `400 bad request`; folding bad
ids into the same shape as cross-user / missing keeps the surface
uniform.

## Wire format: `configResponse`

Every config-bearing response (`POST`, `GET single`, `GET list`
elements, `PATCH`) uses the same JSON object:

```json
{
  "id": 42,
  "name": "primary",
  "content": "settings:\n  foo: 1\n",
  "last_used_with_version": "v1.4.2",
  "created_at": "2026-01-02T03:04:05.123456789Z",
  "modified_at": "2026-01-02T03:04:05.123456789Z"
}
```

Notes:

- `id` is a positive `int64` assigned by the server.
- `content` is the raw configuration plaintext; the encryption
  envelope is internal to the server and never leaks to the client.
- `last_used_with_version` is `null` when no value has been recorded.
  Setting it to the empty string `""` clears the field on disk; the
  next read will then return `null`.
- `created_at` / `modified_at` are RFC3339Nano timestamps in UTC with
  at least millisecond precision. `modified_at` is strictly
  monotonic per row: every successful `PATCH` advances it by at least
  1ms even when the wall clock has not, so clients can safely
  diff-by-`modified_at`.

## Endpoints

### `GET /api/1/user`

Returns a snapshot of the authenticated user.

**200 OK**

```json
{ "id": 1, "name": "alice", "active_config": null }
```

`active_config` is reserved for a future "currently selected config"
feature and is always `null` in this release.

### `GET /api/1/configs`

Lists every config owned by the authenticated user, in ascending id
order.

**200 OK**

```json
[
  { "id": 1, "name": "primary",  "content": "...", "last_used_with_version": null, "created_at": "...", "modified_at": "..." },
  { "id": 2, "name": "fallback", "content": "...", "last_used_with_version": "v1.4.2", "created_at": "...", "modified_at": "..." }
]
```

An empty list is serialised as the literal `[]` (never `null`).

### `POST /api/1/configs`

Creates a new, empty config for the authenticated user.

**Request**

```json
{ "name": "primary" }
```

`name` is required and must be non-empty; the body is hard-capped at
1 MiB by the request-size middleware. An empty `name`, an unknown
field, or malformed JSON returns `400`.

**201 Created**: a `configResponse` with the assigned id, an empty
`content`, and `last_used_with_version: null`.

### `GET /api/1/configs/{id}`

Returns the config with the given numeric id.

- `200 OK`: a `configResponse`.
- `404 Not Found`: row missing, owned by another user, or `{id}` is
  not a positive integer.

### `PATCH /api/1/configs/{id}`

Applies a partial update to the config. Every field in the request
body is optional, but at least one must be present; sending an
empty `{}` returns `400 invalid request`.

**Request**

```json
{
  "name": "renamed",
  "content": "settings:\n  bar: 2\n",
  "last_used_with_version": "v1.4.3"
}
```

- `name`: change the display name. Omitted fields are unchanged.
- `content`: re-encrypt under the same `(userID, configID)` AAD with
  a fresh nonce. An empty string is a valid plaintext.
- `last_used_with_version`: set the recorded version. Sending `""`
  clears the field on disk; the next read returns `null`.

**200 OK**: the freshly-loaded `configResponse`. `modified_at` is
strictly newer than the previous read.

`400 Bad Request` for malformed bodies, `400 invalid request` for an
all-nil patch, `404 Not Found` for missing or cross-user rows OR a
non-numeric / non-positive `{id}`.

### `DELETE /api/1/configs/{id}`

Deletes the config.

- `204 No Content`: success, no response body.
- `404 Not Found`: row missing, owned by another user, or `{id}` is
  not a positive integer.

## Logging discipline

Per `docs/LOGGING_POLICY.md` and `AGENTS.md` §7 the api package never
logs the `Authorization` header, the bearer token plaintext, the
decrypted config content, the master-key bytes, or full request
bodies. Internal-error log lines carry only the structured fields
`op`, `user_id`, optional `config_id`, and (for non-decrypt errors)
the wrapped error string already path-scrubbed by the underlying
layers. Decrypt failures use the literal message `decrypt failure`
with no `err` field so the raw error is intentionally not echoed.
