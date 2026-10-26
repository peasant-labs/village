import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * Loader for `src/testdata/sign-in-route.yaml`, the corpus behind
 * `src/signInRoute.test.ts`. Deletion protection is the required-NAME
 * manifest below, never a count.
 */

export interface ProxyCase {
  name: string;
  why: string;
  /** The address asked for, with its query. */
  path: string;
  cookie: string | null;
  /** The browser's `Sec-Fetch-Dest`, or null for a client that sends none. */
  fetchDest: "document" | "empty" | null;
  expect: { status: number | null; location: string | null };
}

export interface TranscriptPageCase {
  name: string;
  why: string;
  id: string;
  searchParams: Record<string, string>;
  session: boolean;
  fetchDest: "document" | "empty" | null;
  api: "public" | "not-found" | "non-public-body" | "error" | "unreachable" | "no-config" | "non-public-read";
  reads: number;
  expect: { serve: boolean; openSignedOut: boolean; location: string | null };
}

export interface NextCase {
  name: string;
  why: string;
  next: string | null;
  expect: string | null;
}

const requiredProxyCases = [
  "no-cookie-goes-to-the-marketing-site",
  "a-client-that-says-nothing-is-a-page-load",
  "an-empty-cookie-is-no-session",
  "a-session-cookie-is-served-the-home-page",
  "an-in-app-fetch-of-the-root-is-served-the-app",
  "a-signed-out-deep-link-comes-back-to-its-page",
  "a-transcript-link-is-left-to-its-page",
  "the-sign-in-page-hands-over-its-return-address",
  "the-sign-in-page-drops-a-foreign-return-address",
  "the-sign-in-start-is-served-signed-out",
  "the-oauth-landing-is-served-signed-out",
  "the-privacy-notice-is-served-signed-out",
  "the-email-unsubscribe-is-served-signed-out",
  "a-crawler-reads-a-transcript-link-preview",
  "a-crawler-reads-the-open-graph-image",
  "a-crawler-reads-the-x-image",
  "a-share-image-is-served-whatever-the-headers",
  "an-in-app-navigation-to-a-transcript-is-left-to-its-page",
  "a-crawler-on-another-transcript-path-is-redirected",
  "a-crawler-on-the-transcripts-root-is-redirected",
  "a-crawler-on-another-app-page-is-redirected",
];

const requiredTranscriptPageCases = [
  "a-public-transcript-opens-signed-out",
  "a-private-transcript-signs-in-first",
  "a-shared-transcript-signs-in-first",
  "a-missing-transcript-signs-in-first",
  "a-non-public-answer-signs-in-first",
  "a-read-of-a-non-public-transcript-signs-in-first",
  "an-api-error-fails-closed",
  "an-unreachable-api-fails-closed",
  "no-api-configured-fails-closed",
  "the-way-back-keeps-the-query",
  "a-session-is-served-without-a-read",
  "an-in-app-navigation-to-a-private-transcript-signs-in",
  "a-crawler-reads-a-public-transcript",
  "a-crawler-reads-a-private-transcript-link-preview",
  "a-dot-segment-id-is-never-read",
];

const API_ANSWERS = ["public", "not-found", "non-public-body", "error", "unreachable", "no-config", "non-public-read"];

const requiredNextCases = [
  "a-page-on-this-site-is-followed",
  "no-address-lands-on-home",
  "another-host-is-refused",
  "a-protocol-relative-address-is-refused",
  "a-backslash-address-is-refused",
  "the-sign-in-page-itself-is-refused",
  "the-oauth-landing-is-refused",
  "surrounding-space-is-trimmed",
];

export function loadSignInRouteFixtures(): {
  proxyCases: ProxyCase[];
  transcriptPageCases: TranscriptPageCase[];
  nextCases: NextCase[];
} {
  const parsed = parse(readFileSync(resolve(process.cwd(), "src/testdata/sign-in-route.yaml"), "utf8"), {
    strict: true,
  }) as { proxyCases: ProxyCase[]; transcriptPageCases: TranscriptPageCase[]; nextCases: NextCase[] };
  assertExactKeys(parsed, ["proxyCases", "transcriptPageCases", "nextCases"], "sign-in-route fixture root");

  assertNamesMatch(parsed.proxyCases.map((c) => c.name), requiredProxyCases, "sign-in-route proxyCases");
  for (const c of parsed.proxyCases) {
    assertExactKeys(c, ["name", "why", "path", "cookie", "fetchDest", "expect"], `proxy case ${c.name}`);
    assertExactKeys(c.expect, ["status", "location"], `proxy case ${c.name} expect`);
    if (!c.why?.trim()) throw new Error(`proxy case ${c.name} states no reason it exists`);
  }
  // Both answers must be present, or a proxy that always redirected (or never
  // did) would pass.
  if (!parsed.proxyCases.some((c) => c.expect.status === null) || !parsed.proxyCases.some((c) => c.expect.status !== null)) {
    throw new Error("sign-in-route proxyCases must cover both a redirect and a page served");
  }

  assertNamesMatch(
    parsed.transcriptPageCases.map((c) => c.name),
    requiredTranscriptPageCases,
    "sign-in-route transcriptPageCases",
  );
  for (const c of parsed.transcriptPageCases) {
    assertExactKeys(
      c,
      ["name", "why", "id", "searchParams", "session", "fetchDest", "api", "reads", "expect"],
      `transcript page case ${c.name}`,
    );
    assertExactKeys(c.expect, ["serve", "openSignedOut", "location"], `transcript page case ${c.name} expect`);
    if (!c.why?.trim()) throw new Error(`transcript page case ${c.name} states no reason it exists`);
    if (!API_ANSWERS.includes(c.api)) throw new Error(`transcript page case ${c.name}: ${c.api} is not an API answer`);
    if (c.expect.serve === (c.expect.location !== null)) {
      throw new Error(`transcript page case ${c.name}: a served page has no location, and one not served has one`);
    }
  }

  assertNamesMatch(parsed.nextCases.map((c) => c.name), requiredNextCases, "sign-in-route nextCases");
  for (const c of parsed.nextCases) {
    assertExactKeys(c, ["name", "why", "next", "expect"], `next case ${c.name}`);
    if (!c.why?.trim()) throw new Error(`next case ${c.name} states no reason it exists`);
  }
  return parsed;
}
