#!/bin/sh
# Zero-touch bootstrap wrapper around tabby-sync for a single-user deployment.
#
# On first boot (no users.yml in the data volume yet): generates a random
# token in the same tbs_<64 hex> format the upstream `user add` CLI uses,
# writes users.yml with exactly that one user, and persists the plaintext
# token to /data/token.txt (mode 600) so it can be read back out of the
# volume without ever touching the running container. The master key
# needs no help here: tabby-sync's file provider already auto-generates
# it on first `serve` if TABBY_SYNC_MASTER_KEY_PROVIDER=file.
#
# Every subsequent boot: users.yml already exists, this block is skipped
# entirely, and we fall straight through to `exec tabby-sync serve`.
set -eu

: "${TABBY_SYNC_DATA_DIR:?TABBY_SYNC_DATA_DIR must be set}"
: "${TABBY_SYNC_USERS_FILE:?TABBY_SYNC_USERS_FILE must be set}"
: "${TABBY_SYNC_USER_NAME:=default}"

if [ ! -f "$TABBY_SYNC_USERS_FILE" ]; then
    mkdir -p "$(dirname "$TABBY_SYNC_USERS_FILE")"

    token="tbs_$(od -An -vtx1 -N32 /dev/urandom | tr -d ' \n')"
    hash=$(printf '%s' "$token" | sha256sum | awk '{print $1}')
    prefix=$(printf '%s' "$token" | cut -c1-12)

    cat > "$TABBY_SYNC_USERS_FILE" <<EOF
users:
  - id: 1
    name: ${TABBY_SYNC_USER_NAME}
    token_prefix: ${prefix}
    token_hash: ${hash}
    disabled: false
EOF
    chmod 600 "$TABBY_SYNC_USERS_FILE"

    token_out="${TABBY_SYNC_DATA_DIR}/token.txt"
    printf '%s\n' "$token" > "$token_out"
    chmod 600 "$token_out"

    echo "=================================================================="
    echo "tabby-sync: generated first-run credentials for user '${TABBY_SYNC_USER_NAME}'"
    echo "  token: ${token}"
    echo "  (also saved to ${token_out} inside the data volume)"
    echo "  This is shown ONCE. Paste it into Tabby desktop > Settings > Config sync."
    echo "=================================================================="
fi

exec tabby-sync "$@"
