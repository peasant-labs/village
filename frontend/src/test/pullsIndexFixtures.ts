import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import type { VillagePromptRequest } from "@peasant-labs/schema";
import { assertExactKeys, assertNamesMatch } from "./fixtureAssertions";

/**
 * The `/pulls` corpus: one case per surface the list of pull requests waiting
 * on the signed-in person can land on. Loading is strict, the case names are
 * held to a manifest, and each case's written surface is checked against what
 * its own inputs entail, so a case cannot be edited into agreeing with a page
 * that answers the wrong surface.
 */

const REQUIRED_CASE_NAMES = [
  "requests-grouped-by-day-newest-first",
  "a-request-from-another-year-names-its-year",
  "nothing-is-waiting",
  "a-failed-read-is-not-an-empty-list-and-retries",
  "a-read-that-keeps-failing-stays-a-failure",
  "signed-out-is-asked-to-sign-in-back-to-pulls",
] as const;

export type PullsIndexSurface = "rows" | "empty" | "failure" | "signed-out";

export interface PullsIndexCard {
  heading: string;
  meta: string;
  href: string;
}

export interface PullsIndexDay {
  label: string;
  cards: PullsIndexCard[];
}

export interface PullsIndexCase {
  name: (typeof REQUIRED_CASE_NAMES)[number];
  signedIn: boolean;
  requestFailure: "never" | "first" | "always";
  now: string;
  requests: VillagePromptRequest[];
  expectSurface: PullsIndexSurface;
  expectDays: PullsIndexDay[];
}

const CASE_FIELDS = ["name", "signedIn", "requestFailure", "now", "requests", "expectSurface", "expectDays"];
const REQUEST_FIELDS = ["owner", "name", "number", "state", "requested_at", "remote", "head_remote"];

/** The surface a case's own inputs entail, in the page's own branch order. */
function entailedSurface(row: PullsIndexCase): PullsIndexSurface {
  if (!row.signedIn) return "signed-out";
  if (row.requestFailure !== "never") return "failure";
  return row.requests.length === 0 ? "empty" : "rows";
}

export function loadPullsIndexFixtures(): PullsIndexCase[] {
  const raw = parse(readFileSync(resolve(process.cwd(), "src/testdata/pulls-index.yaml"), "utf8"), {
    strict: true,
  }) as { cases: PullsIndexCase[] };
  assertExactKeys(raw, ["cases"], "pulls-index root");
  if (!Array.isArray(raw.cases) || raw.cases.length === 0) throw new Error("pulls-index.yaml has no cases");
  for (const row of raw.cases) {
    assertExactKeys(row, CASE_FIELDS, `pulls-index case ${row.name}`);
    for (const request of row.requests) {
      assertExactKeys(request, REQUEST_FIELDS, `pulls-index case ${row.name} request`);
    }
    for (const day of row.expectDays) {
      assertExactKeys(day, ["label", "cards"], `pulls-index case ${row.name} day`);
      for (const card of day.cards) {
        assertExactKeys(card, ["heading", "meta", "href"], `pulls-index case ${row.name} card`);
      }
    }
    const entailed = entailedSurface(row);
    if (row.expectSurface !== entailed) {
      throw new Error(
        `pulls-index case ${row.name} expects the ${row.expectSurface} surface, but its inputs entail ${entailed}`,
      );
    }
    // What the page settles on lists every request it was served, once each:
    // a case that shows rows names as many cards as it serves requests.
    const settlesOnRows = row.signedIn && row.requestFailure !== "always" && row.requests.length > 0;
    const cards = row.expectDays.reduce((sum, day) => sum + day.cards.length, 0);
    if (cards !== (settlesOnRows ? row.requests.length : 0)) {
      throw new Error(
        `pulls-index case ${row.name} expects ${cards} cards for ${row.requests.length} served requests`,
      );
    }
  }
  assertNamesMatch(
    raw.cases.map((row) => row.name),
    REQUIRED_CASE_NAMES,
    "pulls-index",
  );
  // The corpus must hold a case served out of display order (so never sorting
  // fails) and one whose rows span more than one day (so never grouping fails).
  const outOfOrder = raw.cases.some((row) =>
    row.requests.some((request, index) => index > 0 && Date.parse(request.requested_at) > Date.parse(row.requests[index - 1].requested_at)),
  );
  const severalDays = raw.cases.some((row) => row.expectDays.length > 1);
  if (!outOfOrder || !severalDays) {
    throw new Error("pulls-index needs a case served oldest first and a case spanning several days");
  }
  return raw.cases;
}
