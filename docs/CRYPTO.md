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

## Atomic ID-Bound Create

SQLite assigns each row id with AUTOINCREMENT, while the canonical AAD needs
that final configID before encryption. The generic store contract resolves the
ordering with `CreateConfigWithID`:

1. SQLite starts `BEGIN IMMEDIATE` and inserts an uncommitted sentinel row to
   reserve the final id.
2. The store calls the encrypted layer's builder with that id. The builder
   encrypts once under canonical `(userID, assignedConfigID)` AAD and returns
   only opaque ciphertext and nonce bytes.
3. SQLite replaces the sentinel values and commits the completed row in the
   same transaction.

The sentinel row is never a placeholder-AAD ciphertext and is never visible to
another connection. A builder, update, process, or commit
failure rolls the transaction back, leaving no row. Readers therefore observe
either no row or one fully authenticated row. The callback-shaped contract
also keeps the boundary generic: the encrypted layer knows nothing about
SQLite, and the SQLite layer knows nothing about plaintext, keys, AAD, or the
envelope format. Finalisation retains the existing sync-clock rule:
`modified_at` is strictly later than `created_at`, including when the wall
clock stalls or moves backwards.

Deterministic store tests hold the transaction open while another connection
lists rows, and inject failures both in the builder and during finalisation.
Encryption tests verify that create uses the final configID and never falls
back to the former create/update/delete compensation path.

This change does not migrate existing rows or alter the schema or envelope. If
an installation upgraded from an older release and already contains an
undecryptable row, restore a known-good database backup or rebuild the affected
configuration from Tabby's local copy. Direct SQL deletion is deliberately not
recommended: encrypted rows cannot be identified as valid or invalid by their
stored shape alone, so guessing an id risks deleting healthy data.

## List Failure Policy (v1)

`ListConfigsByUserPlaintext` iterates the underlying store's rows
in ascending ID order and aborts on the first row that fails to
decrypt, returning `crypto.ErrDecrypt` UNWRAPPED with no partial
result. The policy is fail-closed:

- a tampered or replayed row MUST NOT silently disappear from the
  caller's view (a partial result without a flagged integrity
  failure would let an attacker hide rows by corrupting them);
- a single tampered, replayed, or legacy-invalid row fails the entire list
  rather than being silently hidden. Atomic ID-bound creation prevents new
  placeholder-AAD rows from entering this state.

Alternatives that were considered for the HTTP failure shape:

- **skip-and-tag**: continue iteration and return an additional `[]int64` of
  failed config IDs alongside successfully decrypted rows. This would require
  changing the encrypted-store and HTTP response contracts.
- **structured failure**: return a typed error carrying the failing
  configID(s). Conflicts with the current contract that returns the
  bare `crypto.ErrDecrypt` sentinel from List, which the encrypted
  store tests pin via `errors.Is`.

The current HTTP API maps a list decryption failure to a generic 500 and returns
no partial rows. Atomic creation removes the placeholder-row failure window
without weakening that fail-closed contract.

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
