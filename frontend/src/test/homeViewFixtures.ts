import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import { ALL_GROUP_BYS, ALL_VILLAGE_COLUMNS } from "@/lib/homeView";
import type { HomeTranscriptCase } from "@/test/homePageFixtures";

/**
 * Loader for `src/testdata/home-view.yaml`, the corpus behind
 * `src/homeView.test.tsx`. Deletion protection is the required-NAME manifest
 * below, never a count.
 */

export type HomeViewStep =
  | { groupBy: string }
  | { column: string }
  | { project: string }
  | { press: string };

export interface HomeViewCase {
  name: string;
  why: string;
  /** The URL grouping the page mounts with; defaults to project. */
  url?: string;
  /** Whether `stored` belongs under the earlier storage key. */
  legacy?: boolean;
  /** What this browser remembered before the visit: a view, raw text, or nothing. */
  stored: { columns?: unknown; visibleColumns?: readonly unknown[]; collapsed?: readonly unknown[]; open?: readonly unknown[] } | string | null;
  steps: HomeViewStep[];
  expect: {
    groups: string[];
    rows: string[];
    columns: string[];
    stored: { hiddenColumns: string[]; collapsedGroups: number } | null;
    urlGroupBy?: string;
  };
}

const requiredCaseNames = [
  "a-first-visit-groups-by-project-with-every-group-open",
  "closing-a-group-hides-its-rows-and-is-remembered",
  "the-group-picker-regroups-and-the-url-carries-it",
  "the-column-picker-shows-and-hides-columns",
  "a-saved-columns-record-migrates-to-the-closed-list",
  "a-saved-visible-columns-array-migrates-and-drops-an-unknown-member",
  "an-unreadable-remembered-view-falls-back-to-the-default",
  "a-url-grouping-overrides-the-default",
  "hiding-a-project-narrows-this-visit-only",
  "hiding-every-project-says-so-and-offers-them-back",
];

const stepKeys = ["groupBy", "column", "project", "press"];

export function loadHomeViewFixtures(): { rows: HomeTranscriptCase[]; cases: HomeViewCase[] } {
  const parsed = parse(readFileSync(resolve(process.cwd(), "src/testdata/home-view.yaml"), "utf8"), {
    strict: true,
  }) as { rows: HomeTranscriptCase[]; cases: HomeViewCase[] };
  assertExactKeys(parsed, ["rows", "cases"], "home-view fixture root");
  assertNamesMatch(parsed.cases.map((c) => c.name), requiredCaseNames, "home-view cases");

  const titles = new Set(parsed.rows.map((row) => row.title));
  for (const c of parsed.cases) {
    const caseKeys = Object.keys(c);
    const requiredCaseKeys = ["name", "why", "stored", "steps", "expect"];
    const allowedCaseKeys = [...requiredCaseKeys, "url", "legacy"];
    if (
      requiredCaseKeys.some((key) => !caseKeys.includes(key)) ||
      caseKeys.some((key) => !allowedCaseKeys.includes(key))
    ) {
      throw new Error(`home-view case ${c.name} has unknown or missing fields: ${caseKeys.join(", ")}`);
    }
    const expectKeys = Object.keys(c.expect);
    if (expectKeys.some((key) => !["groups", "rows", "columns", "stored", "urlGroupBy"].includes(key))) {
      throw new Error(`home-view case ${c.name} expect has unknown fields: ${expectKeys.join(", ")}`);
    }
    if (!c.why?.trim()) throw new Error(`home-view case ${c.name} states no reason it exists`);
    if (c.url !== undefined) {
      const params = new URLSearchParams(c.url);
      for (const key of params.keys()) {
        if (key !== "groupBy") throw new Error(`home-view case ${c.name}: the URL case carries only groupBy`);
      }
      const groupBy = params.get("groupBy");
      if (groupBy !== null && !ALL_GROUP_BYS.some((member) => member.wire === groupBy)) {
        throw new Error(`home-view case ${c.name}: ${groupBy} is not a grouping`);
      }
    }
    for (const step of c.steps) {
      const keys = Object.keys(step);
      if (keys.length !== 1 || !stepKeys.includes(keys[0])) {
        throw new Error(`home-view case ${c.name}: a step is one of ${stepKeys.join(", ")}`);
      }
    }
    for (const title of c.expect.rows) {
      if (!titles.has(title)) throw new Error(`home-view case ${c.name}: ${title} is not one of the rows`);
    }
    for (const column of c.expect.columns) {
      if (!ALL_VILLAGE_COLUMNS.some((member) => member.label === column)) {
        throw new Error(`home-view case ${c.name}: ${column} is not a column label`);
      }
    }
    const stored = c.expect.stored;
    if (stored !== null) {
      assertExactKeys(stored, ["hiddenColumns", "collapsedGroups"], `home-view case ${c.name} stored`);
      for (const key of stored.hiddenColumns) {
        if (!ALL_VILLAGE_COLUMNS.some((member) => member.wire === key)) {
          throw new Error(`home-view case ${c.name}: no column ${key}`);
        }
      }
    }
    if (c.expect.urlGroupBy !== undefined) {
      if (!ALL_GROUP_BYS.some((member) => member.wire === c.expect.urlGroupBy)) {
        throw new Error(`home-view case ${c.name}: ${c.expect.urlGroupBy} is not a grouping`);
      }
    }
    if (c.legacy === true && c.stored === null) {
      throw new Error(`home-view case ${c.name}: a legacy case must supply a stored view`);
    }
  }
  // The first visit has to be in the corpus: it is the arrangement everyone
  // sees, and it is the only case that may render every group open.
  if (!parsed.cases.some((c) => c.stored === null && c.steps.length === 0 && c.url === undefined)) {
    throw new Error("home-view cases must include a first visit with nothing remembered and nothing pressed");
  }
  // The migration's two shipped shapes both need a case, or a reader whose
  // columns were saved by the earlier page could be reset silently.
  if (!parsed.cases.some((c) => c.legacy === true && typeof c.stored === "object" && c.stored !== null && "columns" in c.stored)) {
    throw new Error("home-view cases must migrate a stored columns record under the earlier key");
  }
  if (!parsed.cases.some((c) => typeof c.stored === "object" && c.stored !== null && "visibleColumns" in c.stored)) {
    throw new Error("home-view cases must migrate a stored visibleColumns array");
  }
  return parsed;
}
