#!/usr/bin/env bash
# Usage: go test -json ... | scripts/require-tests.sh io|parity|schema
set -euo pipefail
dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
if [[ $# -ne 1 ]]; then
  echo "usage: require-tests.sh io|parity|schema < go-test.jsonl" >&2
  exit 2
fi
exec python3 "$dir/require-tests.py" "$dir/gate-required-tests.yaml" "$1"
