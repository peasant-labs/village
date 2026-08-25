import { Suspense } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import UserProfilePage from "@/app/users/[username]/page";
import type {
  CollectiveSubmissionPair,
  ContributedCollective,
  ShareEvent,
  User,
} from "@/lib/types";

/**
 * Support for tests that mount the REAL profile route
 * (`UserProfilePage` inside the real `AuthProvider`) with REST mocked, the way
 * `mountedProductionRoute.tsx` does for the transcript route.
 *
 * The signed-in identity comes from the same `GET /auth/me` the app calls, so
 * "who is looking at this profile" is decided by the production code under
 * test rather than by a stubbed hook. That matters here: the contributed-
 * collectives section exists only on one's own profile, and a test that told
 * the component who the viewer was could not observe the app getting it wrong.
 */

export interface MountedProfileFixture {
  /** The profile being viewed. */
  profileUsername: string;
  /** The signed-in viewer, or null for an anonymous visitor (`/auth/me` 401s). */
  viewerUsername: string | null;
  /** `GET /users/me/collectives/contributions`. */
  contributions: ContributedCollective[];
  /**
   * `GET /users/me/collectives/{groupId}/submissions`, keyed by collective id
   * — the owner-only PAIRS source. Includes a pair whose latest event is a
   * withdrawal (`retracted`/`revoked`); that pair has no row in the legacy
   * current-state list but MUST have one here.
   */
  submissionsByGroupId?: Record<string, CollectiveSubmissionPair[]>;
  /** `GET /users/me/collectives/{groupId}/transcripts/{transcriptId}/events`. */
  eventsByGroupAndTranscript?: Record<string, ShareEvent[]>;
}

function userFixture(username: string): User {
  return {
    id: `user-${username}`,
    github_id: 1,
    github_username: username,
    display_name: username,
    avatar_url: null,
    created_at: "2026-01-01T00:00:00.000Z",
    updated_at: "2026-01-01T00:00:00.000Z",
    is_discoverable: true,
    username_chosen: true,
    provider_username: username,
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

/** The key `eventsByGroupAndTranscript` is indexed by. */
export function eventKey(groupId: string, transcriptId: string): string {
  return `${groupId}/${transcriptId}`;
}

/**
 * Stubs `fetch` for every call the profile route makes, and RECORDS the request
 * paths so a test can assert that an endpoint was never reached — which is how
 * "no section for this viewer" is proven to be an absence of the request too,
 * not merely an absence of pixels.
 *
 * An unexpected request throws, so a new fetch introduced on this route shows
 * up as a named failure rather than as a silent hang.
 */
export function installProfileRESTFixture(fixture: MountedProfileFixture): string[] {
  const requested: string[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const path = url.slice(url.indexOf("/api/v1") + "/api/v1".length);
    requested.push(path);

    if (path === "/auth/me") {
      return fixture.viewerUsername == null
        ? json({ error: "not signed in" }, 401)
        : json(userFixture(fixture.viewerUsername));
    }
    if (path === "/users/me/collectives/contributions") {
      return json({ collectives: fixture.contributions });
    }
    const eventsMatch = path.match(
      /^\/users\/me\/collectives\/([^/]+)\/transcripts\/([^/]+)\/events$/,
    );
    if (eventsMatch) {
      return json(
        fixture.eventsByGroupAndTranscript?.[eventKey(eventsMatch[1], eventsMatch[2])] ?? [],
      );
    }
    const submissionsMatch = path.match(/^\/users\/me\/collectives\/([^/]+)\/submissions$/);
    if (submissionsMatch) {
      return json(fixture.submissionsByGroupId?.[submissionsMatch[1]] ?? []);
    }
    if (path.startsWith("/transcripts?")) {
      return json({ transcripts: [], total: 0, agent_total: 0, page: 1, limit: 24 });
    }
    if (path.startsWith("/users/")) {
      return json(userFixture(fixture.profileUsername));
    }
    throw new Error(`mounted profile route fixture received an unexpected request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return requested;
}

/** Renders the real profile route for `username` behind a fresh, retry-disabled client. */
export async function renderProfileRoute(username: string): Promise<void> {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <AuthProvider>
          <Suspense fallback={<div>loading mounted profile</div>}>
            <UserProfilePage params={Promise.resolve({ username })} />
          </Suspense>
        </AuthProvider>
      </QueryClientProvider>,
    );
  });
}

/** Shared teardown; call once at module scope in each mounted-profile test file. */
export function installMountedProfileTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    localStorage.clear();
    document.documentElement.setAttribute("data-theme", "dark");
  });
}
