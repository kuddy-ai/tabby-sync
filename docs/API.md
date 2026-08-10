# tabby-sync HTTP API (v1)

This document is the wire-format reference for the Tabby-compatible
config sync API mounted under `/api/1/`. Every application endpoint is gated
by the Bearer middleware. `GET /healthz` and browser CORS preflight requests
are handled without credentials; preflight does not execute an API handler.

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

Errors written by the authentication, API, and rate-limit layers carry a JSON
body of the form

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
| `quota exceeded`  | The user already owns 50 configs (409).                              |
| `content too large` | The decoded `content` field exceeds 2 MiB (413).                  |
| `rate limit exceeded` | The applicable in-memory bucket is empty (429).                  |
| `internal error`  | Unexpected server error; details are logged, never echoed (500).     |

The global request-body middleware runs before the API decoder. A body larger
than 2 MiB returns `413` with the plain-text body `request body too large`.
Standard `net/http` route and method errors may also be plain text. Clients
must therefore use the status code first and only parse JSON when the response
declares a JSON content type.

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
  at least millisecond precision. `modified_at` is the semantic config
  clock: changing `name` or `content` advances it strictly, by at least
  1ms when the wall clock has not advanced. Repeating the same name/content
  or changing only `last_used_with_version` preserves it, allowing clients
  to treat idempotent uploads as no remote config change.

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

`name` is required and must be non-empty; the complete JSON body is hard-capped
at 2 MiB by the request-size middleware. An empty `name`, an unknown
field, or malformed JSON returns `400`.

If the user already owns 50 configs, the server returns `409 quota exceeded`.

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
  a fresh nonce when the plaintext changes. An empty string is valid. The
  decoded field limit is 2 MiB, while the enclosing JSON request is also capped
  at 2 MiB, so JSON framing counts toward the practical request limit.
- `last_used_with_version`: set the recorded version. Sending `""`
  clears the field on disk; the next read returns `null`.

**200 OK**: the freshly-loaded `configResponse`. `modified_at` is strictly
newer when `name` or `content` changed. It is unchanged for an idempotent
name/content upload or a `last_used_with_version`-only update.

`400 Bad Request` for malformed bodies, `400 invalid request` for an
all-nil patch, `404 Not Found` for missing or cross-user rows OR a
non-numeric / non-positive `{id}`, and `413` for an oversized request or
decoded content field.

### `DELETE /api/1/configs/{id}`

Deletes the config.

- `204 No Content`: success, no response body.
- `404 Not Found`: row missing, owned by another user, or `{id}` is
  not a positive integer.

## Logging discipline

Per `docs/LOGGING_POLICY.md` and `AGENTS.md` §7 the api package never
logs the `Authorization` header, the bearer token plaintext, the
decrypted config content, the master-key bytes, or full request
bodies. Internal-error log lines carry only the structured fields `op`,
`user_id`, and optional `config_id`; wrapped store and operating-system errors
are deliberately omitted. Decrypt failures use the literal message
`decrypt failure` with no `err` field.

## Rate-limit boundary

Authenticated requests are limited per user to 60 requests per minute. Missing,
malformed, unknown, and disabled-user Bearer tokens return 401 before the
limiter and are not IP-limited by the application. This is intentional for the
supported private, self-hosted deployment model. If the HTTPS endpoint is
reachable from an untrusted network, the reverse proxy, firewall, or upstream
gateway must provide unauthenticated-request throttling and access controls.

Use CLI- or bootstrap-generated tokens, which contain 256 random bits. Do not
replace them with manually chosen, low-entropy credentials. Proxy-derived
client IPs are observability metadata only and never participate in
authentication or authorization decisions.
