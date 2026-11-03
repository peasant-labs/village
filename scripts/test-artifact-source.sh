#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
source_script="$script_dir/check-artifact-source.sh"
# Fail closed unless both explicit trigger path lists cover the sourced build helper.
awk '
  /^  pull_request:$/ { event = "pull_request"; in_paths = 0; next }
  /^  push:$/ { event = "push"; in_paths = 0; next }
  /^[^[:space:]#]/ && $0 != "on:" { event = ""; in_paths = 0 }
  /^    paths:$/ { in_paths = 1; next }
  /^    [[:alnum:]_]+:/ && $0 != "    paths:" { in_paths = 0 }
  in_paths && /^      - "scripts\/production-image-build\.sh"$/ {
    if (event == "pull_request") pull_request = 1
    if (event == "push") push = 1
  }
  END {
    if (!pull_request || !push) {
      print "backend-tests.yml must trigger on scripts/production-image-build.sh for both pull_request and push" > "/dev/stderr"
      exit 1
    }
  }
' "$script_dir/../.github/workflows/backend-tests.yml"
temp_root="$(mktemp -d)"
trap 'rm -rf "$temp_root"' EXIT

mkdir -p "$temp_root/repository/scripts"
cp "$source_script" "$temp_root/repository/scripts/check-artifact-source.sh"
chmod +x "$temp_root/repository/scripts/check-artifact-source.sh"
git -C "$temp_root/repository" init --quiet
git -C "$temp_root/repository" add scripts/check-artifact-source.sh
git -C "$temp_root/repository" \
  -c user.name='Artifact Source Test' \
  -c user.email='artifact-source-test@invalid.example' \
  commit --quiet -m 'initialize source fixture'

revision="$(git -C "$temp_root/repository" rev-parse HEAD)"
"$temp_root/repository/scripts/check-artifact-source.sh" "$revision" >/dev/null

wrong_revision="0000000000000000000000000000000000000000"
if "$temp_root/repository/scripts/check-artifact-source.sh" "$wrong_revision" >"$temp_root/wrong.out" 2>"$temp_root/wrong.err"; then
  printf >&2 '%s\n' "scripts/test-artifact-source.sh: wrong-revision proof unexpectedly passed. The production checker must reject a label that differs from checkout HEAD."
  exit 1
fi
if ! [[ "$(<"$temp_root/wrong.err")" == *"does not match checkout HEAD"* ]]; then
  printf >&2 '%s\n' "scripts/test-artifact-source.sh: wrong-revision proof failed for an unexpected reason: $(<"$temp_root/wrong.err")"
  exit 1
fi

printf '%s\n' '# dirty tracked context' >>"$temp_root/repository/scripts/check-artifact-source.sh"
if "$temp_root/repository/scripts/check-artifact-source.sh" "$revision" >"$temp_root/dirty.out" 2>"$temp_root/dirty.err"; then
  printf >&2 '%s\n' "scripts/test-artifact-source.sh: dirty-context proof unexpectedly passed. The production checker must reject modified tracked build input."
  exit 1
fi
if ! [[ "$(<"$temp_root/dirty.err")" == *"tracked or untracked files differ from HEAD"* ]]; then
  printf >&2 '%s\n' "scripts/test-artifact-source.sh: dirty-context proof failed for an unexpected reason: $(<"$temp_root/dirty.err")"
  exit 1
fi

git -C "$temp_root/repository" restore scripts/check-artifact-source.sh
printf '%s\n' 'untracked build input' >"$temp_root/repository/untracked-build-input.txt"
if "$temp_root/repository/scripts/check-artifact-source.sh" "$revision" >"$temp_root/untracked.out" 2>"$temp_root/untracked.err"; then
  printf >&2 '%s\n' "scripts/test-artifact-source.sh: untracked-context proof unexpectedly passed. The production checker must reject untracked Docker build input."
  exit 1
fi
if ! [[ "$(<"$temp_root/untracked.err")" == *"tracked or untracked files differ from HEAD"* ]]; then
  printf >&2 '%s\n' "scripts/test-artifact-source.sh: untracked-context proof failed for an unexpected reason: $(<"$temp_root/untracked.err")"
  exit 1
fi

printf '%s\n' "Artifact source preflight proofs passed"
