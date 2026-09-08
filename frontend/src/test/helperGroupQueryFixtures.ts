import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { zHelperGroupSummary } from "@peasant-labs/schema";
import type { Transcript } from "@/lib/types";

interface HelperQueryCase {
  name: string;
  kind: "list" | "members";
  params: Record<string, string>;
  expanded: boolean;
  status: number;
  refetchStatus?: number;
  nestedStatus?: number;
  request: Record<string, string>;
}

export function loadHelperGroupQueryFixtures() {
  const root = parse(readFileSync(resolve(process.cwd(), "src/testdata/helper-group-queries.yaml"), "utf8"), { strict: true });
  const group = zHelperGroupSummary.parse(root.group);
  const nestedGroup = zHelperGroupSummary.parse(root.nestedGroup);
  if (!Array.isArray(root.cases) || !root.row || typeof root.row !== "object") throw new Error("helper query fixture lacks cases or its canonical row overrides");
  const names = new Set<string>();
  const cases = root.cases.map((value: HelperQueryCase): HelperQueryCase => {
    if (!value.name || names.has(value.name) || !["list", "members"].includes(value.kind)) throw new Error("helper query fixture has a duplicate name or invalid kind");
    names.add(value.name);
    if (typeof value.expanded !== "boolean" || ![200, 409].includes(value.status)) throw new Error(`${value.name}: invalid expansion or response status`);
    for (const params of [value.params, value.request]) {
      if (!params || Object.values(params).some((v) => typeof v !== "string")) throw new Error(`${value.name}: request parameters must be strings`);
    }
    if (value.refetchStatus !== undefined && value.refetchStatus !== 403) throw new Error(`${value.name}: unsupported refetch refusal`);
    if (value.nestedStatus !== undefined && ![200,409].includes(value.nestedStatus)) throw new Error(`${value.name}: unsupported nested status`);
    return value;
  });
  for (const name of ["global-query", "profile-query", "project-query", "collapsed-no-member-request", "exact-member-scope-only", "expired-scope-no-automatic-refresh", "current-denial-hides-cached-members"]) {
    if (!names.has(name)) throw new Error(`required helper query fixture missing: ${name}`);
  }
  for (const name of ["nested-independent-scope", "nested-expiry-keeps-parent"]) {
    if (!names.has(name)) throw new Error(`required nested query fixture missing: ${name}`);
  }
  return { group, nestedGroup, row: root.row as Partial<Transcript>, nestedRow: root.nestedRow as Partial<Transcript>, cases };
}
