import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * The pull request page fixture: one row per situation the page must render,
 * with what the reader is told and may do in it. Loading is strict and the case
 * names are held to a manifest, so a row cannot be dropped or added without a
 * deliberate edit.
 */

/** Every case this corpus must carry, by name. */
const REQUIRED_CASE_NAMES = [
  "author previewing two transcripts with one audience",
  "author previewing two transcripts with different audiences",
  "author previewing one public transcript on a public repository",
  "author previewing while a transcript read fails",
  "confirm refused with a conflict",
  "author viewing an attached digest",
  "author on a waiting attachment",
  "author on a requested attachment",
  "author on a detached attachment",
  "reader of an attached digest",
  "reader moving through two transcripts",
  "reader who cannot open one bound transcript",
  "reader on a preview without a digest",
  "an attached attachment whose prompts are all gone",
  "an attached attachment with no digest",
] as const;

/** The action controls the page may show, by test id. */
export const PULL_REQUEST_CONTROLS = [
  "confirm-attachment",
  "not-now-attachment",
  "detach-attachment",
] as const;

export type PullRequestControl = (typeof PULL_REQUEST_CONTROLS)[number];

export interface PullRequestBoundTranscript {
  id: string;
  title: string | null;
}

export interface PullRequestTranscriptRead {
  id: string;
  title: string | null;
  visibility: "public" | "private" | "shared";
  collectives: string[];
  status: number | null;
}

export interface PullRequestPageCase {
  name: string;
  viewer_is_author: boolean;
  is_private_repository: boolean;
  state: "requested" | "waiting" | "preview" | "attached" | "detached";
  pull_title: string | null;
  head_ref: string | null;
  digest: "present" | "empty" | "absent";
  digest_transcripts: string[];
  commits_covered: number;
  commits_total: number;
  bound_transcripts: PullRequestBoundTranscript[];
  transcript_reads: PullRequestTranscriptRead[];
  confirm_status: number | null;
  expect_title: string;
  expect_sub: string | null;
  expect_state_line: string | null;
  expect_controls: PullRequestControl[];
  expect_confirm_label: string | null;
  expect_audience_form: "sentence" | "list" | "fallback" | "none";
  expect_audience_lines: string[];
  expect_coverage: string | null;
  expect_digest_block: "split" | "empty" | "absent";
  expect_split_labels: string[];
}

const CASE_FIELDS = [
  "name",
  "viewer_is_author",
  "is_private_repository",
  "state",
  "pull_title",
  "head_ref",
  "digest",
  "digest_transcripts",
  "commits_covered",
  "commits_total",
  "bound_transcripts",
  "transcript_reads",
  "confirm_status",
  "expect_title",
  "expect_sub",
  "expect_state_line",
  "expect_controls",
  "expect_confirm_label",
  "expect_audience_form",
  "expect_audience_lines",
  "expect_coverage",
  "expect_digest_block",
  "expect_split_labels",
] as const;

const BOUND_FIELDS = ["id", "title"] as const;
const READ_FIELDS = ["id", "title", "visibility", "collectives", "status"] as const;

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
    for (const bound of row.bound_transcripts) {
      assertExactKeys(bound, [...BOUND_FIELDS], `pull request page case ${row.name} bound transcript`);
    }
    for (const read of row.transcript_reads) {
      assertExactKeys(read, [...READ_FIELDS], `pull request page case ${row.name} transcript read`);
    }
    for (const control of row.expect_controls) {
      if (!(PULL_REQUEST_CONTROLS as readonly string[]).includes(control)) {
        throw new Error(`pull request page case ${row.name} expects an unknown control ${control}`);
      }
    }
  }
  assertNamesMatch(
    document.cases.map((row) => row.name),
    REQUIRED_CASE_NAMES,
    "pull-request-page",
  );
  return document.cases;
}

/** The one case with this name; a missing name is a fixture defect, not a skip. */
export function pullRequestPageCase(name: (typeof REQUIRED_CASE_NAMES)[number]): PullRequestPageCase {
  const row = loadPullRequestPageFixtures().find((candidate) => candidate.name === name);
  if (!row) throw new Error(`pull-request-page.yaml has no case named ${name}`);
  return row;
}
