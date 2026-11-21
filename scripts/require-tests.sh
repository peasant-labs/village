#!/usr/bin/env bash
# Usage: go test -json ... | scripts/require-tests.sh [io|parity|schema]
set -euo pipefail
dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ $# -gt 1 ]]; then
  echo "usage: require-tests.sh [io|parity|schema] < go-test.jsonl" >&2
  exit 2
fi
exec go run "$dir/require-tests.go" "$dir/gate-required-tests.yaml" "${1:-auto}"
