import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

const FIXTURE = "src/testdata/displayed-turn-count.yaml";

const requiredNames = [
  "a-measured-row-prints-the-conversation-count",
  "a-row-published-before-the-measure-prints-its-stored-count",
  "a-measured-row-with-no-conversation-turns-prints-zero",
  "an-unmeasured-row-prints-no-count",
  "the-browser-derives-nothing",
] as const;

export interface DisplayedTurnCountCase {
  name: string;
  why: string;
  /** Every folded turn the publication recorded. Never printed. */
  turnCount: number | null;
  /** The measured conversation turns, or null when unmeasured. Never printed. */
  conversationTurnCount: number | null;
  /** What the server tells the surface to print. */
  displayedTurnCount: number | null;
  /** The number the surface prints, or null when it prints no count. */
  shows: number | null;
}

function isCount(value: unknown): value is number | null {
  return value === null || (typeof value === "number" && Number.isInteger(value) && value >= 0);
}

export function loadDisplayedTurnCountFixtures(): DisplayedTurnCountCase[] {
  const root: unknown = parse(readFileSync(resolve(process.cwd(), FIXTURE), "utf8"), { strict: true });
  if (typeof root !== "object" || root === null) {
    throw new Error(`${FIXTURE} must be a mapping with a "cases" list; it parsed to ${typeof root}.`);
  }
  assertExactKeys(root, ["cases"], FIXTURE);
  const rows: unknown = (root as { cases: unknown }).cases;
  if (!Array.isArray(rows)) throw new Error(`${FIXTURE} "cases" must be a list.`);
  const cases = rows.map((row: DisplayedTurnCountCase, index) => {
    const where = `${FIXTURE} case ${index}`;
    assertExactKeys(
      row,
      ["name", "why", "turnCount", "conversationTurnCount", "displayedTurnCount", "shows"],
      where,
    );
    if (typeof row.name !== "string" || typeof row.why !== "string" || !row.why.trim()) {
      throw new Error(`${where} needs a name and a non-blank why.`);
    }
    for (const key of ["turnCount", "conversationTurnCount", "displayedTurnCount", "shows"] as const) {
      if (!isCount(row[key])) {
        throw new Error(
          `${where} (${row.name}) has ${key}=${JSON.stringify(row[key])}; it must be null or a whole number of at least zero.`,
        );
      }
    }
    return row;
  });
  assertNamesMatch(cases.map((row) => row.name), requiredNames, "displayed turn count");
  return cases;
}
