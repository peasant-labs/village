import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { parse } from "yaml";
import { testdataPath } from "@/test/testdataPath";
import { ApiError } from "../api";
import {
  classifyContributionReadError,
  ContributionDeploymentError,
  decodeContributionPage,
  decodeContributionProjects,
  decodeContributionSnapshot,
  ContributionRead,
  ContributionReadFailureKind,
} from "./contributionReads";
import { expandContributionTokens, GROUP_ID, projectHashOf } from "../contribute/testTokens";

/** The word a decode case uses for a body the read accepts. */
const DECODE_ACCEPTED = "ok";

interface DecodeCase {
  name: string;
  read: ContributionRead["wire"];
  project?: string;
  body: unknown;
  expect: typeof DECODE_ACCEPTED | { deployment: string };
}

interface ClassifyCase {
  name: string;
  read: ContributionRead["wire"];
  status?: number;
  code?: string;
  network?: boolean;
  aborted?: boolean;
  kind: string;
}

const fixtures = parse(readFileSync(testdataPath("contribution-reads.yaml"), "utf8")) as {
  decode: DecodeCase[];
  classify: ClassifyCase[];
};

function decode(testCase: DecodeCase): unknown {
  const body = expandContributionTokens(testCase.body);
  switch (readOf(testCase.read)) {
    case ContributionRead.Page:
      return decodeContributionPage(body, GROUP_ID);
    case ContributionRead.Projects:
      return decodeContributionProjects(body);
    case ContributionRead.Snapshot:
      return decodeContributionSnapshot(body, GROUP_ID, projectHashOf(testCase.project ?? ""));
  }
}

function readOf(wire: ContributionRead["wire"]): ContributionRead {
  const read = Object.values(ContributionRead).find((member) => member.wire === wire);
  if (!read) throw new Error(`the fixture names read ${JSON.stringify(wire)}, which is not one of the contribution reads`);
  return read;
}

describe("contribution read decoding", () => {
  it.each(fixtures.decode.map((c) => [c.name, c] as const))("%s", (_name, testCase) => {
    if (testCase.expect === DECODE_ACCEPTED) {
      expect(() => decode(testCase)).not.toThrow();
      return;
    }
    const wanted = testCase.expect.deployment;
    let thrown: unknown;
    try {
      decode(testCase);
    } catch (err) {
      thrown = err;
    }
    expect(thrown).toBeInstanceOf(ContributionDeploymentError);
    expect((thrown as Error).message).toContain(wanted);
    expect(classifyContributionReadError(thrown, readOf(testCase.read)).kind).toBe(ContributionReadFailureKind.Deployment);
  });
});

describe("contribution read failure classification", () => {
  it.each(fixtures.classify.map((c) => [c.name, c] as const))("%s", (_name, testCase) => {
    let err: unknown;
    if (testCase.aborted) err = new DOMException("the read was cancelled", "AbortError");
    else if (testCase.network) err = new TypeError("failed to fetch");
    else err = new ApiError(testCase.status ?? 500, "refused", testCase.code ? { code: testCase.code } : undefined);
    expect(classifyContributionReadError(err, readOf(testCase.read)).kind).toBe(testCase.kind);
  });
});
