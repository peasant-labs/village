import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

/**
 * Loader for `src/testdata/hidden-surfaces.yaml` — the corpus behind
 * `src/hiddenSurfaces.test.tsx`. Deletion protection is a required-NAME
 * manifest per group, never a count.
 */

export type DataAccess = "members_only" | "contributors" | "public";

export type DataAccessCase = {
  name: string;
  savedDataAccess: DataAccess;
  expectLabel: string;
  expectOptions: DataAccess[];
};

export type AttestCase = {
  name: string;
  viewerIsOwner: boolean;
};

export type HiddenSurfacesFixtures = {
  dataAccessCases: DataAccessCase[];
  attestCases: AttestCase[];
};

const requiredDataAccessCaseNames = [
  "a-members-only-collective-is-not-offered-public",
  "a-contributors-collective-is-not-offered-public",
  "an-already-public-collective-keeps-its-value-and-label",
] as const;

const requiredAttestCaseNames = [
  "the-owner-is-offered-no-attestation",
  "a-signed-in-reader-is-offered-no-attestation",
] as const;

const dataAccessValues: readonly DataAccess[] = ["members_only", "contributors", "public"];

export function loadHiddenSurfacesFixtures(): HiddenSurfacesFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/hidden-surfaces.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("hidden-surfaces fixture root must be an object");
  }
  assertExactKeys(parsed, ["dataAccessCases", "attestCases"], "hidden-surfaces fixture root");
  const fixtures = parsed as HiddenSurfacesFixtures;

  assertNamesMatch(
    fixtures.dataAccessCases.map((c) => c.name),
    requiredDataAccessCaseNames,
    "hidden-surfaces dataAccessCases",
  );
  for (const c of fixtures.dataAccessCases) {
    assertExactKeys(c, ["name", "savedDataAccess", "expectLabel", "expectOptions"], `data access case ${c.name}`);
    if (typeof c.expectLabel !== "string" || !c.expectLabel.trim()) throw new Error(`data access case ${c.name}: expected label required`);
    for (const v of [c.savedDataAccess, ...c.expectOptions]) {
      if (!dataAccessValues.includes(v)) {
        throw new Error(`data access case ${c.name}: ${v} is not a data-access value`);
      }
    }
    // The rule the control exists to express, derived from the case's own
    // data: public is listed exactly when the collective already is public,
    // and the saved value is always one of the options.
    const listsPublic = c.expectOptions.includes("public");
    if (listsPublic !== (c.savedDataAccess === "public")) {
      throw new Error(
        `data access case ${c.name}: public is offered only to a collective that is already public`,
      );
    }
    if (!c.expectOptions.includes(c.savedDataAccess)) {
      throw new Error(`data access case ${c.name}: the saved value must be among the options`);
    }
  }
  // Both sides of the rule, or a control that always (or never) listed public
  // would still pass.
  if (!fixtures.dataAccessCases.some((c) => c.savedDataAccess === "public")) {
    throw new Error("hidden-surfaces dataAccessCases must hold an already-public collective");
  }
  if (!fixtures.dataAccessCases.some((c) => c.savedDataAccess !== "public")) {
    throw new Error("hidden-surfaces dataAccessCases must hold a collective that is not public");
  }

  assertNamesMatch(
    fixtures.attestCases.map((c) => c.name),
    requiredAttestCaseNames,
    "hidden-surfaces attestCases",
  );
  for (const c of fixtures.attestCases) {
    assertExactKeys(c, ["name", "viewerIsOwner"], `attest case ${c.name}`);
  }
  return fixtures;
}
