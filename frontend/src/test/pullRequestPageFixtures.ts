import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * The pull request page fixture: one row per state the page must render, plus
 * what the reader may do in it. Loading is strict and the case names are held to
 * a manifest, so a row cannot be dropped or added without a deliberate edit.
 */

/** Every case this corpus must carry, by name. */
const REQUIRED_CASE_NAMES = [
  "author viewing an attached digest",
  "author viewing a preview with confirm available",
  "confirm refused with a conflict",
  "non-author viewing an attached digest",
  "non-author viewing a preview without a digest",
] as const;

export interface PullRequestPageCase {
  name: string;
  viewer_is_author: boolean;
  state: "requested" | "waiting" | "preview" | "attached" | "detached";
  digest: "present" | "absent";
  confirm_status: number | null;
  expect_confirm: boolean;
  expect_detach: boolean;
}

const CASE_FIELDS = [
  "name",
  "viewer_is_author",
  "state",
  "digest",
  "confirm_status",
  "expect_confirm",
  "expect_detach",
] as const;

export function loadPullRequestPageFixtures(): PullRequestPageCase[] {
  const path = resolve(process.cwd(), "src/testdata/pull-request-page.yaml");
  const document = parse(readFileSync(path, "utf8"), { strict: true }) as {
    cases: PullRequestPageCase[];
  };
  if (!Array.isArray(document?.cases) || document.cases.length === 0) {
    throw new Error("pull-request-page.yaml has no cases");
  }
  for (const row of document.cases) {
    assertExactKeys(row, [...CASE_FIELDS], `pull request page case ${row.name}`);
  }
  assertNamesMatch(
    document.cases.map((row) => row.name),
    REQUIRED_CASE_NAMES,
    "pull-request-page",
  );
  return document.cases;
}
