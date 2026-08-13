#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
fixture="$script_dir/testdata/production-artifacts/cases.yaml"
temp_root="$(mktemp -d)"
trap 'rm -rf "$temp_root"' EXIT

repository="$temp_root/repository"
mkdir -p "$repository/scripts" "$repository/fake-bin"
cp "$script_dir/check-artifact-source.sh" "$repository/scripts/check-artifact-source.sh"
cp "$script_dir/verify-production-artifacts.sh" "$repository/scripts/verify-production-artifacts.sh"
cp "$script_dir/testdata/production-artifacts/fake-docker.sh" "$repository/fake-bin/docker"
chmod +x "$repository/scripts/check-artifact-source.sh" "$repository/scripts/verify-production-artifacts.sh" "$repository/fake-bin/docker"

git -C "$repository" init --quiet
git -C "$repository" add scripts fake-bin
git -C "$repository" \
  -c user.name='Production Artifact Test' \
  -c user.email='production-artifact-test@invalid.example' \
  commit --quiet -m 'initialize production artifact fixture'
revision="$(git -C "$repository" rev-parse HEAD)"

case_count=0
while IFS= read -r line; do
  if [[ ! "$line" =~ ^[[:space:]]*-[[:space:]]*\"([^\"]*)\"[[:space:]]*$ ]]; then
    continue
  fi
  row="${BASH_REMATCH[1]}"
  IFS='|' read -r mode outcome expected <<<"$row"
  case_count=$((case_count + 1))

  if output="$(
    PATH="$repository/fake-bin:$PATH" \
      FAKE_DOCKER_CASE="$mode" \
      EXPECTED_REVISION="$revision" \
      NEXT_PUBLIC_API_URL='https://village.invalid/api/v1' \
      "$repository/scripts/verify-production-artifacts.sh" "$revision" 2>&1
  )"; then
    actual=pass
  else
    actual=fail
  fi

  if [[ "$actual" != "$outcome" ]]; then
    printf >&2 'scripts/test-production-artifacts.sh: case %s outcome=%s, want %s. Output: %s\n' "$mode" "$actual" "$outcome" "$output"
    exit 1
  fi
  if [[ "$output" != *"$expected"* ]]; then
    printf >&2 'scripts/test-production-artifacts.sh: case %s output did not contain %q. Output: %s\n' "$mode" "$expected" "$output"
    exit 1
  fi
done <"$fixture"

if [[ "$case_count" -ne 5 ]]; then
  printf >&2 'scripts/test-production-artifacts.sh: fixture rows=%d, want 5\n' "$case_count"
  exit 1
fi

printf '%s\n' 'Production artifact verifier proofs passed'
