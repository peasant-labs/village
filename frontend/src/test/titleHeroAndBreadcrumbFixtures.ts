import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";

export type TitleHeroAndBreadcrumbCase = {
  name: string;
  storedTitle: string | null;
  firstUserTurnContent: string;
  project: string;
  expectedProjectCrumbLabel: string;
  expectedHeroTitle: string;
  expectedCrumbUsesShortId: boolean;
  expectedCrumbLastLabel: string | null;
};

export type TitleHeroAndBreadcrumbFixtures = {
  cases: TitleHeroAndBreadcrumbCase[];
};

const requiredCaseNames = [
  "stored-title-present",
  "stored-title-null-harness-markup",
  "stored-title-long-truncates-crumb-not-hero",
] as const;

const caseKeys = [
  "name",
  "storedTitle",
  "firstUserTurnContent",
  "project",
  "expectedProjectCrumbLabel",
  "expectedHeroTitle",
  "expectedCrumbUsesShortId",
  "expectedCrumbLastLabel",
];

function assertExactKeys(value: object, expected: string[], location: string): void {
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (JSON.stringify(actual) !== JSON.stringify(wanted)) {
    throw new Error(`${location} has unknown or missing fields: got ${actual.join(", ")}; want ${wanted.join(", ")}`);
  }
}

export function loadTitleHeroAndBreadcrumbFixtures(): TitleHeroAndBreadcrumbFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/title-hero-and-breadcrumb.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("title-hero-and-breadcrumb fixture root must be an object");
  }
  assertExactKeys(parsed, ["cases"], "fixture root");
  const fixtures = parsed as TitleHeroAndBreadcrumbFixtures;
  if (fixtures.cases.length !== 3) {
    throw new Error(`title-hero-and-breadcrumb fixtures must contain exactly three cases, got ${fixtures.cases.length}`);
  }
  const names = fixtures.cases.map(({ name }) => name).sort();
  const wanted = [...requiredCaseNames].sort();
  if (JSON.stringify(names) !== JSON.stringify(wanted)) {
    throw new Error(`title-hero-and-breadcrumb case names differ: got ${names.join(", ")}; want ${wanted.join(", ")}`);
  }
  if (new Set(fixtures.cases.map(({ name }) => name)).size !== fixtures.cases.length) {
    throw new Error("title-hero-and-breadcrumb fixture case names must be unique");
  }
  for (const c of fixtures.cases) {
    assertExactKeys(c, caseKeys, `case ${c.name}`);
    if (c.expectedCrumbUsesShortId === (c.expectedCrumbLastLabel != null)) {
      throw new Error(
        `case ${c.name}: expectedCrumbUsesShortId and expectedCrumbLastLabel must disagree — exactly one of "falls back to the short id" or "shows a literal label" applies per case`,
      );
    }
    if (c.storedTitle == null && !c.expectedCrumbUsesShortId) {
      throw new Error(`case ${c.name}: a null storedTitle must fall back to the short id in the breadcrumb`);
    }
  }
  return fixtures;
}
