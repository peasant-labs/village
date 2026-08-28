import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys } from "@/test/fixtureAssertions";

export type MountedGraphCase = {
  name: string;
  project: string;
  userTurnContent: string;
  assistantTurnContent: string;
  toolName: string;
  toolFilePath: string;
  expectedToolNodeCount: number;
  expectedTurnNodeCount: number;
};

export type MountedGraphFixtures = {
  cases: MountedGraphCase[];
};

const requiredCaseNames = ["tool-bearing-turn-renders-graph-node"] as const;

const caseKeys = [
  "name",
  "project",
  "userTurnContent",
  "assistantTurnContent",
  "toolName",
  "toolFilePath",
  "expectedToolNodeCount",
  "expectedTurnNodeCount",
];

export function loadMountedGraphFixtures(): MountedGraphFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/mounted-graph.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("mounted-graph fixture root must be an object");
  }
  assertExactKeys(parsed, ["cases"], "fixture root");
  const fixtures = parsed as MountedGraphFixtures;
  const names = fixtures.cases.map(({ name }) => name).sort();
  const wanted = [...requiredCaseNames].sort();
  if (JSON.stringify(names) !== JSON.stringify(wanted)) {
    throw new Error(`mounted-graph case names differ: got ${names.join(", ")}; want ${wanted.join(", ")}`);
  }
  if (new Set(fixtures.cases.map(({ name }) => name)).size !== fixtures.cases.length) {
    throw new Error("mounted-graph fixture case names must be unique");
  }
  for (const c of fixtures.cases) {
    assertExactKeys(c, caseKeys, `case ${c.name}`);
    if (c.expectedToolNodeCount < 1) {
      throw new Error(`case ${c.name}: expectedToolNodeCount must be >= 1 — this test exists to prove a tool node mounts`);
    }
  }
  return fixtures;
}
