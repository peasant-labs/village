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
  cookie: string | null;
  expect: { status: number | null; location: string | null };
}

export interface NextCase {
  name: string;
  why: string;
  next: string | null;
  expect: string | null;
}

const requiredProxyCases = [
  "no-cookie-goes-to-the-marketing-site",
  "an-empty-cookie-is-no-session",
  "a-session-cookie-is-served-the-home-page",
];

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

export function loadSignInRouteFixtures(): { proxyCases: ProxyCase[]; nextCases: NextCase[] } {
  const parsed = parse(readFileSync(resolve(process.cwd(), "src/testdata/sign-in-route.yaml"), "utf8"), {
    strict: true,
  }) as { proxyCases: ProxyCase[]; nextCases: NextCase[] };
  assertExactKeys(parsed, ["proxyCases", "nextCases"], "sign-in-route fixture root");

  assertNamesMatch(parsed.proxyCases.map((c) => c.name), requiredProxyCases, "sign-in-route proxyCases");
  for (const c of parsed.proxyCases) {
    assertExactKeys(c, ["name", "why", "cookie", "expect"], `proxy case ${c.name}`);
    assertExactKeys(c.expect, ["status", "location"], `proxy case ${c.name} expect`);
    if (!c.why?.trim()) throw new Error(`proxy case ${c.name} states no reason it exists`);
  }
  // Both answers must be present, or a proxy that always redirected (or never
  // did) would pass.
  if (!parsed.proxyCases.some((c) => c.expect.status === null) || !parsed.proxyCases.some((c) => c.expect.status !== null)) {
    throw new Error("sign-in-route proxyCases must cover both a redirect and a page served");
  }

  assertNamesMatch(parsed.nextCases.map((c) => c.name), requiredNextCases, "sign-in-route nextCases");
  for (const c of parsed.nextCases) {
    assertExactKeys(c, ["name", "why", "next", "expect"], `next case ${c.name}`);
    if (!c.why?.trim()) throw new Error(`next case ${c.name} states no reason it exists`);
  }
  return parsed;
}
