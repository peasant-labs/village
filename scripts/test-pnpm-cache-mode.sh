#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(git -C "$script_dir/.." rev-parse --show-toplevel)"
fixture="$script_dir/testdata/production-artifacts/invalid-pnpm-cache-modes.yaml"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
seen=false
while IFS= read -r line; do
  if [[ "$line" =~ ^[[:space:]]*-[[:space:]]*\"([^\"]*)\"[[:space:]]*$ ]]; then
    IFS='|' read -r name mode extra <<<"${BASH_REMATCH[1]}"
    [[ "$name" == unrecognized && "$mode" == sometimes && -z "$extra" && "$seen" == false ]] || { echo >&2 'Invalid cache-mode fixture'; exit 1; }
    seen=true
    if "${DOCKER:-docker}" build --target deps --build-arg "PNPM_SHARED_CACHE=$mode" --file "$repo_root/frontend/Dockerfile" "$repo_root" >"$tmp/build.log" 2>&1; then
      echo >&2 'The frontend Dockerfile accepted an invalid PNPM_SHARED_CACHE value'; exit 1
    fi
    grep -Fq 'PNPM_SHARED_CACHE must be true or false' "$tmp/build.log" || { cat >&2 "$tmp/build.log"; exit 1; }
  fi
done < "$fixture"
[[ "$seen" == true ]] || { echo >&2 'Missing unrecognized cache-mode fixture'; exit 1; }
printf '%s\n' 'Frontend Dockerfile rejects an invalid shared-cache mode'
