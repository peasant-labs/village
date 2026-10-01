import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
const requiredNames = ["collective-refetch-refuses-changed-credentials-before-dispatch", "collective-refetch-discards-a-response-after-credentials-change"] as const;
export interface CollectiveReadAccountCase { name: string; why: string; timing: "before-dispatch" | "after-dispatch" }
export function loadCollectiveReadAccountFixtures(): CollectiveReadAccountCase[] {
  const root = parse(readFileSync(resolve(process.cwd(), "src/testdata/collective-read-account.yaml"), "utf8"), { strict: true });
  if (!root || Object.keys(root).join() !== "cases" || !Array.isArray(root.cases)) throw new Error("collective read fixture must contain cases only");
  const cases: CollectiveReadAccountCase[] = root.cases.map((row: Record<string, unknown>) => {
    if (!row || Object.keys(row).sort().join() !== "name,timing,why" || typeof row.name !== "string" || typeof row.why !== "string" || !row.why.trim() || (row.timing !== "before-dispatch" && row.timing !== "after-dispatch")) throw new Error("invalid collective read account case");
    return row as unknown as CollectiveReadAccountCase;
  });
  const names = cases.map((row) => row.name);
  if (new Set(names).size !== names.length || requiredNames.some((name) => !names.includes(name)) || names.some((name) => !(requiredNames as readonly string[]).includes(name))) throw new Error("collective read required case names differ");
  return cases;
}
