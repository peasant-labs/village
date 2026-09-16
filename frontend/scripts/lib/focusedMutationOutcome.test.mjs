import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { expect, it } from "vitest";
import { classifyFocusedOutcome } from "./focusedMutationOutcome.mjs";

/**
 * The classifier the mutation runner trusts to tell a killed assertion from a
 * broken fixture, driven by `scripts/testdata/focused-mutation-outcomes.yaml`.
 *
 * The decisive case is `loader-failure-naming-the-test-is-not-a-kill`: the
 * suite-load error names the case, the case name IS the focused test's name,
 * and no assertion ran — so the report must classify as a load failure, never
 * as an executed-test failure.
 */

const REQUIRED_CASES = [
  "executed-assertion-failure-kills",
  "loader-failure-naming-the-test-is-not-a-kill",
  "all-green-focused-run-survived",
  "silent-load-failure-is-never-a-pass",
];

const fixture = parse(
  readFileSync(resolve(process.cwd(), "scripts/testdata/focused-mutation-outcomes.yaml"), "utf8"),
  { strict: true },
);
const names = (fixture.cases ?? []).map((testCase) => testCase.name);
if (
  names.length !== REQUIRED_CASES.length ||
  new Set(names).size !== names.length ||
  JSON.stringify([...names].sort()) !== JSON.stringify([...REQUIRED_CASES].sort())
) {
  throw new Error(`focused mutation outcome names differ: got ${names.join(", ")}`);
}

for (const testCase of fixture.cases) {
  it(testCase.name, () => {
    const outcome = classifyFocusedOutcome(testCase.report, testCase.testName);
    expect(outcome.kind).toBe(testCase.expected);
    if (testCase.detail_contains != null) {
      expect(outcome.detail).toContain(testCase.detail_contains);
    }
  });
}
