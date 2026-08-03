#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel)"
fixture="$script_dir/testdata/production-artifacts/invalid-revisions.yaml"
temp_root="$(mktemp -d)"
images=()
cleanup() {
  if [[ "${#images[@]}" -gt 0 ]]; then
    docker image rm --force "${images[@]}" >/dev/null 2>&1 || true
  fi
  rm -rf "$temp_root"
}
trap cleanup EXIT

assert_rejected() {
  local target="$1"
  local case_name="$2"
  local invalid_revision="$3"
  local image="village-${target}-invalid-revision:${case_name}-$$"
  local output="$temp_root/${target}-${case_name}.log"
  images+=("$image")

  if [[ "$target" == "backend" ]]; then
    if docker build \
      --build-arg "RAILWAY_GIT_COMMIT_SHA=$invalid_revision" \
      --tag "$image" \
      "$repo_root/backend" >"$output" 2>&1; then
      printf >&2 'scripts/test-production-revision-guard.sh: backend accepted invalid revision case %s.\n' "$case_name"
      exit 1
    fi
  else
    if docker build \
      --build-arg "RAILWAY_GIT_COMMIT_SHA=$invalid_revision" \
      --build-arg 'NEXT_PUBLIC_API_URL=https://village.invalid/api/v1' \
      --tag "$image" \
      --file "$repo_root/frontend/Dockerfile" \
      "$repo_root" >"$output" 2>&1; then
      printf >&2 'scripts/test-production-revision-guard.sh: frontend accepted invalid revision case %s.\n' "$case_name"
      exit 1
    fi
  fi

  if ! grep -Fq 'RAILWAY_GIT_COMMIT_SHA must be the full 40-character lowercase Git revision' "$output"; then
    printf >&2 'scripts/test-production-revision-guard.sh: %s rejected case %s for an unexpected reason. Output: %s\n' "$target" "$case_name" "$(<"$output")"
    exit 1
  fi
}

case_count=0
while IFS= read -r line; do
  if [[ ! "$line" =~ ^[[:space:]]*-[[:space:]]*\"([^\"]*)\"[[:space:]]*$ ]]; then
    continue
  fi
  row="${BASH_REMATCH[1]}"
  IFS='|' read -r case_name invalid_revision <<<"$row"
  invalid_revision="${invalid_revision//<NEWLINE>/$'\n'}"
  case_count=$((case_count + 1))
  assert_rejected backend "$case_name" "$invalid_revision"
  assert_rejected frontend "$case_name" "$invalid_revision"
done <"$fixture"

if [[ "$case_count" -ne 3 ]]; then
  printf >&2 'scripts/test-production-revision-guard.sh: fixture rows=%d, want 3\n' "$case_count"
  exit 1
fi

printf '%s\n' 'Production Dockerfiles reject invalid Railway Git revisions'
