import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "./fixtureAssertions";
export type AccountAutoLinkCase = { name: string; initial: boolean; read: "ok" | "failed"; write: "ok" | "failed"; action: "toggle" | "retry"; expected: boolean };
export function loadAccountAutoLinkFixtures(): AccountAutoLinkCase[] {
  const raw = parse(readFileSync(resolve(process.cwd(), "src/testdata/account-auto-link.yaml"), "utf8"));
  assertExactKeys(raw, ["cases"], "account-auto-link root");
  if (!Array.isArray(raw.cases)) throw new Error("account-auto-link cases must be an array");
  for (const row of raw.cases) {
    assertExactKeys(row, ["name", "initial", "read", "write", "action", "expected"], "account-auto-link case");
    if (typeof row.name !== "string" || typeof row.initial !== "boolean" || typeof row.expected !== "boolean" || !["ok", "failed"].includes(row.read) || !["ok", "failed"].includes(row.write) || !["toggle", "retry"].includes(row.action)) throw new Error("invalid account-auto-link fixture");
  }
  assertNamesMatch(raw.cases.map((row: AccountAutoLinkCase) => row.name), ["automatic-linking-can-be-turned-on", "automatic-linking-can-be-turned-off", "a-failed-write-restores-the-saved-automatic-choice", "a-failed-settings-read-blocks-writes-and-can-be-retried"], "account-auto-link");
  return raw.cases;
}
