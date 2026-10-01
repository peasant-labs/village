import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "./fixtureAssertions";
type HeaderLogoutCase = { name: string; mode: "failed" | "pending" | "rapid"; error: string };
export function loadHeaderLogoutRecoveryFixtures(): { cases: HeaderLogoutCase[] } {
  const raw = parse(readFileSync(resolve(process.cwd(), "src/testdata/header-logout-recovery.yaml"), "utf8"), { strict: true });
  assertExactKeys(raw, ["cases"], "header logout recovery");
  if (!Array.isArray(raw.cases)) throw new Error("header logout cases must be an array");
  for (const row of raw.cases) {
    assertExactKeys(row, ["name", "mode", "error"], "header logout case");
    if (!["failed", "pending", "rapid"].includes(row.mode) || typeof row.error !== "string" || !row.error) throw new Error("invalid header logout case");
  }
  assertNamesMatch(raw.cases.map((row: HeaderLogoutCase) => row.name), ["the-header-sign-out-announces-a-failed-request-and-can-retry", "the-header-sign-out-prevents-repeat-writes-while-the-request-is-pending", "rapid-header-sign-out-reselection-sends-one-request"], "header logout recovery");
  return raw;
}
