import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
const requiredNames = ["invite-search-preserves-the-selected-github-handle", "invite-search-announces-a-failed-invitation"] as const;
export interface CollectiveInviteSearchCase { name: string; why: string; query: string; handle: string; fail: boolean }
export function loadCollectiveInviteSearchFixtures(): CollectiveInviteSearchCase[] {
  const root = parse(readFileSync(resolve(process.cwd(), "src/testdata/collective-invite-search.yaml"), "utf8"), { strict: true });
  if (!root || Object.keys(root).join() !== "cases" || !Array.isArray(root.cases)) throw new Error("invite search fixture must contain cases only");
  const cases: CollectiveInviteSearchCase[] = root.cases.map((row: Record<string, unknown>) => {
    if (!row || Object.keys(row).sort().join() !== "fail,handle,name,query,why" || typeof row.name !== "string" || typeof row.why !== "string" || !row.why.trim() || typeof row.query !== "string" || row.query.length < 2 || typeof row.handle !== "string" || !row.handle.trim() || typeof row.fail !== "boolean") throw new Error("invalid invite search case");
    return row as unknown as CollectiveInviteSearchCase;
  });
  const names = cases.map((row) => row.name);
  if (new Set(names).size !== names.length || requiredNames.some((name) => !names.includes(name)) || names.some((name) => !(requiredNames as readonly string[]).includes(name))) throw new Error("invite search required case names differ");
  return cases;
}
