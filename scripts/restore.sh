#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: scripts/restore.sh <backup-directory>" >&2
}

if [[ $# -ne 1 || -z "${1:-}" ]]; then
  usage
  exit 2
fi

source_dir=$1
if [[ ! -d "$source_dir" ]]; then
  echo "error: backup directory does not exist" >&2
  exit 1
fi
source_dir=$(cd -- "$source_dir" && pwd -P)

for file in tabby-sync.db master.key users.yml; do
  if [[ ! -f "${source_dir}/${file}" || -L "${source_dir}/${file}" || ! -s "${source_dir}/${file}" ]]; then
    echo "error: backup is missing a required non-empty file" >&2
    exit 1
  fi
done
if [[ $(wc -c < "${source_dir}/master.key") -ne 32 ]]; then
  echo "error: backup master key has an unexpected size" >&2
  exit 1
fi

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
cd "$repo_root"

compose=(docker compose)
service=tabby-sync
if ! "${compose[@]}" config --services | grep -Fxq "$service"; then
  echo "error: Compose service tabby-sync is unavailable" >&2
  exit 1
fi

was_running=0
finish() {
  rc=$?
  trap - EXIT HUP INT TERM
  if [[ "$was_running" -eq 1 ]]; then
    if ! "${compose[@]}" start "$service" >/dev/null; then
      echo "error: restore finished but tabby-sync could not be restarted" >&2
      rc=1
    fi
  fi
  exit "$rc"
}
trap finish EXIT
trap 'exit 130' HUP INT TERM

if "${compose[@]}" ps --status running --services | grep -Fxq "$service"; then
  was_running=1
  "${compose[@]}" stop "$service" >/dev/null
fi

# Variables inside this script body belong to the nested container shell.
# shellcheck disable=SC2016
tar -C "$source_dir" -cf - -- tabby-sync.db master.key users.yml |
  "${compose[@]}" run --rm -T --no-deps --entrypoint sh "$service" -c '
    set -eu
    umask 077

    new_dir=""
    old_dir=""
    install_started=0
    committed=0

    finish_restore() {
      rc=$?
      trap - EXIT HUP INT TERM
      if [ "$committed" -ne 1 ]; then
        if [ "$install_started" -eq 1 ]; then
          rm -f /data/tabby-sync.db /data/tabby-sync.db-wal \
            /data/tabby-sync.db-shm /data/tabby-sync.db-journal \
            /data/master.key /data/users.yml /data/token.txt
        fi
        if [ -n "$old_dir" ]; then
          for file in tabby-sync.db tabby-sync.db-wal tabby-sync.db-shm \
            tabby-sync.db-journal master.key users.yml token.txt; do
            if [ -e "$old_dir/$file" ]; then
              rm -rf "/data/$file"
              mv "$old_dir/$file" "/data/$file"
            fi
          done
        fi
      fi
      if [ -n "$new_dir" ]; then rm -rf "$new_dir"; fi
      if [ -n "$old_dir" ]; then rm -rf "$old_dir"; fi
      exit "$rc"
    }
    trap finish_restore EXIT
    trap "exit 130" HUP INT TERM

    new_dir=$(mktemp -d /data/.restore-new.XXXXXX)
    old_dir=$(mktemp -d /data/.restore-old.XXXXXX)

    tar -xf - -C "$new_dir"
    for file in tabby-sync.db master.key users.yml; do
      test -f "$new_dir/$file"
      test ! -L "$new_dir/$file"
      test -s "$new_dir/$file"
      chmod 600 "$new_dir/$file"
    done
    test "$(wc -c < "$new_dir/master.key")" -eq 32

    for file in tabby-sync.db tabby-sync.db-wal tabby-sync.db-shm \
      tabby-sync.db-journal master.key users.yml token.txt; do
      if [ -e "/data/$file" ]; then
        mv "/data/$file" "$old_dir/$file"
      fi
    done

    install_started=1
    mv "$new_dir/tabby-sync.db" /data/tabby-sync.db
    mv "$new_dir/master.key" /data/master.key
    mv "$new_dir/users.yml" /data/users.yml
    chmod 600 /data/tabby-sync.db /data/master.key /data/users.yml

    tabby-sync doctor >/dev/null

    committed=1
    rm -rf "$new_dir" "$old_dir"
    trap - EXIT HUP INT TERM
  '

echo "Restore completed from: $source_dir"
