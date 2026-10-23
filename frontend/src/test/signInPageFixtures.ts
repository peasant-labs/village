import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * Loader for `src/testdata/sign-in-page.yaml` — the corpus behind
 * `src/signInPage.test.tsx`.
 *
 * Deletion protection is the required-NAME manifest below, never a count. Every
 * consistency rule reads the fixture's own data: a case may only expect a
 * provider from the corpus's own `offeredProviders`, and the corpus must
 * exercise every answer a sign-in step can give.
 */

export type SignInPageCase = {
  name: string;
  why: string;
  path: string;
  signedIn: boolean;
  /** The provider `/auth/start` leaves for, or null when it leaves for none. */
  expectStart: string | null;
  /** The return address remembered for `/auth/callback`, or null. */
  expectRemembered: string | null;
  /** Where the chrome's "sign in" link leads, or null when it offers none. */
  expectSignInLink: string | null;
  /** The visitor is sent to the marketing site's log-in popup. */
  expectLeaving: boolean;
};

export type SignInPageFixtures = {
  offeredProviders: string[];
  cases: SignInPageCase[];
};

const requiredCaseNames = [
  "the-sign-in-start-leads-to-github",
  "an-unknown-provider-starts-the-first-offered",
  "a-foreign-return-address-is-not-remembered",
  "a-good-session-skips-the-provider",
  "the-sign-in-page-hands-over-to-the-marketing-site",
  "a-signed-out-deep-link-leaves-for-the-log-in-popup",
  "the-privacy-notice-offers-one-way-in",
  "a-signed-in-root-offers-no-sign-in",
] as const;

const caseKeys = [
  "name",
  "why",
  "path",
  "signedIn",
  "expectStart",
  "expectRemembered",
  "expectSignInLink",
  "expectLeaving",
];

/** The routes the mounted fixture registers (`routeElement` in mountedHomeRoute). */
const routes = ["/", "/login", "/explore", "/auth/start", "/privacy"];

export function loadSignInPageFixtures(): SignInPageFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/sign-in-page.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("sign-in-page fixture root must be an object");
  }
  assertExactKeys(parsed, ["offeredProviders", "cases"], "sign-in-page fixture root");
  const fixtures = parsed as SignInPageFixtures;

  if (!Array.isArray(fixtures.offeredProviders) || fixtures.offeredProviders.length === 0) {
    throw new Error("sign-in-page offeredProviders must name at least one provider");
  }
  assertNamesMatch(
    fixtures.cases.map((c) => c.name),
    requiredCaseNames,
    "sign-in-page cases",
  );
  for (const c of fixtures.cases) {
    assertExactKeys(c, caseKeys, `sign-in-page case ${c.name}`);
    if (!c.why?.trim()) throw new Error(`sign-in-page case ${c.name} has no why`);
    const route = c.path.split("?")[0];
    if (!routes.includes(route)) {
      throw new Error(`sign-in-page case ${c.name}: no route is mounted at ${route}`);
    }
    if (c.expectStart !== null && !fixtures.offeredProviders.includes(c.expectStart)) {
      throw new Error(
        `sign-in-page case ${c.name}: ${c.expectStart} is not among the offered providers ` +
          `(${fixtures.offeredProviders.join(", ")})`,
      );
    }
    // Only the sign-in start leaves for a provider, and only for somebody who
    // is not signed in already.
    if (c.expectStart !== null && (route !== "/auth/start" || c.signedIn)) {
      throw new Error(`sign-in-page case ${c.name}: only /auth/start, signed out, leaves for a provider`);
    }
    if (c.expectRemembered !== null && c.expectStart === null) {
      throw new Error(`sign-in-page case ${c.name}: a return address is remembered only on the way to a provider`);
    }
    // A signed-in visitor has nothing to sign in to, and is never sent away.
    if (c.signedIn && (c.expectSignInLink !== null || c.expectLeaving)) {
      throw new Error(`sign-in-page case ${c.name}: a signed-in visitor is offered no sign-in`);
    }
    // The sign-in steps and the privacy notice are served signed out; every
    // other page sends the visitor to the popup.
    const open = route === "/auth/start" || route === "/privacy";
    if (!c.signedIn && c.expectLeaving === open) {
      throw new Error(`sign-in-page case ${c.name}: a signed-out visitor leaves exactly where no page is served`);
    }
  }
  if (!fixtures.cases.some((c) => c.expectStart !== null)) {
    throw new Error("sign-in-page corpus must hold a case that leaves for a provider");
  }
  if (!fixtures.cases.some((c) => c.expectSignInLink !== null)) {
    throw new Error("sign-in-page corpus must hold a case where the chrome links to sign in");
  }
  if (!fixtures.cases.some((c) => c.expectLeaving)) {
    throw new Error("sign-in-page corpus must hold a case that leaves for the marketing site");
  }
  return fixtures;
}
