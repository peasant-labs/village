#!/usr/bin/env bash
# Operator-only measurement: never run this script as a required check.
set -euo pipefail
dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$dir/cook-number-budget.py" "$@"
