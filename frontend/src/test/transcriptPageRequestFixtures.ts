import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { assertExactKeys } from "@/test/fixtureAssertions";
import type { ExploreFilters } from "@/lib/transcriptPageRequest";

export type ParamBuildFixture = {
  name: string;
  filters: ExploreFilters;
  expectedParams: Record<string, string>;
};

/**
 * The kinds of failure a failure-note case throws. They are the fixture's own
 * words and discriminate the case's `error`, so they are plain strings: a
 * discriminant narrows only on a literal type.
 */
export const ThrownFailureKind = Object.freeze({
  Mismatch: "mismatch",
  Status: "status",
  Network: "network",
  Message: "message",
  None: "none",
} as const);
export type ThrownFailureKind = (typeof ThrownFailureKind)[keyof typeof ThrownFailureKind];

export type FailureNoteFixture = {
  name: string;
  error:
    | { kind: typeof ThrownFailureKind.Mismatch }
    | { kind: typeof ThrownFailureKind.Status; status: number; message: string }
    | { kind: typeof ThrownFailureKind.Network; message: string }
    | { kind: typeof ThrownFailureKind.Message; message: string }
    | { kind: typeof ThrownFailureKind.None };
  expect: string | null;
};

export type TranscriptPageRequestFixtures = {
  paramBuilds: ParamBuildFixture[];
  failureNotes: FailureNoteFixture[];
};

const requiredFailureNoteNames = [
  "too-many-requests-says-to-wait",
  "no-connection-says-to-check-it",
  "the-servers-own-reason-restates-the-heading",
  "a-refused-page-adds-nothing",
  "a-reason-naming-an-api-path-adds-nothing",
  "a-thrown-non-error-adds-nothing",
] as const;

const requiredParamBuildNames = [
  "defaults-page-one",
  "deeper-page-keeps-size",
  "query-is-trimmed",
  "whitespace-query-and-all-provider-omitted",
  "provider-tags-and-order",
] as const;

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
  assertExactKeys(parsed, ["paramBuilds", "failureNotes"], "fixture root");
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

  assertExactNameInventory(
    fixtures.failureNotes.map(({ name }) => name),
    requiredFailureNoteNames,
    "failureNotes fixtures",
  );
  for (const fixture of fixtures.failureNotes) {
    assertExactKeys(fixture, ["name", "error", "expect"], `failureNotes ${fixture.name}`);
    if (fixture.expect !== null && /\/api\/|https?:\/\//.test(fixture.expect)) {
      throw new Error(`failureNotes ${fixture.name}: what the page says may never name a path or a URL`);
    }
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
