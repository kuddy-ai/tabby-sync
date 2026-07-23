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
set -eu

: "${TABBY_SYNC_DATA_DIR:?TABBY_SYNC_DATA_DIR must be set}"
: "${TABBY_SYNC_USERS_FILE:?TABBY_SYNC_USERS_FILE must be set}"
: "${TABBY_SYNC_USER_NAME:=default}"

if [ "${1:-}" = "serve" ] && [ ! -f "$TABBY_SYNC_USERS_FILE" ]; then
    mkdir -p "$(dirname "$TABBY_SYNC_USERS_FILE")"
    mkdir -p "$TABBY_SYNC_DATA_DIR"

    token="tbs_$(od -An -vtx1 -N32 /dev/urandom | tr -d ' \n')"
    hash=$(printf '%s' "$token" | sha256sum | awk '{print $1}')
    prefix=$(printf '%s' "$token" | cut -c1-12)
    # Escape backslash and double-quote so a name containing YAML-special
    # characters (#, :, quotes) can't break the generated file.
    name_escaped=$(printf '%s' "$TABBY_SYNC_USER_NAME" | sed 's/\\/\\\\/g; s/"/\\"/g')

    # Every secret is written to a *.tmp path with mode 600 applied BEFORE
    # the rename, then moved into place with mv (atomic on the same
    # filesystem). This avoids two problems with write-then-chmod: a
    # window where the default umask leaves the file briefly
    # world/group-readable, and a crash mid-write leaving a partial file
    # in place that would satisfy the existence check above forever.
    #
    # users.yml is written LAST (and is the existence check this whole
    # block guards on) so that a crash at any earlier point leaves no
    # trace, and the next boot retries the full bootstrap from scratch
    # instead of getting stuck on a half-written state.
    token_out="${TABBY_SYNC_DATA_DIR}/token.txt"
    umask 077
    printf '%s\n' "$token" > "${token_out}.tmp"
    chmod 600 "${token_out}.tmp"
    mv "${token_out}.tmp" "$token_out"

    users_tmp="${TABBY_SYNC_USERS_FILE}.tmp"
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

    echo "=================================================================="
    echo "tabby-sync: generated first-run credentials for user '${TABBY_SYNC_USER_NAME}'"
    echo "  token saved to: ${token_out} (inside the data volume, mode 600)"
    echo "  Retrieve it with: docker exec <container> cat ${token_out}"
    echo "  Paste it into Tabby desktop > Settings > Config sync."
    echo "=================================================================="
fi

exec tabby-sync "$@"
