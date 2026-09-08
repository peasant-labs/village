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
    assertExactKeys(transition, ["name", "initial", "refreshed", "expectedOrphansBefore", "expectedRootsAfter", "expectedOrphansAfter"], `project orphan transition ${transition.name}`);
    assertPartition(transition.name, transition.initial, [], [], transition.expectedOrphansBefore);
    const groupedAfter = transition.refreshed.filter((row) => !transition.expectedRootsAfter.includes(row.name) && !transition.expectedOrphansAfter.includes(row.name));
    assertPartition(transition.name, transition.refreshed, transition.expectedRootsAfter, groupedAfter.map((row) => row.name), transition.expectedOrphansAfter);
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
