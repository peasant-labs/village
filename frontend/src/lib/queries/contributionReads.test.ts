import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { parse } from "yaml";
import { ApiError } from "../api";
import {
  classifyContributionReadError,
  ContributionDeploymentError,
  decodeContributionPage,
  decodeContributionProjects,
  decodeContributionSnapshot,
  type ContributionRead,
} from "./contributionReads";
import { expandContributionTokens, GROUP_ID, projectHashOf } from "../contribute/testTokens";

interface DecodeCase {
  name: string;
  read: ContributionRead;
  project?: string;
  body: unknown;
  expect: "ok" | { deployment: string };
}

interface ClassifyCase {
  name: string;
  read: ContributionRead;
  status?: number;
  code?: string;
  network?: boolean;
  aborted?: boolean;
  kind: string;
}

const fixtures = parse(readFileSync(resolve(process.cwd(), "src/testdata/contribution-reads.yaml"), "utf8")) as {
  decode: DecodeCase[];
  classify: ClassifyCase[];
};

function decode(testCase: DecodeCase): unknown {
  const body = expandContributionTokens(testCase.body);
  switch (testCase.read) {
    case "page":
      return decodeContributionPage(body, GROUP_ID);
    case "projects":
      return decodeContributionProjects(body);
    case "snapshot":
      return decodeContributionSnapshot(body, GROUP_ID, projectHashOf(testCase.project ?? ""));
  }
}

describe("contribution read decoding", () => {
  it.each(fixtures.decode.map((c) => [c.name, c] as const))("%s", (_name, testCase) => {
    if (testCase.expect === "ok") {
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
    expect(classifyContributionReadError(thrown, testCase.read).kind).toBe("deployment");
  });
});

describe("contribution read failure classification", () => {
  it.each(fixtures.classify.map((c) => [c.name, c] as const))("%s", (_name, testCase) => {
    let err: unknown;
    if (testCase.aborted) err = new DOMException("the read was cancelled", "AbortError");
    else if (testCase.network) err = new TypeError("failed to fetch");
    else err = new ApiError(testCase.status ?? 500, "refused", testCase.code ? { code: testCase.code } : undefined);
    expect(classifyContributionReadError(err, testCase.read).kind).toBe(testCase.kind);
  });
});
