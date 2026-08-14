import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useTranscripts } from "@/lib/queries/transcripts";
import {
  makeQueryClientWrapper,
  transcriptListResponse as response,
} from "@/test/queryHookHelpers";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useTranscripts request wiring", () => {
  it("forwards the TanStack AbortSignal to the discovery fetch", async () => {
    const calls: Array<{ url: string; init: RequestInit | undefined }> = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init });
      return new Response(JSON.stringify(response(1)), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    const { result } = renderHook(
      () => useTranscripts({ sort: "recent", page: "1", limit: "24" }),
      { wrapper: makeQueryClientWrapper() },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(calls).toHaveLength(1);
    expect(calls[0].url).toContain("/transcripts?");
    expect(calls[0].init?.signal).toBeInstanceOf(AbortSignal);
  });

  it("keeps distinct page/filter intents on distinct cache entries", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const page = url.includes("page=2") ? 2 : 1;
      return new Response(JSON.stringify(response(page)), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fetchMock);
    const wrapper = makeQueryClientWrapper();

    const first = renderHook(
      () => useTranscripts({ sort: "recent", page: "1", limit: "24" }),
      { wrapper },
    );
    await waitFor(() => expect(first.result.current.data?.page).toBe(1));

    const second = renderHook(
      () => useTranscripts({ sort: "recent", page: "2", limit: "24" }),
      { wrapper },
    );
    await waitFor(() => expect(second.result.current.data?.page).toBe(2));

    // Two distinct request keys resolved to two distinct confirmed pages: the
    // complete key never collapses separate page intents onto one entry.
    const requestedPages = fetchMock.mock.calls
      .map(([input]) => String(input))
      .map((url) => (url.includes("page=2") ? 2 : 1));
    expect(new Set(requestedPages)).toEqual(new Set([1, 2]));
  });
});
