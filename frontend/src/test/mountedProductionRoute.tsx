import { Suspense } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import type { NameSource } from "@/lib/types";
import type { SessionOrigin } from "@/lib/sessionOrigin";
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
    /** The resolved project identity fields. Optional: a caller that does
     *  not exercise project-identity behavior gets sensible defaults from
     *  {@link installRESTFixture} (`project_display_name` falls back to
     *  `project_name`, matching this fixture's earlier, name-only behavior
     *  exactly, so existing callers need no changes). A caller that DOES
     *  exercise it (e.g. the mixed-name-same-hash or breadcrumb href cases)
     *  supplies these explicitly. */
    project_hash?: string;
    project_display_name?: string;
    project_name_source?: NameSource;
    project_remote_label?: string | null;
    /** Who drove the session; omitted by fixtures that do not exercise it. */
    session_origin?: SessionOrigin;
  };
  owner: {
    id: string;
    /** Defaults to `"fixture-owner"` when omitted — see
     *  `project_hash` above for why callers that don't exercise the
     *  breadcrumb href need not set this. */
    github_username?: string;
  };
  enriched_shares: unknown[];
  /** The approved memberships `GET /transcripts/{id}/collectives` serves to
   *  THIS viewer. Defaults to the empty list the server sends whenever the
   *  collective-visibility rule or the owner's contributor opt-in withholds
   *  everything — which is also what a transcript in no collective gets, by
   *  design. Callers that do not exercise the memberships omit it. */
  viewer_collectives?: Array<{ id: string; name: string }>;
}

const DEFAULT_PROJECT_HASH = "0".repeat(64);
const DEFAULT_OWNER_USERNAME = "fixture-owner";

/** Fills in the resolved project-identity fields (and the owner username the
 *  breadcrumb href needs) with defaults that reproduce this fixture's
 *  earlier, name-only behavior, so existing mounted-route fixtures that
 *  predate the resolved-name wire fields keep passing unmodified. */
function withProjectIdentityDefaults(
  metadata: MountedRouteTranscriptMetadata,
): MountedRouteTranscriptMetadata {
  return {
    ...metadata,
    transcript: {
      ...metadata.transcript,
      project_hash: metadata.transcript.project_hash ?? DEFAULT_PROJECT_HASH,
      project_display_name:
        metadata.transcript.project_display_name ?? metadata.transcript.project_name,
      project_name_source: metadata.transcript.project_name_source ?? "consented",
      project_remote_label: metadata.transcript.project_remote_label ?? null,
    },
    owner: {
      ...metadata.owner,
      github_username: metadata.owner.github_username ?? DEFAULT_OWNER_USERNAME,
    },
  };
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
  const resolvedMetadata = withProjectIdentityDefaults(metadata);
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith(`/transcripts/${transcriptID}/content`)) {
      return new Response(JSON.stringify(detail), { status: 200, headers: { "content-type": "application/json" } });
    }
    if (url.endsWith(`/transcripts/${transcriptID}/collectives`)) {
      // Always 200 with a list, never a refusal: a refusal status would
      // itself confirm that memberships exist and are being withheld.
      return new Response(
        JSON.stringify({
          collectives: (resolvedMetadata.viewer_collectives ?? []).map((c) => ({
            ...c,
            description: null,
            linked_github_org: null,
            shared_at: "2026-08-01T00:00:00.000Z",
          })),
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    }
    if (url.endsWith(`/transcripts/${transcriptID}/annotations`)) {
      return new Response(JSON.stringify({ annotations: [] }), { status: 200, headers: { "content-type": "application/json" } });
    }
    if (url.endsWith(`/transcripts/${transcriptID}`)) {
      return new Response(JSON.stringify(resolvedMetadata), { status: 200, headers: { "content-type": "application/json" } });
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
