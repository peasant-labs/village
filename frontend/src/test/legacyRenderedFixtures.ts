import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parseDocument } from "yaml";
import { assertExactKeys } from "./fixtureAssertions";

export interface LegacyRenderedFixture { name: string; content: string; rendered: string }

/** Go mounted-handler tests verify these same literal browser responses. */
export function loadLegacyRenderedFixtures(): LegacyRenderedFixture[] {
  const path = resolve(process.cwd(), "../backend/internal/handler/testdata/observed_model_preservation/legacy_rendered.yaml");
  const doc = parseDocument(readFileSync(path, "utf8"), {uniqueKeys: true});
  if (doc.errors.length) throw doc.errors[0];
  const value = doc.toJS();
  assertExactKeys(value, ["cases"], "legacy rendered root");
  if (!Array.isArray(value.cases)) throw new Error("Legacy rendered fixtures require cases");
  const names = new Set<string>();
  for (const item of value.cases) {
    assertExactKeys(item, ["name", "content", "rendered"], "legacy rendered case");
    if (typeof item.name !== "string" || !item.name || names.has(item.name) || typeof item.content !== "string" || !item.content || typeof item.rendered !== "string" || !item.rendered) throw new Error("Invalid legacy rendered fixture");
    names.add(item.name);
  }
  for (const name of ["array_to_empty_harness_detail", "historical_nonassistant_observation_detail"]) {
    if (!names.has(name)) throw new Error(`Required rendered fixture missing: ${name}`);
  }
  return value.cases;
}
