import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import { GROUP_BYS, HOME_COLUMNS, type HomeView } from "@/lib/homeView";
import type { HomeTranscriptCase } from "@/test/homePageFixtures";

/**
 * Loader for `src/testdata/home-view.yaml`, the corpus behind
 * `src/homeView.test.tsx`. Deletion protection is the required-NAME manifest
 * below, never a count.
 */

export type HomeViewStep =
  | { open: "projects" | "group by" | "columns" }
  | { pick: string }
  | { toggle: string }
  | { press: string };

export interface HomeViewCase {
  name: string;
  why: string;
  /** What this browser remembered before the visit: a view, raw text, or nothing. */
  stored: HomeView | string | null;
  steps: HomeViewStep[];
  expect: {
    groups: string[];
    rows: string[];
    columns: string[];
    stored: { groupBy: string; hiddenColumns: string[]; openGroups: number } | null;
  };
}

const requiredCaseNames = [
  "a-first-visit-groups-by-project-with-every-group-closed",
  "opening-a-group-shows-its-rows-and-is-remembered",
  "expand-all-then-collapse-all",
  "grouping-by-collective-files-a-row-under-every-reader",
  "grouping-by-agent-uses-the-harness-name",
  "grouping-by-nothing-is-one-flat-list",
  "the-column-picker-shows-and-hides-columns",
  "hiding-a-project-hides-its-rows-for-this-visit-only",
  "hiding-every-project-says-so-and-offers-them-back",
  "an-unreadable-remembered-view-falls-back-to-the-default",
];

const MENUS = new Set(["projects", "group by", "columns"]);

export function loadHomeViewFixtures(): { rows: HomeTranscriptCase[]; cases: HomeViewCase[] } {
  const parsed = parse(readFileSync(resolve(process.cwd(), "src/testdata/home-view.yaml"), "utf8"), {
    strict: true,
  }) as { rows: HomeTranscriptCase[]; cases: HomeViewCase[] };
  assertExactKeys(parsed, ["rows", "cases"], "home-view fixture root");
  assertNamesMatch(parsed.cases.map((c) => c.name), requiredCaseNames, "home-view cases");

  const titles = new Set(parsed.rows.map((row) => row.title));
  for (const c of parsed.cases) {
    assertExactKeys(c, ["name", "why", "stored", "steps", "expect"], `home-view case ${c.name}`);
    assertExactKeys(c.expect, ["groups", "rows", "columns", "stored"], `home-view case ${c.name} expect`);
    if (!c.why?.trim()) throw new Error(`home-view case ${c.name} states no reason it exists`);
    for (const step of c.steps) {
      const keys = Object.keys(step);
      if (keys.length !== 1 || !["open", "pick", "toggle", "press"].includes(keys[0])) {
        throw new Error(`home-view case ${c.name}: a step is one of open, pick, toggle or press`);
      }
      if ("open" in step && !MENUS.has(step.open)) throw new Error(`home-view case ${c.name}: no menu named ${step.open}`);
    }
    for (const title of c.expect.rows) {
      if (!titles.has(title)) throw new Error(`home-view case ${c.name}: ${title} is not one of the rows`);
    }
    const stored = c.expect.stored;
    if (stored !== null) {
      assertExactKeys(stored, ["groupBy", "hiddenColumns", "openGroups"], `home-view case ${c.name} stored`);
      if (!(GROUP_BYS as readonly string[]).includes(stored.groupBy)) {
        throw new Error(`home-view case ${c.name}: ${stored.groupBy} is not a grouping`);
      }
      for (const key of stored.hiddenColumns) {
        if (!(HOME_COLUMNS as readonly string[]).includes(key)) throw new Error(`home-view case ${c.name}: no column ${key}`);
      }
    }
  }
  // The first visit has to be in the corpus: it is the arrangement everyone sees.
  if (!parsed.cases.some((c) => c.stored === null && c.steps.length === 0)) {
    throw new Error("home-view cases must include a first visit with nothing remembered and nothing pressed");
  }
  return parsed;
}
