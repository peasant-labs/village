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
export type FinalContractCompatibilityFixtures = {
  harnesses: HarnessFixture[];
  offContractHarness: OffContractHarnessFixture;
  sessions: SessionFixture[];
};

function assertExactKeys(value: object, expected: string[], location: string): void {
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (JSON.stringify(actual) !== JSON.stringify(wanted)) {
    throw new Error(`${location} has unknown or missing fields: got ${actual.join(", ")}; want ${wanted.join(", ")}`);
  }
}

export function loadFinalContractCompatibilityFixtures(): FinalContractCompatibilityFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/final-contract-compatibility.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("final contract compatibility fixture root must be an object");
  }
  assertExactKeys(parsed, ["harnesses", "offContractHarness", "sessions"], "fixture root");
  const fixtures = parsed as FinalContractCompatibilityFixtures;
  if (fixtures.harnesses.length !== 6 || fixtures.sessions.length !== 3) {
    throw new Error("final contract compatibility fixtures must contain exactly six harnesses and three session cases");
  }
  const names = [...fixtures.harnesses, fixtures.offContractHarness, ...fixtures.sessions].map(({ name }) => name);
  if (new Set(names).size !== names.length) throw new Error("final contract compatibility fixture names must be unique");
  for (const fixture of fixtures.harnesses) assertExactKeys(fixture, ["name", "value"], `harness ${fixture.name}`);
  assertExactKeys(fixtures.offContractHarness, ["expectedError", "name", "transcriptId", "value"], "off-contract harness");
  for (const fixture of fixtures.sessions) {
    const expected = fixture.turnsState === "nullable-fields"
      ? ["expectedText", "expectedTurnCount", "name", "stopReasons", "turnsState"]
      : ["expectedText", "expectedTurnCount", "name", "turnsState"];
    assertExactKeys(fixture, expected, `session ${fixture.name}`);
  }
  return fixtures;
}
