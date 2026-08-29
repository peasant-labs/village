import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import RootPage from "@/app/page";
import ExploreRoute from "@/app/explore/page";
import type { HomeRequestFailure, HomeTranscriptCase } from "@/test/homePageFixtures";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import type { TranscriptListItem, User } from "@/lib/types";

/**
 * Support for tests that mount the REAL root route (`/`) and the REAL explore
 * route (`/explore`), each inside the real `AuthProvider`.
 *
 * The signed-in identity comes from the same `GET /auth/me` the app calls, so
 * WHICH surface `/` serves is decided by the production code under test rather
 * than by a stubbed hook. That is the whole point of the route: a test that
 * told the page who the visitor was could not observe it getting that wrong.
 */

export interface MountedHomeFixture {
  /** The signed-in visitor, or null for an anonymous one (`/auth/me` 401s). */
  viewerUsername: string | null;
  /** The rows `GET /transcripts?owner=…` serves for the viewer. */
  transcripts: HomeTranscriptCase[];
  /**
   * How the owner-scoped list request behaves. `always` fails every attempt;
   * `after-first-answer` answers once and fails every later attempt, which is
   * the failed-REFRESH case where rows are already on screen. Discovery's own
   * unscoped request always succeeds, so a failure on this page is observably
   * the home request's and not the whole stub's.
   */
  ownerRequestFailure?: HomeRequestFailure;
  /** Whether the account claims a chosen handle. Defaults to true. */
  usernameChosen?: boolean;
}

function userFixture(username: string, usernameChosen = true): User {
  return {
    id: `user-${username}`,
    github_id: 1,
    github_username: username,
    display_name: username,
    avatar_url: null,
    created_at: "2026-01-01T00:00:00.000Z",
    updated_at: "2026-01-01T00:00:00.000Z",
    is_discoverable: true,
    username_chosen: usernameChosen,
    provider_username: username,
  };
}

function listItem(t: HomeTranscriptCase, owner: User): TranscriptListItem {
  return {
    // Only the fields these cases are about; the rest of the wire row comes
    // from the shared fixture builder.
    transcript: makeTranscriptFixture({
      id: t.id,
      local_id: t.localID ?? t.id,
      parent_session_id: t.parentSessionID ?? null,
      owner_id: owner.id,
      title: t.title,
      project_name: t.projectDisplayName,
      project_hash: t.projectHash,
      project_display_name: t.projectDisplayName,
      published_at: t.publishedAt,
      updated_at: t.publishedAt,
    }),
    tags: [],
    owner,
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

/** What a test holds over the stubbed backend after installing it. */
export interface MountedHomeBackend {
  /** Every path either route requested, in order. */
  requested: string[];
  /**
   * Stop failing the owner-scoped request, so the NEXT attempt answers. Lets a
   * test prove a surface recovers rather than only that it can be reached.
   */
  heal(): void;
  /**
   * Hold every owner-scoped answer open until {@link release} is called. A
   * request that resolves immediately passes through its in-flight state in a
   * frame no assertion can catch, so a surface that reports "working on it"
   * needs the request to actually still be working.
   */
  hold(): void;
  release(): void;
  /**
   * Serve a DIFFERENT signed-in person from `/auth/me`. The session query is an
   * ordinary query with focus refetching left on, so the handle can change
   * while this page stays mounted — which is the only way the owner-keyed
   * failure memory is ever consulted before it is cleared.
   */
  setViewer(username: string): void;
}

/**
 * Stubs `fetch` for every call either route makes, and RECORDS the request
 * paths so a test can assert that an endpoint was never reached — which is how
 * "the other surface did not render" is proven to be an absence of its request
 * too, not merely an absence of pixels.
 *
 * An unexpected request throws, so a new fetch introduced on either route
 * shows up as a named failure rather than as a silent hang.
 */
export function installHomeRouteREST(fixture: MountedHomeFixture): MountedHomeBackend {
  const requested: string[] = [];
  const chosen = fixture.usernameChosen ?? true;
  let failure: HomeRequestFailure = fixture.ownerRequestFailure ?? "never";
  const owner = userFixture(fixture.viewerUsername ?? "anon", chosen);
  let ownerAnswers = 0;
  let viewer = fixture.viewerUsername;
  let held: Promise<void> | null = null;
  let releaseHeld: (() => void) | null = null;
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const path = url.slice(url.indexOf("/api/v1") + "/api/v1".length);
    requested.push(path);

    if (path === "/auth/me") {
      return viewer == null
        ? json({ error: "not signed in" }, 401)
        : json(userFixture(viewer, chosen));
    }
    if (path.startsWith("/tags/popular")) return json([]);
    if (path.startsWith("/groups/search")) return json({ collectives: [] });
    if (path.startsWith("/transcripts")) {
      // The owner-scoped request is the home page's; every other transcripts
      // request belongs to discovery, which these cases render empty.
      const isOwnerScoped = path.includes("owner=");
      if (isOwnerScoped && held != null) await held;
      if (isOwnerScoped) {
        const failsNow =
          failure === "always" || (failure === "after-first-answer" && ownerAnswers > 0);
        ownerAnswers += 1;
        if (failsNow) {
          return json({ error: "the session list is unavailable" }, 500);
        }
      }
      const rows = isOwnerScoped ? fixture.transcripts.map((t) => listItem(t, owner)) : [];
      return json({
        transcripts: rows,
        total: rows.length,
        agent_total: 0,
        page: 1,
        limit: 24,
      });
    }
    throw new Error(`mounted home route fixture received an unexpected request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return {
    requested,
    heal() {
      failure = "never";
    },
    hold() {
      // A second hold would overwrite the first without resolving it, stranding
      // any request already waiting on it: the test would then die of a bare
      // timeout with nothing saying why.
      if (held != null) {
        throw new Error(
          "mounted home route fixture: a request is already held; release it before holding again",
        );
      }
      held = new Promise<void>((resolve) => {
        releaseHeld = resolve;
      });
    },
    setViewer(username: string) {
      viewer = username;
    },
    release() {
      const resolve = releaseHeld;
      held = null;
      releaseHeld = null;
      resolve?.();
    },
  };
}

async function renderRoute(element: React.ReactElement): Promise<void> {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <AuthProvider>{element}</AuthProvider>
      </QueryClientProvider>,
    );
  });
}

/** Renders the real route registered at `path`. */
export async function renderAppRoute(path: string): Promise<void> {
  if (path === "/") return renderRoute(<RootPage />);
  if (path === "/explore") return renderRoute(<ExploreRoute />);
  throw new Error(`mounted home route fixture has no route registered at ${path}`);
}

/** Shared teardown; call once at module scope in each mounted-home test file. */
export function installHomeRouteTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    localStorage.clear();
    document.documentElement.setAttribute("data-theme", "dark");
  });
}
