#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
checker="$script_dir/check-go-test-events.go"
fixtures="$script_dir/testdata/go-test-events"
package="github.com/peasant-labs/village/backend/internal/example"
temp_root="$(mktemp -d)"
trap 'rm -rf "$temp_root"' EXIT

go run "$checker" "$package" <"$fixtures/no-skip.jsonl" >"$temp_root/pass.out" 2>"$temp_root/pass.err"
grep -q '^=== RUN' "$temp_root/pass.out"
grep -q '^--- PASS:' "$temp_root/pass.out"

if go run "$checker" "$package" <"$fixtures/top-level-skip.jsonl" >"$temp_root/top.out" 2>"$temp_root/top.err"; then
  printf >&2 '%s\n' "scripts/test-go-test-events.sh: top-level skip fixture unexpectedly passed; every Go skip action must fail the real-service gate."
  exit 1
fi
grep -q 'emitted 1 Go skip event' "$temp_root/top.err"

if go run "$checker" "$package" <"$fixtures/indented-subtest-skip.jsonl" >"$temp_root/sub.out" 2>"$temp_root/sub.err"; then
  printf >&2 '%s\n' "scripts/test-go-test-events.sh: nested subtest skip fixture unexpectedly passed; indentation in human output must not hide a Go skip action."
  exit 1
fi
grep -q 'emitted 1 Go skip event' "$temp_root/sub.err"

printf '%s\n' "Go test event skip proofs passed"
