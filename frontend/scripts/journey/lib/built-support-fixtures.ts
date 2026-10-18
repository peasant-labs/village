import { readFileSync } from "node:fs";
import { parse } from "yaml";

export type BuiltSupportFixture =
  | { name: "grouped-contributable-envelope" | "grouped-pending-payload"; route: string; schema: "contributable" | "pending" }
  | { name: "term-resistant-child" | "concurrent-stale-acquisition" | "interrupted-bookkeeping"; termMs: number; killMs: number };

export function loadBuiltSupportFixtures(): BuiltSupportFixture[] {
  const data = parse(readFileSync(new URL("../testdata/collectives-built-support.yaml", import.meta.url), "utf8")) as {
    requiredNames: string[]; cases: BuiltSupportFixture[];
  };
  const required = ["grouped-contributable-envelope", "grouped-pending-payload", "term-resistant-child", "concurrent-stale-acquisition", "interrupted-bookkeeping"].sort();
  if (JSON.stringify(data.requiredNames.toSorted()) !== JSON.stringify(required) || JSON.stringify(data.cases.map((row) => row.name).sort()) !== JSON.stringify(required)) {
    throw new Error("Built collective support fixtures must retain all required schema and lifecycle cases");
  }
  for (const row of data.cases) {
    if ("route" in row ? typeof row.route !== "string" || !["contributable", "pending"].includes(row.schema)
      : !Number.isSafeInteger(row.termMs) || row.termMs < 1 || row.termMs > 2000 || row.killMs !== 2000) {
      throw new Error(`Invalid bounded collective support fixture ${row.name}`);
    }
  }
  return data.cases;
}
