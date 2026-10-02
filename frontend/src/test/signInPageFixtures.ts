import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * Loader for `src/testdata/sign-in-page.yaml` — the corpus behind
 * `src/signInPage.test.tsx`.
 *
 * Deletion protection is the required-NAME manifest below, never a count. Every
 * consistency rule reads the fixture's own data: a case may only expect
 * providers from the corpus's own `offeredProviders`, and the corpus must
 * exercise both places a sign-in button can appear.
 */

export type SignInPageCase = {
  name: string;
  why: string;
  path: "/" | "/explore";
  signedIn: boolean;
  expectPageProviders: string[];
  expectHeaderProviders: string[];
};

export type SignInPageFixtures = {
  offeredProviders: string[];
  cases: SignInPageCase[];
};

const requiredCaseNames = [
  "a-signed-out-root-is-one-github-button",
  "a-signed-out-deep-link-signs-in-with-github-from-the-header",
  "a-signed-in-root-offers-no-sign-in",
] as const;

const caseKeys = [
  "name",
  "why",
  "path",
  "signedIn",
  "expectPageProviders",
  "expectHeaderProviders",
];

const paths = ["/", "/explore"];

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
    if (!paths.includes(c.path)) {
      throw new Error(`sign-in-page case ${c.name}: no route is mounted at ${c.path}`);
    }
    for (const id of [...c.expectPageProviders, ...c.expectHeaderProviders]) {
      if (!fixtures.offeredProviders.includes(id)) {
        throw new Error(
          `sign-in-page case ${c.name}: ${id} is not among the offered providers ` +
            `(${fixtures.offeredProviders.join(", ")})`,
        );
      }
    }
    // A signed-in visitor has nothing to sign in to. A case expecting a button
    // for one would assert the opposite of the rule.
    if (c.signedIn && (c.expectPageProviders.length > 0 || c.expectHeaderProviders.length > 0)) {
      throw new Error(`sign-in-page case ${c.name}: a signed-in visitor is offered no sign-in`);
    }
  }
  // Both places a button can appear must be exercised, or a corpus could pass
  // while the other one still offered every provider.
  if (!fixtures.cases.some((c) => c.expectPageProviders.length > 0)) {
    throw new Error("sign-in-page corpus must hold a case where the page offers a provider");
  }
  if (!fixtures.cases.some((c) => c.expectHeaderProviders.length > 0)) {
    throw new Error("sign-in-page corpus must hold a case where the header offers a provider");
  }
  return fixtures;
}
