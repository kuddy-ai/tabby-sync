# Cryptographic Envelope and Master Key Handling

This document pins the on-disk format, key-handling policy, and operator
responsibilities for tabby-sync's encrypted-at-rest storage. It is the
canonical reference cross-linked from `SECURITY.md`.

## Threat Model

tabby-sync encrypts user-supplied configuration content before it
reaches the SQLite database, so a stolen `tabby-sync.db` (or its WAL /
shm sidecars) is not enough to recover the cleartext: an attacker also
needs the master key. The envelope does NOT protect against:

- a compromised binary or compromised host: the master key lives in
  process memory while the server is running, and any code with read
  access to the process address space can read it;
- a compromised filesystem when the file-provider's `master.key` lives
  next to the database;
- side-channel attacks on the host running the server.

Backing up the master key, restricting filesystem access, and hardening
the runtime are operator responsibilities. tabby-sync's responsibility
ends at the envelope itself.

## Envelope Construction

Every configuration row is sealed with the following construction:

```
master_key (32 bytes)
   |
   | HKDF-SHA256(salt = nil, info = "tabby-sync/v1/user/{userID}")
   v
subkey (32 bytes)            <-- per-user, deterministic
   |
   | AES-256-GCM
   v
ciphertext = Seal(plaintext)
   nonce  = 12 random bytes from crypto/rand
   AAD    = 17 bytes:
              [0]    = CryptoVersion = 0x01
              [1:9]  = big-endian int64 userID
              [9:17] = big-endian int64 configID
   tag    = appended to ciphertext (Go's crypto/cipher convention)
```

The 17-byte AAD binds every ciphertext to the
`(envelope-version, owning-user, owning-config)` tuple it was produced
for. Replaying a ciphertext under a different user or different config
id fails closed with `crypto.ErrDecrypt`. A future envelope version
bump can re-key without an out-of-band schema migration: rows written
under v1 stay decryptable while v2 rows carry the new version byte.

## Master Key Providers

tabby-sync supports two master-key providers, selected by
`TABBY_SYNC_MASTER_KEY_PROVIDER` (`file` or `env`).

### `file` provider (default; recommended)

- Location: `${TABBY_SYNC_DATA_DIR}/master.key`
- On-disk format: exactly 32 raw bytes. No header, no encoding, no
  length prefix.
- File mode: `0o600`, enforced explicitly via `os.Chmod` after the
  atomic temp+rename write so the operator's umask does not silently
  widen permissions.
- Parent directory mode: `0o700`, also enforced via `os.Chmod`.
- First-call behaviour: if `master.key` does not exist, the provider
  generates 32 random bytes from `crypto/rand`, writes them to a
  temporary file in the same directory, fsyncs the temp file, then
  renames it over the target path. A crash mid-way never leaves a
  half-written `master.key` on disk.
- Subsequent calls read the file and verify its size is exactly
  32 bytes; any other size is rejected with `keys.ErrInvalidLength`
  WITHOUT echoing the actual size or the file path.

### `env` provider

- Variable: `TABBY_SYNC_MASTER_KEY`
- Format: 64 hexadecimal characters (lowercase preferred; mixed case
  decoded case-insensitively).
- An empty value is rejected with `keys.ErrMissing`.
- A non-hex value is rejected with a generic error that does NOT echo
  the offending value.
- A decoded length other than 32 bytes is rejected with
  `keys.ErrInvalidLength`.

## Two-Step Write

SQLite assigns the row id via AUTOINCREMENT, so the canonical AAD
(which embeds the configID) cannot be known until the row has been
inserted. The encrypted-store wrapper therefore runs a two-step
write on `CreateConfigPlaintext`:

1. Encrypt the plaintext with a placeholder configID of `0`, INSERT
   the row, and read back the assigned id.
2. Re-encrypt the plaintext under the canonical
   `(userID, assignedConfigID)` AAD with a fresh nonce, then UPDATE
   the row in place.

If step 2 fails synchronously (Encrypt error, UPDATE error) the
wrapper attempts to DELETE the orphaned row so the database does not
retain an undecryptable record. A regression test in
`internal/store/encrypted` pins this rollback.

