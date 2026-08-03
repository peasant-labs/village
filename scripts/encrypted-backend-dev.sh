#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel)"
dev_compose="$repo_root/docker-compose.encrypted-dev.yml"
test_compose="$repo_root/docker-compose.encrypted-test.yml"
secret_file="$repo_root/.env.encrypted-dev"
path_hash="$(printf '%s' "$repo_root" | git hash-object --stdin | cut -c1-12)"
dev_project="village-dev-$path_hash"
test_prefix="village-encrypted-${UID:-user}-"
test_project="${VILLAGE_ENCRYPTED_PROJECT:-${test_prefix}$(date -u +%Y%m%d%H%M%S)-$$}"
postgres_port="${VILLAGE_TEST_POSTGRES_PORT:-55460}"
minio_port="${VILLAGE_TEST_MINIO_PORT:-59060}"

fail() {
  printf >&2 'encrypted backend command failed: %s\nstage: %s\nimpact: %s\nrepair: %s\n' \
    "$1" "${2:-operation}" "${3:-the requested encrypted backend operation did not complete}" "${4:-correct the reported condition and retry}"
  exit 1
}

require_compose() {
  command -v docker >/dev/null 2>&1 || fail "Docker CLI is unavailable" prerequisite "no containers were changed" "install and start Docker"
  docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 is unavailable or Docker cannot be reached" prerequisite "no containers were changed" "start Docker and install its Compose v2 plugin"
}

require_curl() {
  command -v curl >/dev/null 2>&1 || fail "curl is unavailable" prerequisite "the backend was not probed or seeded" "install curl and retry the requested backend development command"
}

generate_secrets() {
  [[ -f "$secret_file" ]] && return
  command -v openssl >/dev/null 2>&1 || fail "openssl is unavailable for local secret generation" secret-generation "the development stack was not started" "install openssl and rerun make backend-dev"
  umask 077
  local kek jwt tmp
  kek="$(openssl rand -base64 32 | tr -d '\n')"
  jwt="$(openssl rand -hex 32)"
  tmp="${secret_file}.tmp.$$"
  printf 'JWT_SECRET=%s\nTRANSCRIPT_KEK_ACTIVE_VERSION=1\nTRANSCRIPT_KEK_KEYRING={"1":"%s"}\n' "$jwt" "$kek" >"$tmp"
  chmod 600 "$tmp"
  if ln "$tmp" "$secret_file" 2>/dev/null; then
    rm -f -- "$tmp"
  else
    rm -f -- "$tmp"
    [[ -f "$secret_file" ]] || fail "the worktree secret file could not be installed atomically" secret-generation "the development stack was not started" "check repository permissions and rerun make backend-dev"
  fi
}

dev() {
  require_compose
  require_curl
  generate_secrets
  local -a compose=(docker compose -f "$dev_compose" -p "$dev_project")
  "${compose[@]}" up -d --build postgres minio minio-init backend || fail "Compose could not start the persistent services" startup "the API may be unavailable; existing volumes and secrets were preserved" "inspect: docker compose -f '$dev_compose' -p '$dev_project' logs"
  local ready=0
  for _ in {1..120}; do
    if curl --fail --silent --show-error "http://127.0.0.1:${VILLAGE_DEV_BACKEND_PORT:-58080}/api/v1/openapi.json" >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  [[ "$ready" -eq 1 ]] || fail "the backend did not become reachable within 120 seconds" readiness "the API is unavailable; existing volumes and secrets were preserved" "inspect: docker compose -f '$dev_compose' -p '$dev_project' logs backend"
  printf 'Project: %s\nAPI: http://127.0.0.1:%s\nPostgreSQL: postgres://peasant:peasant@127.0.0.1:%s/peasant?sslmode=disable\nMinIO console: http://127.0.0.1:%s\n' "$dev_project" "${VILLAGE_DEV_BACKEND_PORT:-58080}" "${VILLAGE_DEV_POSTGRES_PORT:-55432}" "${VILLAGE_DEV_MINIO_CONSOLE_PORT:-59001}"
}

seed() {
  require_compose
  require_curl
  [[ -f "$secret_file" ]] || fail "the generated development secret file is absent" seed-preflight "no seed stage ran" "run make backend-dev first"
  local -a compose=(docker compose -f "$dev_compose" -p "$dev_project")
  curl --fail --silent --show-error "http://127.0.0.1:${VILLAGE_DEV_BACKEND_PORT:-58080}/api/v1/openapi.json" >/dev/null 2>&1 || fail "the worktree development backend is not reachable" seed-preflight "no seed stage ran" "run make backend-dev, then retry make backend-dev-seed"
  "${compose[@]}" exec -T backend go run ./cmd/server -seed-core
  "${compose[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U peasant -d peasant <"$repo_root/scripts/seed.sql"
  "${compose[@]}" exec -T backend go run ./cmd/server -seed-privacy
  "${compose[@]}" exec -T postgres psql -v ON_ERROR_STOP=1 -U peasant -d peasant <"$repo_root/scripts/seed-privacy-features.sql"
  printf 'Seeded encrypted core data, relationships, encrypted privacy data, and privacy relationships in project %s.\n' "$dev_project"
}

dev_down() {
  require_compose
  docker compose -f "$dev_compose" -p "$dev_project" down --remove-orphans
  printf 'Stopped project %s; named volumes and %s were preserved.\n' "$dev_project" "$secret_file"
}

