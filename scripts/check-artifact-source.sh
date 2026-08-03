#!/usr/bin/env bash
set -euo pipefail

revision="${1:-}"
if [[ ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
  printf >&2 '%s\n' "scripts/check-artifact-source.sh: expected one full 40-character lowercase Git revision. Artifact verification cannot prove which source is being built. Pass \$(git rev-parse HEAD)."
  exit 2
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel)"
head_revision="$(git -C "$repo_root" rev-parse HEAD)"
if [[ "$revision" != "$head_revision" ]]; then
  printf >&2 '%s\n' "scripts/check-artifact-source.sh: requested revision '$revision' does not match checkout HEAD '$head_revision'. No artifact was built because its label would not prove the source context. Check out the intended revision and retry with that exact HEAD."
  exit 1
fi

source_changes="$(git -C "$repo_root" status --porcelain)"
if [[ -n "$source_changes" ]]; then
  printf >&2 '%s\n' "scripts/check-artifact-source.sh: tracked or untracked files differ from HEAD, so a revision label would not identify the Docker build contexts. Commit, restore, or remove the listed build inputs, then retry."
  printf >&2 '%s\n' "$source_changes"
  exit 1
fi

printf '%s\n' "Verified clean source at revision: $revision"
