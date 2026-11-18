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
        required = {"cold-pass", "missing", "double-cold", "bad-count", "missing-metric", "wrong-mode"}
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


if __name__ == "__main__":
    unittest.main()
