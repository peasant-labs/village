import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";

const requiredNames = [
  "removal-stops-after-account-changes",
  "removal-stops-after-credential-changes",
  "removal-failure-does-not-cross-accounts",
  "removal-failure-does-not-cross-credentials",
  "removal-refuses-a-credential-changed-before-dispatch",
] as const;
export interface CollectiveRemovalAccountCase {
  name: string;
  why: string;
  switch: "identity" | "credential";
  responseStatus: 200 | 500;
  timing: "first-response" | "before-dispatch";
}
export function loadCollectiveRemovalAccountFixtures(): CollectiveRemovalAccountCase[] {
  const root = parse(readFileSync(resolve(process.cwd(), "src/testdata/collective-removal-account.yaml"), "utf8"), { strict: true });
  if (!root || Object.keys(root).join() !== "cases" || !Array.isArray(root.cases)) throw new Error("removal account fixture must contain cases only");
  const cases: CollectiveRemovalAccountCase[] = root.cases.map((row: Record<string, unknown>) => {
    if (!row || Object.keys(row).sort().join() !== "name,responseStatus,switch,timing,why" || typeof row.name !== "string" || typeof row.why !== "string" || !row.why.trim() || (row.switch !== "identity" && row.switch !== "credential") || (row.responseStatus !== 200 && row.responseStatus !== 500) || (row.timing !== "first-response" && row.timing !== "before-dispatch")) throw new Error("invalid removal account case");
    return row as unknown as CollectiveRemovalAccountCase;
  });
  const names = cases.map((row) => row.name);
  if (new Set(names).size !== names.length || requiredNames.some((name) => !names.includes(name)) || names.some((name) => !(requiredNames as readonly string[]).includes(name))) throw new Error("removal account required case names differ");
  return cases;
}