### Power-loss orphan window (known limitation)

Three failure modes between step 1 and step 2 leave a row whose AAD
is `(userID, 0)` instead of `(userID, assignedConfigID)`, which no
future read can open:

- a process kill (SIGKILL, OOM kill) between INSERT and UPDATE;
- a host crash or power loss between INSERT and UPDATE;
- a same-user `Get` or `List` that races a `Create` and observes the
  row before the UPDATE has landed (transient: the next read after
  step 2 lands works again).

The first two leave a permanent orphan; the third is a transient
window that closes once the UPDATE returns. Both surface to callers
as `crypto.ErrDecrypt` returned UNWRAPPED (fail-closed: the wrapper
refuses to return data it cannot authenticate).

The closure for the orphan window is to wrap INSERT and UPDATE in a
single `database/sql` transaction so the placeholder row is never
visible to readers and self-cleans on rollback. Implementing the
transaction requires either extending the `internal/store.Store`
interface with a `WithTx` primitive or moving the two-step write
inside the SQLite implementation; both are out of scope for issue
#10. The transaction-based fix is therefore deferred to a follow-up
issue.

### Operator recovery for an orphan row

Until the transactional write lands, an operator faced with a
permanent orphan row has these options:

- delete the SQLite row directly (`DELETE FROM configs WHERE id=?`)
  if the configID is known;
- rebuild the affected user's row set from the upstream client's
  local copy (Tabby's local config is the source of truth in normal
  operation);
- restore from a pre-incident backup of `tabby-sync.db` (which is
  why the BACKUP imperative below applies to the database too).

`ListConfigsByUserPlaintext` aborts on the first decrypt failure
(see below), so an orphan row currently bricks the affected user's
list path until the row is removed. This is a deliberate
fail-closed posture documented as the v1 List policy; see the next
section.

## List Failure Policy (v1)

`ListConfigsByUserPlaintext` iterates the underlying store's rows
in ascending ID order and aborts on the first row that fails to
decrypt, returning `crypto.ErrDecrypt` UNWRAPPED with no partial
result. The policy is fail-closed:

- a tampered or replayed row MUST NOT silently disappear from the
  caller's view (a partial result without a flagged integrity
  failure would let an attacker hide rows by corrupting them);
- a single orphan row from a power-loss event between the two-step
  write's INSERT and UPDATE bricks the rest of the user's list
  until the orphan is removed (see the operator recovery list
  above).

Alternatives considered and explicitly deferred to a follow-up
issue:

- **skip-and-tag**: continue iteration and return an additional
  `[]int64` of failed configIDs alongside the successfully-decrypted
  rows. Requires extending `EncryptedStore.ListConfigsByUserPlaintext`
  with a richer return shape; would let the upcoming HTTP layer
  (issue #8) surface a "n rows could not be decrypted" indicator
  while still serving the legitimate ones.
- **structured failure**: return a typed error carrying the failing
  configID(s). Conflicts with the current contract that returns the
  bare `crypto.ErrDecrypt` sentinel from List, which the encrypted
  store tests pin via `errors.Is`.

The fail-closed v1 policy is the conservative default; the policy
choice for the upcoming HTTP layer (issue #8) is left to that
issue's API design.

## Logging Discipline

Per `docs/LOGGING_POLICY.md` and `AGENTS.md` §7, the crypto and keys
packages emit no log records. The CLI wiring layer logs exactly one
structured `master key loaded` line at startup, carrying only the
`provider` field; no path, no key bytes, and no length are ever
written to logs.

## **BACKUP**

> **If the master key is lost, encrypted content cannot be recovered.
> There is no recovery path. Operators MUST back up `master.key` (or
> whatever feeds `TABBY_SYNC_MASTER_KEY`) before the first write, and
> re-back-up after every rotation. Back the master key up SEPARATELY
> from the database: a single backup that contains both the database
> and the master key is equivalent to a plaintext backup.**

See [`SECURITY.md`](../SECURITY.md) for the project-level security
policy and the documented incident response.
