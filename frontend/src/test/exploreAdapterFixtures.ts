import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { makeTranscriptFixture } from "./transcriptRowFixture";

const REQUIRED_NAMES = [
  "split-fields-win-over-legacy", "one-split-field-uses-zero", "explicit-zero-is-valid",
  "legacy-used-when-both-splits-missing", "all-null-stays-null", "absent-splits-use-legacy",
  "absent-input-present-output", "present-input-absent-output", "absent-legacy-split-total", "all-fields-absent",
];
export type TokenCase = { name: string; tokens_in?: number | null; tokens_out?: number | null; token_count?: number | null; expected: number | null };
export function validateExploreAdapterFixtures(input: unknown): TokenCase[] {
  const root = input as { cases?: unknown[] };
  if (!root || Object.keys(root).join(",") !== "cases" || !Array.isArray(root.cases) || !root.cases.length) throw new Error("adapter fixture requires nonempty cases");
  const names = new Set<string>();
  const cases = root.cases.map((value) => {
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("adapter case must be object");
    const entry = value as Record<string, unknown>;
    if (typeof entry.name !== "string" || !entry.name.trim()) throw new Error("adapter case name must be nonempty string");
    if (names.has(entry.name)) throw new Error("adapter case names must be unique");
    names.add(entry.name);
    if (!Object.hasOwn(entry, "expected")) throw new Error("adapter case requires expected");
    for (const [key, field] of Object.entries(entry)) {
      if (key === "name") continue;
      if (!["tokens_in", "tokens_out", "token_count", "expected"].includes(key)) throw new Error("unsupported adapter case field");
      if (field !== null && (typeof field !== "number" || !Number.isSafeInteger(field) || field < 0)) throw new Error("adapter token values must be nonnegative integers or null");
    }
    return entry as TokenCase;
  });
  for (const name of REQUIRED_NAMES) if (!names.has(name)) throw new Error(`adapter fixture missing required case ${name}`);
  return cases;
}
export function loadExploreAdapterFixtures(): TokenCase[] {
  return validateExploreAdapterFixtures(parse(readFileSync(resolve(process.cwd(), "src/testdata/explore-adapter.yaml"), "utf8")));
}

export function tokenTranscript(entry: TokenCase) {
  const transcript = makeTranscriptFixture();
  // Delete builder defaults: omitted wire fields must really be absent at the adapter.
  Reflect.deleteProperty(transcript, "token_count");
  Reflect.deleteProperty(transcript, "tokens_in");
  Reflect.deleteProperty(transcript, "tokens_out");
  const { name: _name, expected: _expected, ...tokens } = entry;
  void _name; void _expected;
  return Object.assign(transcript, tokens);
}
