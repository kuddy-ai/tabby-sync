#!/bin/sh
# Zero-touch bootstrap wrapper around tabby-sync for a single-user deployment.
#
# On first boot (no users.yml in the data volume yet), the Go bootstrap command
# validates the user name, creates exactly one random-token user, and persists
# the plaintext token to /data/token.txt (mode 600). The token is never written
# to stdout (container logs are often less tightly controlled than the data
# volume); operators retrieve it via
# `docker exec <container> cat /data/token.txt`.
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
# Alpine's BusyBox ash supports pipefail even though POSIX sh does not.
# shellcheck disable=SC3040
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
            tabby-sync bootstrap "$TABBY_SYNC_USER_NAME"
            token_out="${TABBY_SYNC_DATA_DIR}/token.txt"

            echo "=================================================================="
            echo "tabby-sync: generated first-run credentials"
            echo "  token saved to: ${token_out} (inside the data volume, mode 600)"
            echo "  Retrieve it with: docker exec <container> cat ${token_out}"
            echo "  Paste it into Tabby desktop > Settings > Config sync."
            echo "  Once saved, it's safe to delete ${token_out} - nothing reads it"
            echo "  again, only users.yml's hash is checked at request time - and"
            echo "  removing it keeps this plaintext secret out of future backups."
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
            echo "tabby-sync: timed out waiting for concurrent bootstrap to finish." >&2
            echo "  This container is exiting; if no other tabby-sync process is" >&2
            echo "  actually running against this volume (e.g. a previous container" >&2
            echo "  was SIGKILLed mid-bootstrap and left a stale lock behind), remove" >&2
            echo "  the lock directory from the volume before restarting:" >&2
            echo "  docker run --rm -v <volume>:${TABBY_SYNC_DATA_DIR} alpine rmdir ${lock_dir}" >&2
            exit 1
        fi
    fi
fi

exec tabby-sync "$@"
