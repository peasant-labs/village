import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";

export type HarnessFixture = { name: string; value: string };
export type OffContractHarnessFixture = HarnessFixture & { transcriptId: string; expectedError: string };
export type SessionFixture = {
  name: string;
  turnsState: "omitted" | "null" | "nullable-fields";
  expectedText: string | null;
  expectedTurnCount: number;
  stopReasons?: ["max_turn_requests", "refusal"];
};
export type ObservedModelTurnFixture = {
  name: string;
  content: string;
  sourceObservation: string | null;
  expectedEffectiveModel: string;
  expectedTransition: string | null;
};
export type ObservedModelSessionFixture = {
  name: string;
  sessionModel: string;
  expectedTransitionCount: number;
  turns: ObservedModelTurnFixture[];
};
export type FinalContractCompatibilityFixtures = {
  harnesses: HarnessFixture[];
  offContractHarness: OffContractHarnessFixture;
  sessions: SessionFixture[];
  observedModelSessions: ObservedModelSessionFixture[];
};

const requiredHarnessNames = [
  "strike",
  "cursor",
  "claude-code",
  "gemini-cli",
  "codex",
  "opencode",
  "antigravity",
] as const;
const requiredSessionNames = ["omitted-turns", "null-turns", "nullable-turn-fields"] as const;
const requiredObservedModelSessionNames = [
  "sticky-observed-model-transition",
  "legacy-session-model-fallback",
] as const;
const requiredObservedModelTurnNames = {
  "sticky-observed-model-transition": [
    "sticky-observed-fable",
    "sticky-inherited-fable",
    "sticky-observed-opus",
    "sticky-inherited-opus",
  ],
  "legacy-session-model-fallback": [
    "legacy-fallback-one",
    "legacy-fallback-two",
    "legacy-fallback-three",
    "legacy-fallback-four",
  ],
} as const;

function assertExactKeys(value: object, expected: string[], location: string): void {
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (JSON.stringify(actual) !== JSON.stringify(wanted)) {
    throw new Error(`${location} has unknown or missing fields: got ${actual.join(", ")}; want ${wanted.join(", ")}`);
  }
}

function assertExactNameInventory(actual: string[], expected: readonly string[], location: string): void {
  const got = [...actual].sort();
  const wanted = [...expected].sort();
  if (JSON.stringify(got) !== JSON.stringify(wanted)) {
    throw new Error(`${location} name inventory differs: got ${got.join(", ")}; want ${wanted.join(", ")}`);
  }
}

export function loadFinalContractCompatibilityFixtures(): FinalContractCompatibilityFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/final-contract-compatibility.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("final contract compatibility fixture root must be an object");
  }
  assertExactKeys(parsed, ["harnesses", "observedModelSessions", "offContractHarness", "sessions"], "fixture root");
  const fixtures = parsed as FinalContractCompatibilityFixtures;
  if (fixtures.harnesses.length !== 7 || fixtures.sessions.length !== 3 || fixtures.observedModelSessions.length !== 2) {
    throw new Error("final contract compatibility fixtures must contain exactly seven harnesses, three nullable-turn cases, and two observed-model cases");
  }
  assertExactNameInventory(fixtures.harnesses.map(({ name }) => name), requiredHarnessNames, "harness fixtures");
  assertExactNameInventory(fixtures.sessions.map(({ name }) => name), requiredSessionNames, "nullable-turn fixtures");
  assertExactNameInventory(
    fixtures.observedModelSessions.map(({ name }) => name),
    requiredObservedModelSessionNames,
    "observed-model session fixtures",
  );
  const names = [
    ...fixtures.harnesses,
    fixtures.offContractHarness,
    ...fixtures.sessions,
    ...fixtures.observedModelSessions,
    ...fixtures.observedModelSessions.flatMap(({ turns }) => turns),
  ].map(({ name }) => name);
  if (new Set(names).size !== names.length) throw new Error("final contract compatibility fixture names must be unique");
  for (const fixture of fixtures.harnesses) assertExactKeys(fixture, ["name", "value"], `harness ${fixture.name}`);
  assertExactKeys(fixtures.offContractHarness, ["expectedError", "name", "transcriptId", "value"], "off-contract harness");
  for (const fixture of fixtures.sessions) {
    const expected = fixture.turnsState === "nullable-fields"
      ? ["expectedText", "expectedTurnCount", "name", "stopReasons", "turnsState"]
      : ["expectedText", "expectedTurnCount", "name", "turnsState"];
    assertExactKeys(fixture, expected, `session ${fixture.name}`);
  }
  for (const fixture of fixtures.observedModelSessions) {
    assertExactKeys(fixture, ["expectedTransitionCount", "name", "sessionModel", "turns"], `observed-model session ${fixture.name}`);
    if (fixture.turns.length !== 4) {
      throw new Error(`observed-model session ${fixture.name} must contain exactly four assistant turns`);
    }
    const requiredTurns = requiredObservedModelTurnNames[fixture.name as keyof typeof requiredObservedModelTurnNames];
    if (!requiredTurns) throw new Error(`observed-model session ${fixture.name} has no independent required-turn inventory`);
    assertExactNameInventory(fixture.turns.map(({ name }) => name), requiredTurns, `observed-model session ${fixture.name}`);
    for (const turn of fixture.turns) {
      assertExactKeys(
        turn,
        ["content", "expectedEffectiveModel", "expectedTransition", "name", "sourceObservation"],
        `observed-model turn ${turn.name}`,
      );
      if (!turn.content || !turn.expectedEffectiveModel || (turn.sourceObservation != null && !turn.sourceObservation)) {
        throw new Error(`observed-model turn ${turn.name} must carry non-empty content and model values`);
      }
    }
    const expectedTransitions = fixture.turns.filter(({ expectedTransition }) => expectedTransition != null).length;
    if (fixture.expectedTransitionCount !== expectedTransitions) {
      throw new Error(
        `observed-model session ${fixture.name} transition count ${fixture.expectedTransitionCount} differs from its ${expectedTransitions} explicit transition expectations`,
      );
    }
  }
  return fixtures;
}
