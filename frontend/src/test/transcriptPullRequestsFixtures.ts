import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import type { VillageTranscriptPullRequest } from "@peasant-labs/schema";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/** Loader for `src/testdata/transcript-pull-requests.yaml`. */

export interface TranscriptPullRequestsCase {
  name: string;
  status: number;
  pullRequests: VillageTranscriptPullRequest[];
  expectShown: string[];
  expectShowAll: string | null;
  expectAfterShowAll: string[];
}

export interface TranscriptPullRequestsFixtures {
  shownLimit: number;
  cases: TranscriptPullRequestsCase[];
}

const requiredCaseNames = [
  "no-pull-requests-shows-no-section",
  "one-pull-request-shows-it-without-show-all",
  "more-than-the-limit-folds-behind-show-all",
  "a-failed-read-says-so-and-offers-a-retry",
] as const;

const rowKeys = ["owner", "name", "number", "title", "head_ref", "state"];

export function loadTranscriptPullRequestsFixtures(): TranscriptPullRequestsFixtures {
  const path = resolve(process.cwd(), "src/testdata/transcript-pull-requests.yaml");
  const parsed: unknown = parse(readFileSync(path, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("transcript-pull-requests fixture root must be an object");
  }
  assertExactKeys(parsed, ["shownLimit", "cases"], "fixture root");
  const fixtures = parsed as TranscriptPullRequestsFixtures;
  assertNamesMatch(fixtures.cases.map((c) => c.name), requiredCaseNames, "transcript-pull-requests cases");
  for (const c of fixtures.cases) {
    assertExactKeys(
      c,
      ["name", "status", "pullRequests", "expectShown", "expectShowAll", "expectAfterShowAll"],
      `case ${c.name}`,
    );
    for (const pr of c.pullRequests) assertExactKeys(pr, rowKeys, `case ${c.name} pull request #${pr.number}`);
    if (c.expectShown.length !== Math.min(c.pullRequests.length, fixtures.shownLimit)) {
      throw new Error(`case ${c.name}: expectShown must list the first ${fixtures.shownLimit} rows served`);
    }
    if ((c.expectShowAll != null) !== c.pullRequests.length > fixtures.shownLimit) {
      throw new Error(`case ${c.name}: show all appears exactly when more than ${fixtures.shownLimit} rows are served`);
    }
    if (c.expectAfterShowAll.length !== c.pullRequests.length) {
      throw new Error(`case ${c.name}: expectAfterShowAll must list every row served`);
    }
  }
  return fixtures;
}
