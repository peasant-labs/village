import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import type { ExploreFilters } from "@/lib/transcriptPageRequest";

export type ParamBuildFixture = {
  name: string;
  filters: ExploreFilters;
  expectedParams: Record<string, string>;
};

export type TranscriptPageRequestFixtures = {
  paramBuilds: ParamBuildFixture[];
};

const requiredParamBuildNames = [
  "defaults-page-one",
  "deeper-page-keeps-size",
  "query-is-trimmed",
  "whitespace-query-and-all-provider-omitted",
  "provider-tags-and-order",
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
  assertExactKeys(parsed, ["paramBuilds"], "fixture root");
  const fixtures = parsed as TranscriptPageRequestFixtures;

  if (fixtures.paramBuilds.length !== requiredParamBuildNames.length) {
    throw new Error(
      `transcript page request fixtures must contain exactly ${requiredParamBuildNames.length} paramBuilds cases`,
    );
  }
  assertExactNameInventory(
    fixtures.paramBuilds.map(({ name }) => name),
    requiredParamBuildNames,
    "paramBuilds fixtures",
  );
  const allNames = fixtures.paramBuilds.map(({ name }) => name);
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
  return fixtures;
}
