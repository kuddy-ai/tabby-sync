#!/bin/sh
# Zero-touch bootstrap wrapper around tabby-sync for a single-user deployment.
#
# On first boot (no users.yml in the data volume yet): generates a random
# token in the same tbs_<64 hex> format the upstream `user add` CLI uses,
# writes users.yml with exactly that one user, and persists the plaintext
# token to /data/token.txt (mode 600). The token is never written to stdout
# (container logs are often less tightly controlled than the data volume);
# operators retrieve it via `docker exec <container> cat /data/token.txt`.
# The master key needs no help here: tabby-sync's file provider already
# auto-generates it on first `serve` if TABBY_SYNC_MASTER_KEY_PROVIDER=file.
#
# Every subsequent boot: users.yml already exists, this block is skipped
# entirely, and we fall straight through to `exec tabby-sync serve`.
#
# Bootstrap only runs ahead of the `serve` subcommand. Any other
# subcommand (version, doctor, user ...) is an inspection/admin
# invocation, not a server boot, and should not have the side effect of
# creating credentials the operator didn't ask for.
#
# pipefail matters for the token/hash pipelines below: under plain
# `set -e`, a pipeline's exit status is only the LAST command's, so a
# failing `od` or `sha256sum` upstream of a successful `tr`/`awk` would
# otherwise go unnoticed and bootstrap could proceed with an
# empty/partial token. BusyBox ash (this image's /bin/sh) supports
# pipefail; if this script is ever run under a shell that doesn't, this
# line itself fails closed under set -e rather than silently no-op'ing.
set -euo pipefail

: "${TABBY_SYNC_DATA_DIR:?TABBY_SYNC_DATA_DIR must be set}"
: "${TABBY_SYNC_USERS_FILE:?TABBY_SYNC_USERS_FILE must be set}"
: "${TABBY_SYNC_USER_NAME:=default}"

if [ "${1:-}" = "serve" ] && [ ! -f "$TABBY_SYNC_USERS_FILE" ]; then
    mkdir -p "$(dirname "$TABBY_SYNC_USERS_FILE")"
    mkdir -p "$TABBY_SYNC_DATA_DIR"

    # mkdir is atomic on POSIX filesystems (exactly one caller can create a
    # given directory), so it doubles as a cross-process lock with no extra
    # dependency: if two containers/processes start against the same empty
    # volume at once, only one wins the mkdir and bootstraps; the other
    # waits for users.yml to appear instead of racing to write its own
    # token.txt/users.yml pair, which would leave the operator with a
    # token that doesn't match the effective users.yml.
    lock_dir="${TABBY_SYNC_DATA_DIR}/.bootstrap.lock"
    if mkdir "$lock_dir" 2>/dev/null; then
        trap 'rmdir "$lock_dir" 2>/dev/null || true' EXIT
        # Re-check inside the lock: another process may have finished
        # bootstrapping between our first check above and acquiring it.
        if [ ! -f "$TABBY_SYNC_USERS_FILE" ]; then
            token="tbs_$(od -An -vtx1 -N32 /dev/urandom | tr -d ' \n')"
            hash=$(printf '%s' "$token" | sha256sum | awk '{print $1}')
            prefix=$(printf '%s' "$token" | cut -c1-12)
            # Strip control characters (newline, CR, etc.) once, up front,
            # and reuse the result for both the YAML-escaped name below
            # AND the log line further down - printing the raw env var in
            # the log would let a name containing control characters
            # inject misleading lines into container logs.
            name_sanitized=$(printf '%s' "$TABBY_SYNC_USER_NAME" | tr -d '\000-\037\177')
            # Escape backslash and double-quote so the sanitized name
            # can't break the generated file's YAML structure.
            name_escaped=$(printf '%s' "$name_sanitized" | sed 's/\\/\\\\/g; s/"/\\"/g')

            # Every secret is written to a *.tmp path with mode 600
            # applied BEFORE the rename, then moved into place with mv
            # (atomic on the same filesystem). This avoids two problems
            # with write-then-chmod: a window where the default umask
            # leaves the file briefly world/group-readable, and a crash
            # mid-write leaving a partial file in place that would
            # satisfy the existence check above forever.
            #
            # users.yml is written LAST and is the only file the
            # existence check above guards on, so a crash between the
            # two writes leaves a stale token.txt on disk - harmless,
            # since the next boot retries the full bootstrap and
            # overwrites it with a fresh token before writing users.yml
            # again.
            #
            # The subshell scopes umask 077 to just these writes so it
            # does not leak into the exec'd tabby-sync process below and
            # affect the permissions of files it creates later (DB, WAL,
            # etc).
            token_out="${TABBY_SYNC_DATA_DIR}/token.txt"
            users_tmp="${TABBY_SYNC_USERS_FILE}.tmp"
            (
                umask 077
                printf '%s\n' "$token" > "${token_out}.tmp"
                chmod 600 "${token_out}.tmp"
                mv "${token_out}.tmp" "$token_out"

                cat > "$users_tmp" <<EOF
users:
  - id: 1
    name: "${name_escaped}"
    token_prefix: ${prefix}
    token_hash: ${hash}
    disabled: false
EOF
                chmod 600 "$users_tmp"
                mv "$users_tmp" "$TABBY_SYNC_USERS_FILE"
            )

            echo "=================================================================="
            echo "tabby-sync: generated first-run credentials for user '${name_sanitized}'"
            echo "  token saved to: ${token_out} (inside the data volume, mode 600)"
            echo "  Retrieve it with: docker exec <container> cat ${token_out}"
            echo "  Paste it into Tabby desktop > Settings > Config sync."
            echo "=================================================================="
        fi
        rmdir "$lock_dir" 2>/dev/null || true
        trap - EXIT
    else
        echo "tabby-sync: another process is bootstrapping this data volume; waiting..." >&2
        i=0
        while [ ! -f "$TABBY_SYNC_USERS_FILE" ] && [ "$i" -lt 50 ]; do
            sleep 0.2
            i=$((i + 1))
        done
        if [ ! -f "$TABBY_SYNC_USERS_FILE" ]; then
            echo "tabby-sync: timed out waiting for concurrent bootstrap to finish" >&2
            exit 1
        fi
    fi
fi

exec tabby-sync "$@"
