import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";

export const REQUIRED_EXPLORE_QUERY_CASES = ["missing-facets", "null-facets", "object-facets", "unknown-harness", "zero-count", "negative-count", "fractional-count", "explicit-empty-facets", "same-viewer-retains", "viewer-a-to-b-clears", "viewer-a-to-anonymous-clears"] as const;
export type FacetCase = { name: string; kind: "facet"; operation?: "omit"; facets?: unknown; accepted: boolean };
export type ScopeCase = { name: string; kind: "scope"; next_scope: string };
export type ExploreQueryCase = FacetCase | ScopeCase;
export function loadExploreQueryBoundaryFixtures(path = resolve(process.cwd(), "src/testdata/explore-query-boundary.yaml")): ExploreQueryCase[] {
  return validateExploreQueryBoundaryFixtures(parse(readFileSync(path, "utf8")));
}
export function validateExploreQueryBoundaryFixtures(input: unknown): ExploreQueryCase[] {
  const root = input as { cases?: unknown[] };
  if (!root || Object.keys(root).join(",") !== "cases" || !Array.isArray(root.cases) || root.cases.length === 0) throw new Error("explore query fixture requires one nonempty cases array");
  const cases = root.cases as Record<string, unknown>[];
  const names = cases.map((entry) => {
    if (!entry || typeof entry !== "object" || Array.isArray(entry) || typeof entry.name !== "string" || !entry.name.trim()) throw new Error("explore query fixture requires nonempty string name");
    return entry.name;
  });
  if (new Set(names).size !== names.length) throw new Error("explore query fixture names must be unique");
  const validated = cases.map((entry) => {
    if (entry.kind === "facet") {
      const keys = Object.keys(entry).sort().join(",");
      if (!["accepted,facets,kind,name", "accepted,kind,name,operation"].includes(keys) || (entry.operation !== undefined && entry.operation !== "omit")) throw new Error(`invalid facet fixture ${String(entry.name)}`);
      if (typeof entry.accepted !== "boolean") throw new Error("facet accepted must be boolean");
    } else if (entry.kind === "scope") {
      if (Object.keys(entry).sort().join(",") !== "kind,name,next_scope") throw new Error(`invalid scope fixture ${String(entry.name)}`);
      if (!["viewer-a", "viewer-b", "anonymous"].includes(entry.next_scope as string)) throw new Error("unsupported explore query scope target");
    } else throw new Error(`unsupported explore query fixture kind for ${String(entry.name)}`);
    return entry as unknown as ExploreQueryCase;
  });
  for (const required of REQUIRED_EXPLORE_QUERY_CASES) if (!names.includes(required)) throw new Error(`explore query fixture missing required case ${required}`);
  return validated;
}
