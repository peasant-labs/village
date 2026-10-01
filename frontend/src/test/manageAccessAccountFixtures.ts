import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
const requiredNames = ["another-account-cannot-read-cached-private-collective-suggestions", "replacement-credentials-cannot-read-cached-private-collective-suggestions"] as const;
export interface ManageAccessAccountCase { name: string; why: string; change: "actor" | "credential" }
export function loadManageAccessAccountFixtures(): ManageAccessAccountCase[] {
  const root = parse(readFileSync(resolve(process.cwd(), "src/testdata/manage-access-account.yaml"), "utf8"), { strict: true });
  if (!root || Object.keys(root).join() !== "cases" || !Array.isArray(root.cases)) throw new Error("manage access fixture must contain cases only");
  const cases: ManageAccessAccountCase[] = root.cases.map((row: Record<string, unknown>) => {
    if (!row || Object.keys(row).sort().join() !== "change,name,why" || typeof row.name !== "string" || typeof row.why !== "string" || !row.why.trim() || (row.change !== "actor" && row.change !== "credential")) throw new Error("invalid manage access account case");
    return row as unknown as ManageAccessAccountCase;
  });
  const names = cases.map((row) => row.name);
  if (new Set(names).size !== names.length || requiredNames.some((name) => !names.includes(name)) || names.some((name) => !(requiredNames as readonly string[]).includes(name))) throw new Error("manage access required case names differ");
  return cases;
}
