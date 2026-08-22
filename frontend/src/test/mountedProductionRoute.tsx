import { Suspense } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import TranscriptDetailPage from "@/app/transcripts/[id]/page";

/**
 * Shared support for tests that mount the REAL production route
 * (`TranscriptDetailPage` -> `SessionDetailV2` -> fairtrade's `TranscriptViewer`) with REST
 * mocked, rather than rendering `SessionDetailV2` with hand-built props. `mountedObservedModelRoute.test.tsx`
 * and `titleHeroAndBreadcrumb.test.tsx` each declared their own copies of this REST mock, the
 * route render, and the teardown; kept in one place so a third mounted-route test does not add a
 * third copy.
 */

/** The transcript-metadata REST shape (`GET /transcripts/{id}`) every mounted-route fixture
 *  installs. Each caller supplies its own field values — only the request-matching mechanics
 *  below are shared. */
export interface MountedRouteTranscriptMetadata {
  transcript: {
    id: string;
    local_id: string;
    visibility: string;
    title: string | null;
    description: string | null;
    project_name: string;
  };
  owner: { id: string };
  enriched_shares: unknown[];
}

/** Stubs `fetch` to serve exactly the four REST calls `TranscriptDetailPage` makes for one
 *  transcript id: metadata, content, annotations, and the caller's groups. `fixtureLabel` names
 *  the calling test file in the "unexpected request" error so a failure identifies its source. */
export function installRESTFixture(
  transcriptID: string,
  metadata: MountedRouteTranscriptMetadata,
  detail: SessionDetailPayload,
  fixtureLabel: string,
): ReturnType<typeof vi.fn> {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith(`/transcripts/${transcriptID}/content`)) {
      return new Response(JSON.stringify(detail), { status: 200, headers: { "content-type": "application/json" } });
    }
    if (url.endsWith(`/transcripts/${transcriptID}/annotations`)) {
      return new Response(JSON.stringify({ annotations: [] }), { status: 200, headers: { "content-type": "application/json" } });
    }
    if (url.endsWith(`/transcripts/${transcriptID}`)) {
      return new Response(JSON.stringify(metadata), { status: 200, headers: { "content-type": "application/json" } });
    }
    if (url.endsWith("/groups")) {
      return new Response(JSON.stringify([]), { status: 200, headers: { "content-type": "application/json" } });
    }
    throw new Error(`${fixtureLabel} fixture received an unexpected request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

/** Renders the real `TranscriptDetailPage` route for `transcriptID` behind a fresh,
 *  retry-disabled QueryClient — the same mount every mounted-route test asserts against. */
export async function renderProductionRoute(transcriptID: string): Promise<void> {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <Suspense fallback={<div>loading mounted transcript</div>}>
          <TranscriptDetailPage params={Promise.resolve({ id: transcriptID })} />
        </Suspense>
      </QueryClientProvider>,
    );
  });
}

/** Registers the shared teardown (call once at module scope in each mounted-route test file):
 *  unmount, drop the `fetch` stub, clear localStorage, and reset the document theme so no test
 *  leaks state into the next. */
export function installMountedRouteTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    localStorage.clear();
    document.documentElement.setAttribute("data-theme", "dark");
  });
}
