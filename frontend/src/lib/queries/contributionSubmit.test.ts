import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { parse } from "yaml";
import { zVillageBatchShareSelectionRequest } from "@peasant-labs/schema";
import { ApiError } from "../api";
import { contributionSelectionPath, decodeSelectionResponse, submitSelection } from "./contributionSubmit";
import { expandContributionTokens, GROUP_ID, projectHashOf, transcriptIdOf } from "../contribute/testTokens";

interface DecodeCase {
  name: string;
  body: unknown;
  expect: "ok" | { server: string };
}

const fixtures = parse(readFileSync(resolve(process.cwd(), "src/testdata/contribution-submit.yaml"), "utf8")) as {
  decode: DecodeCase[];
};

const request = zVillageBatchShareSelectionRequest.parse({
  project_hash: projectHashOf("pA"),
  transcript_ids: [transcriptIdOf("t1"), transcriptIdOf("t2")],
  private_approvals: [],
});

describe("selection write response", () => {
  it.each(fixtures.decode.map((c) => [c.name, c] as const))("%s", (_name, testCase) => {
    const body = expandContributionTokens(testCase.body);
    if (testCase.expect === "ok") {
      expect(() => decodeSelectionResponse(body, request)).not.toThrow();
      return;
    }
    let thrown: unknown;
    try {
      decodeSelectionResponse(body, request);
    } catch (err) {
      thrown = err;
    }
    expect(thrown).toBeInstanceOf(ApiError);
    expect((thrown as ApiError).status).toBe(502);
    expect((thrown as ApiError).message).toContain(testCase.expect.server);
  });
});

describe("submitSelection", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("posts the request to the collective's selection write", async () => {
    const fetchMock = vi.fn(async () =>
      new Response(JSON.stringify({ project_hash: request.project_hash, shared: [], already_shared: request.transcript_ids }), { status: 200 }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const response = await submitSelection(GROUP_ID, request);
    expect(response.already_shared).toEqual(request.transcript_ids);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url.endsWith(contributionSelectionPath(GROUP_ID))).toBe(true);
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual(request);
  });

  it("tells a missing route's plain-text 404 from the server's own 404", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("404 page not found", { status: 404 })));
    const missingRoute = await submitSelection(GROUP_ID, request).catch((err: unknown) => err);
    expect(missingRoute).toBeInstanceOf(ApiError);
    expect((missingRoute as ApiError).fromServer).toBe(false);

    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ error: "this collective no longer exists" }), { status: 404 })));
    const deleted = await submitSelection(GROUP_ID, request).catch((err: unknown) => err);
    expect((deleted as ApiError).fromServer).toBe(true);
  });
});
