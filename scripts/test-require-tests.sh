#!/usr/bin/env bash
set -euo pipefail
dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
checker="$dir/require-tests.sh"
fixtures="$dir/testdata/require-tests"
"$checker" io <"$fixtures/pass.jsonl"
for name in missing skip fail malformed; do
  if "$checker" io <"$fixtures/$name.jsonl" >/dev/null 2>&1; then
    echo "require-tests unexpectedly accepted $name" >&2
    exit 1
  fi
done
echo "required test event checks passed"
