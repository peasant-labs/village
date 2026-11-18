#!/usr/bin/env python3
"""Fail closed on missing, failed or skipped Go test events."""
import json
import sys


def main():
    with open(sys.argv[1], encoding="utf-8") as file:
        groups = json.load(file)  # JSON is a strict subset of YAML.
    required = groups.get(sys.argv[2])
    if not isinstance(required, list) or not required or len(required) != len(set(required)):
        raise ValueError("unknown or invalid required-test group")
    passed = set()
    started = set()
    errors = []
    for line in sys.stdin:
        event = json.loads(line)
        action, name = event.get("Action"), event.get("Test", "")
        if action in ("skip", "fail"):
            errors.append(f"{action}: {name or event.get('Package', 'unknown package')}")
        if action == "run" and name:
            started.add(name)
        if action == "pass" and name:
            passed.add(name)
    missing = set(required) - (passed & started)
    errors.extend(f"required test did not pass: {name}" for name in sorted(missing))
    if not passed:
        errors.append("zero tests passed")
    if errors:
        raise ValueError("; ".join(errors))
    print("required tests passed: " + ", ".join(required))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(f"require-tests: {error}", file=sys.stderr)
        sys.exit(1)
