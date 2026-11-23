#!/usr/bin/env bash
set -euo pipefail
dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
checker="$dir/require-tests.sh"
fixtures="$dir/testdata/require-tests"
temp_root="$(mktemp -d)"
trap 'rm -rf "$temp_root"' EXIT
"$checker" io <"$fixtures/pass.jsonl"
"$checker" <"$fixtures/pass.jsonl"
for name in missing skip fail malformed empty; do
  if "$checker" io <"$fixtures/$name.jsonl" >/dev/null 2>"$temp_root/$name.err"; then
    echo "require-tests unexpectedly accepted $name" >&2
    exit 1
  fi
done
if "$checker" <"$fixtures/ambiguous.jsonl" >/dev/null 2>"$temp_root/ambiguous.err"; then
  echo "require-tests unexpectedly accepted ambiguous" >&2
  exit 1
fi
# A missing required test is named in the refusal, so the gate says what to restore.
if ! grep -q 'required test did not pass: TestCookNumberIOBudget' "$temp_root/missing.err"; then
  echo "require-tests refused the missing fixture without naming the missing test: $(cat "$temp_root/missing.err")" >&2
  exit 1
fi
if ! grep -q 'cannot uniquely identify the required-test group from the test events (matched 2 groups); name the group explicitly' "$temp_root/ambiguous.err"; then
  echo "require-tests refused the ambiguous fixture without naming the ambiguity: $(cat "$temp_root/ambiguous.err")" >&2
  exit 1
fi
if ! grep -q 'zero tests passed' "$temp_root/empty.err"; then
  echo "require-tests refused the empty fixture without naming zero tests passed: $(cat "$temp_root/empty.err")" >&2
  exit 1
fi
echo "required test event checks passed"