reset() {
  [[ "${CONFIRM:-}" == 1 ]] || fail "destructive reset requires CONFIRM=1" confirmation "nothing was removed" "run make backend-dev-reset CONFIRM=1 only when this worktree's local encrypted data may be destroyed"
  require_compose
  docker compose -f "$dev_compose" -p "$dev_project" down --volumes --remove-orphans
  rm -f -- "$secret_file"
  printf 'Removed only derived project %s, its Compose volumes, and its generated local secret file.\n' "$dev_project"
}

test_down() {
  require_compose
  [[ -n "${VILLAGE_ENCRYPTED_PROJECT:-}" && "$test_project" == "$test_prefix"* ]] || fail "the retained test project must use the generated prefix $test_prefix" teardown "nothing was removed" "copy the exact project printed by the retained test run"
  local containers container config_files
  containers="$(docker ps -aq --filter "label=com.docker.compose.project=$test_project")" || fail "retained project containers could not be enumerated" teardown "nothing was removed" "restore Docker access and retry"
  [[ -n "$containers" ]] || fail "retained test project $test_project has no Compose-owned containers" teardown "nothing was removed" "verify the printed project name and that its retained containers still exist"
  while IFS= read -r container; do
    config_files="$(docker inspect --format '{{ index .Config.Labels "com.docker.compose.project.config_files" }}' "$container")" || fail "container ownership could not be inspected for retained project $test_project" teardown "nothing was removed" "restore Docker access and inspect the retained project before retrying"
    [[ "$config_files" == "$test_compose" ]] || fail "retained project $test_project contains a container owned by unexpected Compose config $config_files" teardown "nothing was removed" "inspect the project manually; only containers owned by $test_compose may be removed by this command"
  done <<<"$containers"
  docker compose -f "$test_compose" -p "$test_project" down --volumes --remove-orphans
}

test_project_resources() {
  local kind="$1"
  local result
  case "$kind" in
    container) result="$(docker ps -aq --filter "label=com.docker.compose.project=$test_project")" ;;
    volume) result="$(docker volume ls -q --filter "label=com.docker.compose.project=$test_project")" ;;
    network) result="$(docker network ls -q --filter "label=com.docker.compose.project=$test_project")" ;;
    *) fail "unknown resource kind $kind" isolation "nothing was changed" "report this script defect" ;;
  esac || fail "Docker could not enumerate $kind resources for test project $test_project" isolation "nothing was changed" "restore Docker access and retry"
  printf '%s' "$result"
}

test_stack() {
  require_compose
  [[ "$test_project" =~ ^[a-z0-9][a-z0-9_-]*$ ]] || fail "test project $test_project is not a safe Compose namespace" isolation "nothing was created or removed" "use lowercase letters, digits, underscores, or hyphens"
  [[ "$test_project" == "$test_prefix"* ]] || fail "test project $test_project is outside the generated prefix" isolation "nothing was created or removed" "unset VILLAGE_ENCRYPTED_PROJECT and retry"
  local existing_containers existing_volumes existing_networks
  existing_containers="$(test_project_resources container)"
  existing_volumes="$(test_project_resources volume)"
  existing_networks="$(test_project_resources network)"
  [[ -z "$existing_containers$existing_volumes$existing_networks" ]] || fail "test project $test_project already has containers, volumes, or networks and will not be reused or deleted" isolation "no test resource was started or removed" "inspect this namespace and, only if it is a retained encrypted test stack, run make backend-encrypted-down VILLAGE_ENCRYPTED_PROJECT=$test_project"
  test_compose_cmd=(docker compose -f "$test_compose" -p "$test_project")
  test_started=0
  test_cleanup_status=0
  cleanup_test() {
    local original_status="$1"
    trap - EXIT INT TERM
    if [[ "$test_started" -eq 1 && "${KEEP_ENCRYPTED_TEST_STACK:-0}" != 1 ]]; then
      "${test_compose_cmd[@]}" down --volumes --remove-orphans || test_cleanup_status=$?
    fi
    if [[ "$test_cleanup_status" -ne 0 ]]; then
      printf >&2 'encrypted backend teardown failed for generated project %s; tests cannot be reported successful until cleanup succeeds\n' "$test_project"
      if [[ "$original_status" -eq 0 ]]; then
        original_status="$test_cleanup_status"
      fi
    fi
    exit "$original_status"
  }
  trap 'cleanup_test "$?"' EXIT
  trap 'cleanup_test 130' INT
  trap 'cleanup_test 143' TERM
  test_started=1
  "${test_compose_cmd[@]}" up -d postgres minio
  "${test_compose_cmd[@]}" run --rm minio-init >/dev/null
  export TEST_DATABASE_ADMIN_URL="postgres://test:test@127.0.0.1:${postgres_port}/postgres?sslmode=disable"
  export TEST_DATABASE_URL_TEMPLATE="postgres://test:test@127.0.0.1:${postgres_port}/{database}?sslmode=disable"
  export TEST_S3_ENDPOINT="http://127.0.0.1:${minio_port}" TEST_S3_BUCKET=village-encrypted-test TEST_S3_ACCESS_KEY=village-test TEST_S3_SECRET_KEY=village-test-only-password
  printf 'Disposable encrypted test project: %s (fixed ports require VILLAGE_TEST_* overrides for concurrent runs)\n' "$test_project"
  (cd "$repo_root/backend" && nix develop -c env GOWORK=off GOFLAGS=-mod=readonly ../scripts/run-integration-gates.sh)
  cleanup_test 0
}

case "${1:-test}" in
  dev) dev ;;
  seed) seed ;;
  dev-down) dev_down ;;
  reset) reset ;;
  test) test_stack ;;
  down) test_down ;;
  *) fail "unknown command ${1:-}" argument-parsing "nothing was changed" "use a documented Make target" ;;
esac
