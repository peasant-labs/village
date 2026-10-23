import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import type { TranscriptContext } from "@/lib/transcriptContext";

/** Loader for `src/testdata/transcript-context.yaml`. */

export interface ParseCase {
  name: string;
  raw: string;
  expect: TranscriptContext | null;
}

export interface HrefCase {
  name: string;
  transcriptId: string;
  context: TranscriptContext | null;
  turn: number | null;
  expect: string;
}

export interface InferCase {
  name: string;
  shares: Array<{ group_id: string; status: string }>;
  mine: string[];
  expect: TranscriptContext;
}

export interface BreadcrumbCase {
  name: string;
  context: TranscriptContext | null;
  isLoggedIn: boolean;
  collectiveName: string | null;
  expect: string[];
}

export interface RouteCase {
  name: string;
  signedIn: boolean;
  search: string;
  myCollectives: Array<{ id: string; name: string }>;
  shares: Array<{ group_id: string; group_name: string; status: string }>;
  expectCrumbs: string[];
  expectReplace: string | null;
}

export interface TranscriptContextFixtures {
  parseCases: ParseCase[];
  hrefCases: HrefCase[];
  inferCases: InferCase[];
  breadcrumbCases: BreadcrumbCase[];
  routeCases: RouteCase[];
}

const requiredParseCaseNames = [
  "home",
  "a-collective",
  "a-pull-request",
  "nothing",
  "an-unknown-place",
  "a-collective-id-with-a-slash",
  "a-pull-request-without-a-number",
  "a-pull-request-with-a-zero-number",
  "a-pull-request-with-an-extra-segment",
] as const;

const requiredHrefCaseNames = ["no-place-no-turn", "a-collective", "a-pull-request-turn"] as const;

const requiredInferCaseNames = [
  "one-of-mine-holds-it",
  "two-of-mine-hold-it",
  "none-of-mine-holds-it",
  "mine-only-has-it-waiting",
  "it-is-in-no-collective",
] as const;

const requiredBreadcrumbCaseNames = [
  "from-home-signed-in",
  "from-nowhere-signed-out",
  "from-a-collective",
  "from-a-collective-not-yet-named",
  "from-a-pull-request",
] as const;

const requiredRouteCaseNames = [
  "opened-from-their-collective-names-it",
  "opened-from-a-collective-they-are-not-in-names-it-from-the-share",
  "opened-from-a-pull-request-names-it",
  "a-direct-link-in-one-of-theirs-is-given-that-collective",
  "a-direct-link-in-none-of-theirs-is-given-home-and-keeps-its-turn",
  "a-signed-out-reader-is-left-at-home",
] as const;

export function loadTranscriptContextFixtures(): TranscriptContextFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/transcript-context.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("transcript-context fixture root must be an object");
  }
  assertExactKeys(parsed, ["parseCases", "hrefCases", "inferCases", "breadcrumbCases", "routeCases"], "fixture root");
  const fx = parsed as TranscriptContextFixtures;
  assertNamesMatch(fx.parseCases.map((c) => c.name), requiredParseCaseNames, "transcript-context parseCases");
  assertNamesMatch(fx.hrefCases.map((c) => c.name), requiredHrefCaseNames, "transcript-context hrefCases");
  assertNamesMatch(fx.inferCases.map((c) => c.name), requiredInferCaseNames, "transcript-context inferCases");
  assertNamesMatch(
    fx.breadcrumbCases.map((c) => c.name),
    requiredBreadcrumbCaseNames,
    "transcript-context breadcrumbCases",
  );
  assertNamesMatch(fx.routeCases.map((c) => c.name), requiredRouteCaseNames, "transcript-context routeCases");
  for (const c of fx.routeCases) {
    assertExactKeys(
      c,
      ["name", "signedIn", "search", "myCollectives", "shares", "expectCrumbs", "expectReplace"],
      `route case ${c.name}`,
    );
    if (!c.signedIn && c.expectReplace !== null) {
      throw new Error(`route case ${c.name}: a signed-out reader's address is never rewritten`);
    }
  }
  for (const c of fx.parseCases) assertExactKeys(c, ["name", "raw", "expect"], `parse case ${c.name}`);
  for (const c of fx.hrefCases) {
    assertExactKeys(c, ["name", "transcriptId", "context", "turn", "expect"], `href case ${c.name}`);
  }
  for (const c of fx.inferCases) assertExactKeys(c, ["name", "shares", "mine", "expect"], `infer case ${c.name}`);
  for (const c of fx.breadcrumbCases) {
    assertExactKeys(c, ["name", "context", "isLoggedIn", "collectiveName", "expect"], `breadcrumb case ${c.name}`);
  }
  return fx;
}
