import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import type { ExploreFilters } from "@/lib/transcriptPageRequest";

export type ParamBuildFixture = {
  name: string;
  filters: ExploreFilters;
  expectedParams: Record<string, string>;
};

export type SettledValidationFixture = {
  name: string;
  requestedPage: number;
  requestedLimit: number;
  responsePage: number;
  responseLimit: number;
  expectedOk: boolean;
};

export type TranscriptPageRequestFixtures = {
  paramBuilds: ParamBuildFixture[];
  settledValidation: SettledValidationFixture[];
};

const requiredParamBuildNames = [
  "defaults-page-one",
  "deeper-page-keeps-size",
  "query-is-trimmed",
  "whitespace-query-and-all-provider-omitted",
  "provider-tags-and-order",
] as const;

const requiredSettledValidationNames = [
  "page-one-matches",
  "deeper-page-matches",
  "stale-lower-page-rejected",
  "ahead-page-rejected",
  "limit-drift-rejected",
  "page-and-limit-drift-rejected",
] as const;

function assertExactKeys(value: object, expected: string[], location: string): void {
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (JSON.stringify(actual) !== JSON.stringify(wanted)) {
    throw new Error(
      `${location} has unknown or missing fields: got ${actual.join(", ")}; want ${wanted.join(", ")}`,
    );
  }
}

function assertExactNameInventory(
  actual: string[],
  expected: readonly string[],
  location: string,
): void {
  const got = [...actual].sort();
  const wanted = [...expected].sort();
  if (JSON.stringify(got) !== JSON.stringify(wanted)) {
    throw new Error(
      `${location} name inventory differs: got ${got.join(", ")}; want ${wanted.join(", ")}`,
    );
  }
}

export function loadTranscriptPageRequestFixtures(): TranscriptPageRequestFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/transcript-page-request.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (parsed == null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("transcript page request fixture root must be an object");
  }
  assertExactKeys(parsed, ["paramBuilds", "settledValidation"], "fixture root");
  const fixtures = parsed as TranscriptPageRequestFixtures;

  if (fixtures.paramBuilds.length !== requiredParamBuildNames.length) {
    throw new Error(
      `transcript page request fixtures must contain exactly ${requiredParamBuildNames.length} paramBuilds cases`,
    );
  }
  if (fixtures.settledValidation.length !== requiredSettledValidationNames.length) {
    throw new Error(
      `transcript page request fixtures must contain exactly ${requiredSettledValidationNames.length} settledValidation cases`,
    );
  }
  assertExactNameInventory(
    fixtures.paramBuilds.map(({ name }) => name),
    requiredParamBuildNames,
    "paramBuilds fixtures",
  );
  assertExactNameInventory(
    fixtures.settledValidation.map(({ name }) => name),
    requiredSettledValidationNames,
    "settledValidation fixtures",
  );

  const allNames = [...fixtures.paramBuilds, ...fixtures.settledValidation].map(({ name }) => name);
  if (new Set(allNames).size !== allNames.length) {
    throw new Error("transcript page request fixture names must be unique across sections");
  }

  for (const fixture of fixtures.paramBuilds) {
    assertExactKeys(fixture, ["name", "filters", "expectedParams"], `paramBuilds ${fixture.name}`);
    assertExactKeys(
      fixture.filters,
      ["query", "provider", "topics", "order", "page"],
      `paramBuilds ${fixture.name} filters`,
    );
  }

  for (const fixture of fixtures.settledValidation) {
    assertExactKeys(
      fixture,
      ["name", "requestedPage", "requestedLimit", "responsePage", "responseLimit", "expectedOk"],
      `settledValidation ${fixture.name}`,
    );
    // Non-vacuity guard: the encoded expectation must agree with the exact
    // page/limit equality the row describes, so a stale expectedOk can never
    // silently pass.
    const derivedOk =
      fixture.responsePage === fixture.requestedPage &&
      fixture.responseLimit === fixture.requestedLimit;
    if (fixture.expectedOk !== derivedOk) {
      throw new Error(
        `settledValidation ${fixture.name} expectedOk ${fixture.expectedOk} contradicts its ` +
          `page/limit equality (${derivedOk})`,
      );
    }
  }

  return fixtures;
}
