import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * Loader for `src/testdata/online-hint.yaml`, the corpus behind
 * `src/onlineHint.test.tsx`. Deletion protection is the required-NAME manifest
 * below, never a count.
 */

export interface DomainCase {
  name: string;
  why: string;
  appHost: string;
  marketingUrl: string;
  expect: string | null;
}

export interface CookieCase {
  name: string;
  why: string;
  handle: string | null;
  domain: string;
  expect: string;
}

export interface ProviderCase {
  name: string;
  why: string;
  meStatus: number;
  expectWrites: (string | null)[];
}

const requiredDomainCases = [
  "the-hosted-app-shares-the-marketing-domain",
  "a-www-marketing-site-shares-its-bare-domain",
  "a-look-alike-host-shares-nothing",
  "a-local-app-shares-nothing",
  "a-local-marketing-site-shares-nothing",
];
const requiredCookieCases = ["a-handle-is-written-for-the-session-length", "no-handle-clears-the-hint"];
const requiredProviderCases = [
  "a-signed-in-person-is-hinted",
  "a-signed-out-answer-clears-the-hint",
  "a-failed-read-leaves-the-hint-alone",
];

export function loadOnlineHintFixtures(): {
  domainCases: DomainCase[];
  cookieCases: CookieCase[];
  providerCases: ProviderCase[];
} {
  const parsed = parse(readFileSync(resolve(process.cwd(), "src/testdata/online-hint.yaml"), "utf8"), {
    strict: true,
  }) as { domainCases: DomainCase[]; cookieCases: CookieCase[]; providerCases: ProviderCase[] };
  assertExactKeys(parsed, ["domainCases", "cookieCases", "providerCases"], "online-hint fixture root");
  assertNamesMatch(parsed.domainCases.map((c) => c.name), requiredDomainCases, "online-hint domainCases");
  assertNamesMatch(parsed.cookieCases.map((c) => c.name), requiredCookieCases, "online-hint cookieCases");
  assertNamesMatch(parsed.providerCases.map((c) => c.name), requiredProviderCases, "online-hint providerCases");
  for (const c of parsed.domainCases) assertExactKeys(c, ["name", "why", "appHost", "marketingUrl", "expect"], `domain case ${c.name}`);
  for (const c of parsed.cookieCases) assertExactKeys(c, ["name", "why", "handle", "domain", "expect"], `cookie case ${c.name}`);
  for (const c of parsed.providerCases) assertExactKeys(c, ["name", "why", "meStatus", "expectWrites"], `provider case ${c.name}`);
  for (const c of [...parsed.domainCases, ...parsed.cookieCases, ...parsed.providerCases]) {
    if (!c.why?.trim()) throw new Error(`online-hint case ${c.name} states no reason it exists`);
  }
  // The hint must be shown both written and refused, or a function that
  // always (or never) shared the domain would pass.
  if (!parsed.domainCases.some((c) => c.expect === null) || !parsed.domainCases.some((c) => c.expect !== null)) {
    throw new Error("online-hint domainCases must cover both a shared domain and none");
  }
  return parsed;
}
