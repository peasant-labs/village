import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import RootPage from "@/app/page";
import ExploreRoute from "@/app/explore/page";
import Navbar from "@/components/layout/Navbar";
import type {
  HomeCollectiveCase,
  HomeContributionCase,
  HomeRequestFailure,
  HomeStatsCase,
  HomeTranscriptCase,
} from "@/test/homePageFixtures";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import type { TranscriptListItem, User } from "@/lib/types";

/**
 * Support for tests that mount the REAL root route (`/`) and the REAL explore
 * route (`/explore`), each inside the real `AuthProvider`, optionally under the
 * REAL header (`Navbar`) the app shell mounts above every page.
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
  /** What `GET /users/me/stats` answers. Defaults to all zero. */
  stats?: HomeStatsCase;
  /** What `GET /groups` answers: the caller's memberships. Defaults to none. */
  collectives?: HomeCollectiveCase[];
  /** What `GET /users/me/collectives/contributions` answers. Defaults to none. */
  contributions?: HomeContributionCase[];
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
      visibility: t.visibility ?? ((t.sharedWith ?? []).length > 0 ? "shared" : "private"),
    }),
    tags: [],
    owner,
    // As the list serves them: only the collectives that APPROVED the row.
    shares: (t.sharedWith ?? []).map((name, i) => ({
      transcript_id: t.id,
      group_id: `00000000-0000-4000-8000-${String(i).padStart(12, "0")}`,
      group_name: name,
      acceptance_mode: "open",
      status: "approved",
      shared_at: t.publishedAt,
    })),
    pull_requests: {
      count: t.pullRequests?.count ?? 0,
      recent: (t.pullRequests?.recent ?? []).map((ref) => {
        const [refOwner, name] = ref.repo.split("/");
        return { owner: refOwner, name, number: ref.number };
      }),
    },
  };
}

function groupRow(c: HomeCollectiveCase) {
  return {
    id: c.id,
    name: c.name,
    description: null,
    created_by: "user-fixture-owner",
    created_at: "2026-01-01T00:00:00.000Z",
    updated_at: "2026-01-01T00:00:00.000Z",
    acceptance_mode: "open",
    data_access: "members",
    display_members: true,
    linked_github_org: null,
    transcript_deletion_policy: "retain",
    role: c.role,
    member_since: "2026-01-01T00:00:00.000Z",
    member_count: c.members,
    transcript_count: 0,
  };
}

function contributionRow(c: HomeContributionCase) {
  return {
    id: c.id,
    name: c.name,
    description: null,
    linked_github_org: null,
    approved_count: 1,
    pending_count: c.pending,
    rejected_attempt_count: 0,
    withdrawn_attempt_count: 0,
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
  /** Every `POST /auth/logout` the header's sign out sent. */
  logouts: number;
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
  const backend = { logouts: 0 };
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const path = url.slice(url.indexOf("/api/v1") + "/api/v1".length);
    requested.push(path);

    if (path === "/auth/logout") {
      if ((init?.method ?? "GET").toUpperCase() !== "POST") {
        throw new Error(`mounted home route fixture: /auth/logout must be a POST, got ${init?.method}`);
      }
      backend.logouts += 1;
      return json({ status: "logged out" });
    }
    if (path === "/auth/me") {
      return viewer == null
        ? json({ error: "not signed in" }, 401)
        : json(userFixture(viewer, chosen));
    }
    if (path === "/users/me/stats") {
      const stats = fixture.stats;
      return json({
        total_transcripts: stats?.transcripts ?? 0,
        total_turns: stats?.turns ?? 0,
        total_duration_ms: stats?.durationMs ?? 0,
        total_tokens: stats?.tokens ?? 0,
        pull_request_count: stats?.pullRequests ?? 0,
      });
    }
    if (path === "/groups") return json((fixture.collectives ?? []).map(groupRow));
    if (path === "/users/me/collectives/contributions") {
      return json({ collectives: (fixture.contributions ?? []).map(contributionRow) });
    }
    if (path.startsWith("/tags/popular")) return json([]);
    if (path.startsWith("/groups/search")) return json({ collectives: [] });
    if (path.startsWith("/transcripts")) {
      // The grouped helper read is a separate, opt-in view of the same route. It
      // is answered here (rather than through the flat owner-request failure
      // schedule) so a page's helper groups can never change the flat list's
      // retry behaviour under test.
      if (path.includes("view=grouped")) {
        const groupedQuery = new URLSearchParams(path.slice(path.indexOf("?") + 1));
        return json({
          items: [],
          page: Number(groupedQuery.get("page") ?? 1),
          limit: Number(groupedQuery.get("limit") ?? 20),
          totalItems: 0,
          ordinarySessionTotal: 0,
          helperThreadTotal: 0,
        });
      }
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
      // The owner-scoped list is served the way the list handler serves it: the
      // query matched anywhere in the title ignoring case, then one page of the
      // matches at the requested page and limit, in the order the case wrote
      // them. Discovery's own request is answered with no rows.
      const query = new URLSearchParams(path.includes("?") ? path.slice(path.indexOf("?") + 1) : "");
      const q = (query.get("q") ?? "").toLowerCase();
      const page = Number(query.get("page") ?? 1);
      const limit = Number(query.get("limit") ?? 24);
      const matching = isOwnerScoped
        ? fixture.transcripts.filter((t) => t.title.toLowerCase().includes(q))
        : [];
      const rows = matching
        .slice((page - 1) * limit, page * limit)
        .map((t) => listItem(t, owner));
      return json({
        transcripts: rows,
        total: matching.length,
        agent_total: 0,
        page,
        limit,
      });
    }
    throw new Error(`mounted home route fixture received an unexpected request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return {
    requested,
    get logouts() {
      return backend.logouts;
    },
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

/** The real page registered at `path`. */
function routeElement(path: string): React.ReactElement {
  if (path === "/") return <RootPage />;
  if (path === "/explore") return <ExploreRoute />;
  throw new Error(`mounted home route fixture has no route registered at ${path}`);
}

/**
 * Renders the real route registered at `path`. With `header`, the real
 * `Navbar` is mounted above it exactly as the app shell mounts it, and the
 * jsdom location is moved to `path` first so the header reads the same
 * pathname a real navigation would leave behind.
 */
export async function renderAppRoute(
  path: string,
  options: { header?: boolean } = {},
): Promise<void> {
  const page = routeElement(path);
  if (!options.header) return renderRoute(page);
  window.history.replaceState({}, "", path);
  return renderRoute(
    <>
      <Navbar />
      <main>{page}</main>
    </>,
  );
}

/**
 * Renders the real header alone at `pathname`, for a test about the chrome
 * rather than the page beneath it.
 */
export async function renderHeaderAt(pathname: string): Promise<void> {
  window.history.replaceState({}, "", pathname);
  return renderRoute(<Navbar />);
}

/** Shared teardown; call once at module scope in each mounted-home test file. */
export function installHomeRouteTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    globalThis.localStorage?.clear();
    window.history.replaceState({}, "", "/");
    document.documentElement.setAttribute("data-theme", "dark");
  });
}
