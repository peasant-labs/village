import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

export type ProjectOrphanRow = {
  name: string;
  ownerID: string;
  localID: string;
  parentSessionID: string | null;
  origin: "user" | "agent" | "unknown";
};
export type ProjectOrphanCase = {
  name: string;
  rows: ProjectOrphanRow[];
  expectedRoots: string[];
  expectedGroups: { parent: string; children: string[] }[];
  expectedOrphans: string[];
};
export type ProjectOrphanTransition = {
  name: string;
  initial: ProjectOrphanRow[];
  refreshed: ProjectOrphanRow[];
  expectedOrphansBefore: string[];
  expectedRootsAfter: string[];
  expectedGroupsAfter: { parent: string; children: string[] }[];
  expectedOrphansAfter: string[];
};

const requiredCases = ["mixed-roots-descendants-and-invalid-ancestry", "no-orphans", "only-orphans", "empty-project"];
const requiredTransitions = ["complete-parent-chain-arrives-and-removes-orphan-group", "parent-arrives-while-another-orphan-remains", "arriving-parent-still-unresolved"];

export function loadProjectOrphanGroupingFixtures(): { cases: ProjectOrphanCase[]; transitions: ProjectOrphanTransition[] } {
  const parsed = parse(readFileSync(resolve(process.cwd(), "src/testdata/project-orphan-grouping.yaml"), "utf8"), { strict: true }) as { cases: ProjectOrphanCase[]; transitions: ProjectOrphanTransition[] };
  assertExactKeys(parsed, ["cases", "transitions"], "project orphan fixture root");
  assertNamesMatch(parsed.cases.map((c) => c.name), requiredCases, "project orphan cases");
  assertNamesMatch(parsed.transitions.map((c) => c.name), requiredTransitions, "project orphan transitions");
  for (const testCase of parsed.cases) {
    assertExactKeys(testCase, ["name", "rows", "expectedRoots", "expectedGroups", "expectedOrphans"], `project orphan case ${testCase.name}`);
    assertPartition(testCase.name, testCase.rows, testCase.expectedRoots, testCase.expectedGroups.flatMap((g) => g.children), testCase.expectedOrphans);
  }
  for (const transition of parsed.transitions) {
    assertExactKeys(transition, ["name", "initial", "refreshed", "expectedOrphansBefore", "expectedRootsAfter", "expectedGroupsAfter", "expectedOrphansAfter"], `project orphan transition ${transition.name}`);
    assertPartition(transition.name, transition.initial, [], [], transition.expectedOrphansBefore);
    for (const group of transition.expectedGroupsAfter) {
      assertExactKeys(group, ["parent", "children"], `project orphan transition ${transition.name} group ${group.parent}`);
      if (!transition.expectedRootsAfter.includes(group.parent) || group.children.length === 0) {
        throw new Error(`${transition.name}: every post-refetch group must name a genuine root and at least one child`);
      }
    }
    assertPartition(transition.name, transition.refreshed, transition.expectedRootsAfter, transition.expectedGroupsAfter.flatMap((group) => group.children), transition.expectedOrphansAfter);
  }
  return parsed;
}

function assertPartition(name: string, rows: ProjectOrphanRow[], roots: string[], children: string[], orphans: string[]): void {
  const rowNames = rows.map((row) => row.name);
  const claimed = [...roots, ...children, ...orphans];
  if (new Set(claimed).size !== claimed.length || [...rowNames].sort().join("\0") !== [...claimed].sort().join("\0")) {
    throw new Error(`${name}: roots, descendants, and orphans must contain every real transcript exactly once`);
  }
}
