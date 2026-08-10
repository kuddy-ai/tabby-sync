#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: scripts/backup.sh <new-backup-directory>" >&2
}

if [[ $# -ne 1 || -z "${1:-}" ]]; then
  usage
  exit 2
fi

requested=$1
parent=$(dirname -- "$requested")
name=$(basename -- "$requested")
if [[ "$name" == "." || "$name" == ".." || -z "$name" ]]; then
  echo "error: backup directory name is invalid" >&2
  exit 2
fi

mkdir -p -- "$parent"
parent=$(cd -- "$parent" && pwd -P)
destination="${parent}/${name}"
if [[ -e "$destination" || -L "$destination" ]]; then
  echo "error: backup destination already exists" >&2
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
tmp_dir=""
finish() {
  rc=$?
  trap - EXIT HUP INT TERM
  if [[ -n "$tmp_dir" && -d "$tmp_dir" ]]; then
    rm -rf -- "$tmp_dir"
  fi
  if [[ "$was_running" -eq 1 ]]; then
    if ! "${compose[@]}" start "$service" >/dev/null; then
      echo "error: backup finished but tabby-sync could not be restarted" >&2
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

umask 077
tmp_dir=$(mktemp -d "${parent}/.${name}.tmp.XXXXXX")
chmod 700 "$tmp_dir"

for file in tabby-sync.db master.key users.yml; do
  # $1 is expanded by the nested container shell, not this host shell.
  # shellcheck disable=SC2016
  "${compose[@]}" run --rm -T --no-deps --entrypoint sh "$service" \
    -c 'set -eu; test -f "/data/$1"; test ! -L "/data/$1"; test -s "/data/$1"; cat "/data/$1"' \
    sh "$file" > "${tmp_dir}/${file}"
  test -s "${tmp_dir}/${file}"
  chmod 600 "${tmp_dir}/${file}"
done

if [[ $(wc -c < "${tmp_dir}/master.key") -ne 32 ]]; then
  echo "error: master key backup has an unexpected size" >&2
  exit 1
fi

mv -- "$tmp_dir" "$destination"
tmp_dir=""
echo "Backup created: $destination"
