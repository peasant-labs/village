import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TranscriptListHarnessFacetsError, useTranscripts } from "./transcripts";
import { makeQueryClientHarness, transcriptListResponse } from "@/test/queryHookHelpers";

type Fixture = { required_names: string[]; cases: { name: string; value: string; accepted: boolean }[] };
const fixture = parse(readFileSync(resolve(process.cwd(), "src/testdata/explore-query-boundary.yaml"), "utf8")) as Fixture;
const facets: Record<string, unknown> = {
  null: null, object: {}, unknown: [{ harness: "unknown", count: 1 }], zero: [{ harness: "codex", count: 0 }],
  negative: [{ harness: "codex", count: -1 }], fractional: [{ harness: "codex", count: 1.5 }], empty: [],
};

describe("Explore discovery query trust and viewer scope", () => {
  it("retains the independent required-name inventory", () => {
    expect(new Set(fixture.cases.map((entry) => entry.name))).toEqual(new Set(fixture.required_names));
  });
  for (const entry of fixture.cases.filter((entry) => ["missing", "null", "object", "unknown", "zero", "negative", "fractional", "empty"].includes(entry.value))) {
    it(entry.name, async () => {
      const body = transcriptListResponse(1) as unknown as Record<string, unknown>;
      if (entry.value === "missing") delete body.harness_facets; else body.harness_facets = facets[entry.value];
      vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } })));
      const { client, wrapper } = makeQueryClientHarness();
      const view = renderHook(() => useTranscripts({ page: "1" }, { authScope: "viewer-a", requireHarnessFacets: true }), { wrapper });
      await waitFor(() => expect(view.result.current.status).toBe(entry.accepted ? "success" : "error"));
      const key = ["transcripts", "viewer-a", { page: "1" }];
      if (entry.accepted) expect(client.getQueryData(key)).toEqual(body);
      else { expect(view.result.current.error).toBeInstanceOf(TranscriptListHarnessFacetsError); expect(client.getQueryData(key)).toBeUndefined(); }
      vi.unstubAllGlobals();
    });
  }
  for (const scopeCase of fixture.cases.filter((entry) => ["same", "viewer-b", "anonymous"].includes(entry.value))) {
    it(scopeCase.name, async () => {
      let resolveNext: ((value: Response) => void) | undefined;
      const fetchMock = vi.fn()
        .mockResolvedValueOnce(new Response(JSON.stringify(transcriptListResponse(1, { total: 1 })), { status: 200, headers: { "content-type": "application/json" } }))
        .mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveNext = resolve; }));
      vi.stubGlobal("fetch", fetchMock);
      const { client, wrapper } = makeQueryClientHarness();
      const initial = { scope: "viewer-a", page: "1" };
      const view = renderHook(({ scope, page }) => useTranscripts({ page }, { authScope: scope, requireHarnessFacets: true }), { initialProps: initial, wrapper });
      await waitFor(() => expect(view.result.current.isSuccess).toBe(true));
      const nextScope = scopeCase.value === "same" ? "viewer-a" : scopeCase.value;
      view.rerender({ scope: nextScope, page: "2" });
      expect(view.result.current.data?.page).toBe(scopeCase.value === "same" ? 1 : undefined);
      act(() => resolveNext?.(new Response("failure", { status: 500 })));
      await waitFor(() => expect(view.result.current.isError).toBe(true));
      expect(view.result.current.data).toBeUndefined();
      if (scopeCase.value !== "same") expect(client.getQueryData(["transcripts", nextScope, { page: "2" }])).toBeUndefined();
      vi.unstubAllGlobals();
    });
  }
});
