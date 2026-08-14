import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  TranscriptListQueryErrorCode,
  TranscriptListResponseMismatchError,
  useTranscripts,
} from "@/lib/queries/transcripts";
import {
  makeQueryClientHarness,
  makeQueryClientWrapper,
  transcriptListResponse as response,
} from "@/test/queryHookHelpers";
import { loadTranscriptQueryValidationFixtures } from "@/test/transcriptQueryValidationFixtures";

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

const validationFixtures = loadTranscriptQueryValidationFixtures();

describe("useTranscripts response trust boundary", () => {
  for (const fixture of validationFixtures) {
    it(fixture.name, async () => {
      let responseIndex = 0;
      let releaseDeferred: (() => void) | null = null;
      const requestedURLs: string[] = [];
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        requestedURLs.push(String(input));
        const responseFixture = fixture.responses[responseIndex++];
        const makeResponse = () => new Response(
          JSON.stringify(response(responseFixture.page, {
            limit: responseFixture.limit,
            total: responseFixture.total,
          })),
          { status: 200, headers: { "content-type": "application/json" } },
        );
        if (fixture.action === "seededNewKey" && responseIndex === 3) {
          return new Promise<Response>((resolve) => {
            releaseDeferred = () => resolve(makeResponse());
          });
        }
        return Promise.resolve(makeResponse());
      });
      vi.stubGlobal("fetch", fetchMock);
      const { client, wrapper } = makeQueryClientHarness();
      const view = renderHook(
        ({ params }) => useTranscripts(params),
        { initialProps: { params: fixture.params }, wrapper },
      );

      await waitFor(() => expect(view.result.current.status).toBe(fixture.initialStatus));
      const initialKey = ["transcripts", fixture.params];
      if (fixture.initialStatus === "error") {
        const error = view.result.current.error;
        expect(error).toBeInstanceOf(TranscriptListResponseMismatchError);
        expect((error as TranscriptListResponseMismatchError).code).toBe(
          TranscriptListQueryErrorCode.ResponsePaginationMismatch,
        );
        for (const fragment of fixture.errorFragments) {
          expect((error as Error).message).toContain(fragment);
        }
        expect((error as Error).message.toLowerCase()).not.toContain("previously confirmed");
        expect(client.getQueryData(initialKey)).toBeUndefined();
        expect(client.getQueryState(initialKey)?.status).toBe("error");
      } else {
        expect(view.result.current.data?.page).toBe(fixture.responses[0].page);
        expect(client.getQueryData(initialKey)).toEqual(view.result.current.data);
      }

      if (fixture.action === "retry") {
        await act(async () => {
          await view.result.current.refetch();
        });
        await waitFor(() => expect(view.result.current.isSuccess).toBe(true));
        expect(view.result.current.data?.page).toBe(fixture.finalPage);
        expect(client.getQueryData(initialKey)).toEqual(view.result.current.data);
        expect(requestedURLs).toHaveLength(2);
        expect(requestedURLs.every((url) => url.includes("page=2"))).toBe(true);
      }

      if (fixture.action === "seededNewKey") {
        view.rerender({ params: fixture.mismatchParams! });
        await waitFor(() => expect(view.result.current.isError).toBe(true));
        const mismatchError = view.result.current.error as TranscriptListResponseMismatchError;
        expect(mismatchError).toBeInstanceOf(TranscriptListResponseMismatchError);
        for (const fragment of fixture.errorFragments) expect(mismatchError.message).toContain(fragment);
        expect(client.getQueryData(["transcripts", fixture.mismatchParams])).toBeUndefined();

        view.rerender({ params: fixture.nextParams! });
        await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
        // The only possible placeholder is the previously validated page 1. The
        // rejected page 5 never appears while page 3 is pending.
        expect(view.result.current.data?.page).not.toBe(5);
        if (view.result.current.data != null) expect(view.result.current.data.page).toBe(1);
        act(() => releaseDeferred?.());
        await waitFor(() => expect(view.result.current.data?.page).toBe(fixture.finalPage));
      }
    });
  }
});
