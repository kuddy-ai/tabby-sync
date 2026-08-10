#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
tmp_dir=$(mktemp -d)
project_suffix=${GITHUB_RUN_ID:-$$}${GITHUB_RUN_ATTEMPT:-0}

export COMPOSE_FILE="${repo_root}/testdata/docker-compose.lifecycle.yml"
export COMPOSE_PROJECT_NAME="tabbysynclifecycle${project_suffix}"
export TABBY_SYNC_TEST_IMAGE=${TABBY_SYNC_TEST_IMAGE:-tabby-sync:smoke}

compose=(docker compose)

cleanup() {
  rc=$?
  trap - EXIT HUP INT TERM
  "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  rm -rf -- "$tmp_dir"
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

wait_healthy() {
  local deadline=$((SECONDS + 30))
  local ids=()
  local id
  while (( SECONDS < deadline )); do
    mapfile -t ids < <("${compose[@]}" ps -q tabby-sync)
    if [[ ${#ids[@]} -gt 0 ]]; then
      local all_healthy=1
      for id in "${ids[@]}"; do
        if [[ $(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$id") != "healthy" ]]; then
          all_healthy=0
          break
        fi
      done
      if [[ "$all_healthy" -eq 1 ]]; then
        return 0
      fi
    fi
    sleep 1
  done
  echo "error: lifecycle containers did not become healthy" >&2
  "${compose[@]}" logs --no-color tabby-sync >&2 || true
  return 1
}

assert_mode_600() {
  local container_id=$1
  docker exec "$container_id" sh -c '
    set -eu
    for file in /data/tabby-sync.db /data/master.key /data/users.yml; do
      test "$(stat -c %a "$file")" = 600
    done
  '
}

# Unicode whitespace must be rejected by the same strings.TrimSpace semantics
# used when the server loads users.yml. A regression would start the server and
# hang until timeout instead of failing before any credential file is written.
export TABBY_SYNC_TEST_USER_NAME=$'\u00a0'
set +e
timeout 10s "${compose[@]}" run --rm -T --no-deps tabby-sync serve \
  >"${tmp_dir}/invalid-name.stdout" 2>"${tmp_dir}/invalid-name.stderr"
invalid_rc=$?
set -e
if [[ "$invalid_rc" -eq 0 || "$invalid_rc" -eq 124 ]]; then
  echo "error: Unicode-whitespace bootstrap did not fail closed" >&2
  exit 1
fi
if grep -Eq 'tbs_[0-9a-f]{64}' "${tmp_dir}/invalid-name.stdout" "${tmp_dir}/invalid-name.stderr"; then
  echo "error: invalid bootstrap output leaked a token" >&2
  exit 1
fi
"${compose[@]}" run --rm -T --no-deps --entrypoint sh tabby-sync \
  -c 'test ! -e /data/users.yml && test ! -e /data/token.txt'

# A failed copy from an empty volume must leave no destination that could be
# mistaken for a valid backup.
if "${repo_root}/scripts/backup.sh" "${tmp_dir}/invalid-backup" \
  >"${tmp_dir}/invalid-backup.stdout" 2>"${tmp_dir}/invalid-backup.stderr"; then
  echo "error: backup unexpectedly succeeded without source files" >&2
  exit 1
fi
if [[ -e "${tmp_dir}/invalid-backup" ]]; then
  echo "error: failed backup left a destination behind" >&2
  exit 1
fi
"${compose[@]}" down --volumes --remove-orphans >/dev/null

# Start two containers simultaneously against a fresh volume. Exactly one may
# bootstrap, and neither logs nor test diagnostics may contain the token.
export TABBY_SYNC_TEST_USER_NAME=default
"${compose[@]}" up -d --scale tabby-sync=2
wait_healthy
mapfile -t ids < <("${compose[@]}" ps -q tabby-sync)
if [[ ${#ids[@]} -ne 2 ]]; then
  echo "error: expected two concurrent lifecycle containers" >&2
  exit 1
fi
"${compose[@]}" logs --no-color tabby-sync >"${tmp_dir}/concurrent.log"
if [[ $(grep -c 'generated first-run credentials' "${tmp_dir}/concurrent.log") -ne 1 ]]; then
  echo "error: concurrent startup did not bootstrap exactly once" >&2
  exit 1
fi
if grep -Eq 'tbs_[0-9a-f]{64}' "${tmp_dir}/concurrent.log"; then
  echo "error: container logs leaked a plaintext token" >&2
  exit 1
fi

container_id=${ids[0]}
docker exec "$container_id" sh -c '
  set -eu
  test -s /data/token.txt
  test "$(stat -c %a /data/token.txt)" = 600
  test "$(stat -c %a /data/users.yml)" = 600
'
users_before=$(docker exec "$container_id" sha256sum /data/users.yml)
token_before=$(docker exec "$container_id" sha256sum /data/token.txt)

# Scale back to one instance and restart it. Existing credentials must remain
# byte-for-byte unchanged.
"${compose[@]}" up -d --scale tabby-sync=1
wait_healthy
container_id=$("${compose[@]}" ps -q tabby-sync | head -n 1)
"${compose[@]}" restart tabby-sync >/dev/null
wait_healthy
container_id=$("${compose[@]}" ps -q tabby-sync | head -n 1)
if [[ $(docker exec "$container_id" sha256sum /data/users.yml) != "$users_before" ]]; then
  echo "error: repeated startup changed users.yml" >&2
  exit 1
fi
if [[ $(docker exec "$container_id" sha256sum /data/token.txt) != "$token_before" ]]; then
  echo "error: repeated startup changed token.txt" >&2
  exit 1
fi

umask 077
docker exec "$container_id" cat /data/token.txt >"${tmp_dir}/client-token"
docker exec "$container_id" sh -c '
  set -eu
  token=$(cat /data/token.txt)
  response=$(wget -q -O - \
    --header="Authorization: Bearer $token" \
    --header="Content-Type: application/json" \
    --post-data="{\"name\":\"backup-smoke\"}" \
    http://127.0.0.1:8080/api/1/configs)
  printf "%s" "$response" | grep -q "backup-smoke"
'

backup_dir="${tmp_dir}/backup"
"${repo_root}/scripts/backup.sh" "$backup_dir" >/dev/null
if [[ -e "${backup_dir}/token.txt" ]]; then
  echo "error: backup included the one-time plaintext token" >&2
  exit 1
fi
for file in tabby-sync.db master.key users.yml; do
  if [[ ! -s "${backup_dir}/${file}" || $(stat -c %a "${backup_dir}/${file}") != 600 ]]; then
    echo "error: backup file is missing, empty, or has an unsafe mode" >&2
    exit 1
  fi
done
if [[ $(stat -c %a "$backup_dir") != 700 ]]; then
  echo "error: backup directory mode is not 0700" >&2
  exit 1
fi

# Remove only this disposable Compose project's resources, restore through the
# service mount into the newly-created volume, then verify health and data.
"${compose[@]}" down --volumes --remove-orphans >/dev/null
"${repo_root}/scripts/restore.sh" "$backup_dir" >/dev/null
"${compose[@]}" up -d
wait_healthy
container_id=$("${compose[@]}" ps -q tabby-sync | head -n 1)
assert_mode_600 "$container_id"
if docker exec "$container_id" test -e /data/token.txt; then
  echo "error: restore recreated a plaintext token file" >&2
  exit 1
fi

docker exec -i "$container_id" sh -c '
  set -eu
  token=$(cat)
  response=$(wget -q -O - \
    --header="Authorization: Bearer $token" \
    http://127.0.0.1:8080/api/1/configs)
  printf "%s" "$response" | grep -q "backup-smoke"
' <"${tmp_dir}/client-token"

echo "Docker bootstrap, backup, and restore lifecycle passed"
