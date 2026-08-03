#!/usr/bin/env bash
set -euo pipefail

source_script="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/check-artifact-source.sh"
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
