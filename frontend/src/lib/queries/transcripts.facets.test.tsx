import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TranscriptListHarnessFacetsError, useTranscripts } from "./transcripts";
import { makeQueryClientHarness, transcriptListResponse } from "@/test/queryHookHelpers";
import { loadExploreQueryBoundaryFixtures } from "@/test/exploreQueryBoundaryFixtures";
import { resolve } from "node:path";
import { loadExploreViewerFixtures, viewerResponse } from "@/test/exploreViewerFixtures";

const fixture = loadExploreQueryBoundaryFixtures();

describe("Explore discovery query trust and viewer scope", () => {
  it("retains the independent required-name inventory", () => {
    expect(fixture.length).toBeGreaterThan(0);
  });
  it("rejects duplicate fixture names", () => {
    expect(() => loadExploreQueryBoundaryFixtures(resolve(process.cwd(), "src/testdata/explore-query-boundary-invalid/duplicate.yaml"))).toThrow("unique");
  });
  it("rejects unsupported fixture dispatch", () => {
    expect(() => loadExploreQueryBoundaryFixtures(resolve(process.cwd(), "src/testdata/explore-query-boundary-invalid/unknown-kind.yaml"))).toThrow("unsupported explore query fixture kind");
  });
  for (const entry of fixture.filter((entry) => entry.kind === "facet")) {
    it(entry.name, async () => {
      const body = transcriptListResponse(1) as unknown as Record<string, unknown>;
      if (entry.operation === "omit") delete body.harness_facets; else body.harness_facets = entry.facets;
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
  for (const scopeCase of fixture.filter((entry) => entry.kind === "scope")) {
    it(scopeCase.name, async () => {
      let resolveNext: ((value: Response) => void) | undefined;
      const populated = viewerResponse(loadExploreViewerFixtures().viewers["viewer-a"], "viewer-a");
      const fetchMock = vi.fn()
        .mockResolvedValueOnce(new Response(JSON.stringify(populated), { status: 200, headers: { "content-type": "application/json" } }))
        .mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveNext = resolve; }));
      vi.stubGlobal("fetch", fetchMock);
      const { client, wrapper } = makeQueryClientHarness();
      const initial = { scope: "viewer-a", page: "1" };
      const view = renderHook(({ scope, page }) => useTranscripts({ page }, { authScope: scope, requireHarnessFacets: true }), { initialProps: initial, wrapper });
      await waitFor(() => expect(view.result.current.isSuccess).toBe(true));
      const nextScope = scopeCase.next_scope;
      view.rerender({ scope: nextScope, page: "2" });
      expect(view.result.current.data?.page).toBe(scopeCase.next_scope === "viewer-a" ? 1 : undefined);
      expect(view.result.current.data).toEqual(scopeCase.next_scope === "viewer-a" ? populated : undefined);
      expect(client.getQueryData(["transcripts", "viewer-a", { page: "1" }])).toEqual(populated);
      act(() => resolveNext?.(new Response("failure", { status: 500 })));
      await waitFor(() => expect(view.result.current.isError).toBe(true));
      expect(view.result.current.data).toBeUndefined();
      if (scopeCase.next_scope !== "viewer-a") expect(client.getQueryData(["transcripts", nextScope, { page: "2" }])).toBeUndefined();
      vi.unstubAllGlobals();
    });
  }
});
