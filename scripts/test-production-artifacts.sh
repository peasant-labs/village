#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
fixture="${PRODUCTION_ARTIFACT_CASES_FIXTURE:-$script_dir/testdata/production-artifacts/cases.yaml}"
validate_only="${PRODUCTION_ARTIFACT_CASES_VALIDATE_ONLY:-false}"
temp_root="$(mktemp -d)"
trap 'rm -rf "$temp_root"' EXIT

parsed_cases="$temp_root/parsed-cases"
case_count=0
line_number=0
header_seen=false
declare -Ar required_modes=(
  [success]=1
  [native_success]=1
  [native_failure]=1
  [native_frontend_failure]=1
  [native_empty]=1
  [revision]=1
  [native_revision]=1
  [label_mismatch]=1
  [missing_runtime_revision]=1
  [runtime_revision_mismatch]=1
  [backend_command_mismatch]=1
  [frontend_command_mismatch]=1
  [missing_license_notices]=1
)
declare -A seen_modes=()
while IFS= read -r line || [[ -n "$line" ]]; do
  line_number=$((line_number + 1))
  if [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]]; then
    continue
  fi
  if [[ "$line" =~ ^[[:space:]]*cases:[[:space:]]*$ ]]; then
    if [[ "$header_seen" == true ]]; then
      printf >&2 'scripts/test-production-artifacts.sh: duplicate cases key at %s:%d. Keep exactly one cases sequence.\n' "$fixture" "$line_number"
      exit 1
    fi
    header_seen=true
    continue
  fi
  if [[ "$header_seen" != true || ! "$line" =~ ^[[:space:]]*-[[:space:]]*\"([^\"]*)\"[[:space:]]*$ ]]; then
    printf >&2 'scripts/test-production-artifacts.sh: unsupported fixture line at %s:%d: %s. Use only the cases key and quoted mode|outcome|expected rows.\n' "$fixture" "$line_number" "$line"
    exit 1
  fi
  row="${BASH_REMATCH[1]}"
  IFS='|' read -r mode outcome expected extra <<<"$row"
  if [[ ! "$mode" =~ ^[a-z0-9_]+$ || ( "$outcome" != pass && "$outcome" != fail ) || -z "$expected" || -n "$extra" ]]; then
    printf >&2 'scripts/test-production-artifacts.sh: invalid fixture row at %s:%d: %s. Expected mode|pass-or-fail|non-empty-message.\n' "$fixture" "$line_number" "$row"
    exit 1
  fi
  if [[ -z "${required_modes[$mode]+present}" ]]; then
    printf >&2 'scripts/test-production-artifacts.sh: unexpected artifact mode %s at %s:%d. Use the required production-artifact mode set.\n' "$mode" "$fixture" "$line_number"
    exit 1
  fi
  if [[ -n "${seen_modes[$mode]+present}" ]]; then
    printf >&2 'scripts/test-production-artifacts.sh: duplicate artifact mode %s at %s:%d. Each required mode must appear exactly once.\n' "$mode" "$fixture" "$line_number"
    exit 1
  fi
  seen_modes[$mode]=1
  printf '%s\n' "$row" >>"$parsed_cases"
  case_count=$((case_count + 1))
done <"$fixture"

for required_mode in "${!required_modes[@]}"; do
  if [[ -z "${seen_modes[$required_mode]+present}" ]]; then
    printf >&2 'scripts/test-production-artifacts.sh: missing required artifact mode %s in %s. Restore every production-artifact behavior case.\n' "$required_mode" "$fixture"
    exit 1
  fi
done

required_mode_count=${#required_modes[@]}
if [[ "$header_seen" != true || "$case_count" -ne "$required_mode_count" ]]; then
  printf >&2 'scripts/test-production-artifacts.sh: fixture rows=%d, want exactly %d under one cases key.\n' "$case_count" "$required_mode_count"
  exit 1
fi
if [[ "$validate_only" == true ]]; then
  printf '%s\n' 'Production artifact fixture is strict and complete'
  exit 0
fi

repository="$temp_root/repository"
mkdir -p "$repository/scripts" "$repository/fake-bin"
cp "$script_dir/check-artifact-source.sh" "$repository/scripts/check-artifact-source.sh"
cp "$script_dir/verify-production-artifacts.sh" "$repository/scripts/verify-production-artifacts.sh"
cp "$script_dir/production-image-build.sh" "$repository/scripts/production-image-build.sh"
cp "$script_dir/test-production-revision-guard.sh" "$repository/scripts/test-production-revision-guard.sh"
mkdir -p "$repository/scripts/testdata/production-artifacts"
cp "$script_dir/testdata/production-artifacts/invalid-revisions.yaml" "$repository/scripts/testdata/production-artifacts/invalid-revisions.yaml"
cp "$script_dir/testdata/production-artifacts/fake-docker.sh" "$repository/fake-bin/docker"
ln -s docker "$repository/fake-bin/podman"
printf '#!/usr/bin/env bash\nexit 0\n' >"$repository/fake-bin/mkdir"
chmod +x "$repository/fake-bin/mkdir"
chmod +x "$repository/scripts/check-artifact-source.sh" "$repository/scripts/verify-production-artifacts.sh" "$repository/fake-bin/docker"

git -C "$repository" init --quiet
git -C "$repository" add scripts fake-bin
git -C "$repository" \
  -c user.name='Production Artifact Test' \
  -c user.email='production-artifact-test@invalid.example' \
  commit --quiet -m 'initialize production artifact fixture'
revision="$(git -C "$repository" rev-parse HEAD)"

while IFS= read -r row; do
  IFS='|' read -r mode outcome expected <<<"$row"

  profile=()
  if [[ "$mode" == native_* ]]; then profile=('RUNNER_BUILD_SECCOMP_PROFILE=/server-only/seccomp.json'); fi
  if [[ "$mode" == native_empty ]]; then profile=('RUNNER_BUILD_SECCOMP_PROFILE='); fi
  script="$repository/scripts/verify-production-artifacts.sh"
  revision_guard=''
  if [[ "$mode" == revision || "$mode" == native_revision ]]; then
    script="$repository/scripts/test-production-revision-guard.sh"
    revision_guard=1
  fi

  if output="$(
    env -u RUNNER_BUILD_SECCOMP_PROFILE \
      PATH="$repository/fake-bin:$PATH" \
      FAKE_DOCKER_CASE="$mode" \
      FAKE_REVISION_GUARD="$revision_guard" \
      "${profile[@]}" \
      EXPECTED_REVISION="$revision" \
      NEXT_PUBLIC_API_URL='https://village.invalid/api/v1' \
      bash "$script" "$revision" 2>&1
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
done <"$parsed_cases"

assert_invalid_fixture() {
  local invalid_fixture=$1
  local expected_error=$2
  local description=$3
  if PRODUCTION_ARTIFACT_CASES_FIXTURE="$invalid_fixture" \
    PRODUCTION_ARTIFACT_CASES_VALIDATE_ONLY=true \
    "$0" >"$temp_root/invalid.out" 2>"$temp_root/invalid.err"; then
    printf >&2 'scripts/test-production-artifacts.sh: %s unexpectedly passed strict validation.\n' "$description"
    exit 1
  fi
  if ! [[ "$(<"$temp_root/invalid.err")" == *"$expected_error"* ]]; then
    printf >&2 'scripts/test-production-artifacts.sh: %s failed for an unexpected reason: %s\n' "$description" "$(<"$temp_root/invalid.err")"
    exit 1
  fi
}

assert_invalid_fixture "$script_dir/testdata/production-artifacts/unknown-field.yaml" "unsupported fixture line" "unknown-field fixture"
assert_invalid_fixture "$script_dir/testdata/production-artifacts/duplicate-mode.yaml" "duplicate artifact mode" "duplicate-mode fixture"
assert_invalid_fixture "$script_dir/testdata/production-artifacts/missing-mode.yaml" "missing required artifact mode" "missing-mode fixture"
assert_invalid_fixture "$script_dir/testdata/production-artifacts/unexpected-mode.yaml" "unexpected artifact mode" "unexpected-mode fixture"

printf '%s\n' 'Production artifact verifier proofs passed'
