import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * Loader for `src/testdata/signed-out-transcript.yaml`, the corpus behind
 * `src/signedOutTranscript.test.tsx`. Deletion protection is the required-NAME
 * manifest below, never a count.
 */

export interface FrameCase {
  name: string;
  why: string;
  pathname: string;
  signedIn: boolean;
  declares: "public" | "not-public" | "nothing";
  expectChrome: "public" | "sections" | "leaving";
}

export interface PageCase {
  name: string;
  why: string;
  search: string;
  expectCrumbs: string[];
}

const requiredFrameCases = [
  "a-public-transcript-stays-open-signed-out",
  "a-transcript-not-marked-public-is-left-for-sign-in",
  "a-transcript-page-that-has-not-said-is-waited-for",
  "a-public-mark-opens-no-other-page",
  "a-public-mark-opens-no-deeper-transcript-path",
  "a-signed-in-reader-of-a-public-transcript-is-unchanged",
  "a-signed-in-reader-of-another-transcript-is-unchanged",
];

const requiredPageCases = ["opened-with-no-place", "opened-from-a-collective", "opened-from-a-pull-request"];

export function loadSignedOutTranscriptFixtures(): { frameCases: FrameCase[]; pageCases: PageCase[] } {
  const parsed = parse(readFileSync(resolve(process.cwd(), "src/testdata/signed-out-transcript.yaml"), "utf8"), {
    strict: true,
  }) as { frameCases: FrameCase[]; pageCases: PageCase[] };
  assertExactKeys(parsed, ["frameCases", "pageCases"], "signed-out-transcript fixture root");

  assertNamesMatch(parsed.frameCases.map((c) => c.name), requiredFrameCases, "signed-out-transcript frameCases");
  for (const c of parsed.frameCases) {
    assertExactKeys(c, ["name", "why", "pathname", "signedIn", "declares", "expectChrome"], `frame case ${c.name}`);
    if (!["public", "not-public", "nothing"].includes(c.declares)) {
      throw new Error(`frame case ${c.name}: ${c.declares} is not a declaration`);
    }
    if (!["public", "sections", "leaving"].includes(c.expectChrome)) {
      throw new Error(`frame case ${c.name}: ${c.expectChrome} is not a chrome the frame draws`);
    }
    if (!c.why?.trim()) throw new Error(`frame case ${c.name} states no reason it exists`);
  }
  // Both answers on a transcript page must be present, or a frame that never
  // left (or always did) would pass.
  const onTranscript = parsed.frameCases.filter((c) => c.pathname === "/transcripts/7f1d2c3b" && !c.signedIn);
  if (!onTranscript.some((c) => c.expectChrome === "leaving") || !onTranscript.some((c) => c.expectChrome === "public")) {
    throw new Error("signed-out-transcript frameCases must cover a transcript page both left and kept");
  }

  assertNamesMatch(parsed.pageCases.map((c) => c.name), requiredPageCases, "signed-out-transcript pageCases");
  for (const c of parsed.pageCases) {
    assertExactKeys(c, ["name", "why", "search", "expectCrumbs"], `page case ${c.name}`);
    if (!c.why?.trim()) throw new Error(`page case ${c.name} states no reason it exists`);
  }
  return parsed;
}
