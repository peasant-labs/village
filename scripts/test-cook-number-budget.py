#!/usr/bin/env python3
"""Canned-input checks only; this test performs no measurements."""
import importlib.util
import json
import pathlib
import unittest

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("cook_budget", HERE / "cook-number-budget.py")
budget = importlib.util.module_from_spec(spec)
spec.loader.exec_module(budget)


class ParseTests(unittest.TestCase):
    def test_fixture(self):
        rows = json.loads((HERE / "testdata/cook-budget-parser.json").read_text())
        required = {"cold-pass", "missing", "double-cold", "bad-count", "missing-metric", "wrong-mode", "wrong-affinity", "wrong-procs"}
        self.assertEqual({row["name"] for row in rows}, required)
        for row in rows:
            with self.subTest(row["name"]):
                try:
                    budget.parse_samples(row["output"], row["mode"], row["count"], row["runs"])
                    accepted = True
                except (ValueError, json.JSONDecodeError):
                    accepted = False
                self.assertEqual(accepted, row["accept"])

    def test_percentile(self):
        self.assertEqual(budget.percentile(list(range(1, 41))), 38)

    def test_barrier_skew_and_isolation(self):
        result = budget.concurrent_result([{"start_unix_ns": 100}, {"start_unix_ns": 145}])
        self.assertEqual(result["start_skew_ns"], 45)
        status = budget.isolation_status({"16": 1.01})
        self.assertEqual(status, "degraded")
        self.assertTrue(budget.exit_failed(budget.isolation_errors(status, True), False))
        self.assertFalse(budget.exit_failed(budget.isolation_errors(status, False), False))

if __name__ == "__main__":
    unittest.main()
