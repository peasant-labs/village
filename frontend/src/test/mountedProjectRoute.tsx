import { Suspense, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import UserProjectPage from "@/app/users/[username]/projects/[projectHash]/page";
import UserProfilePage from "@/app/users/[username]/page";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import type {
  NameSource,
  ProjectCollectiveRollupEntry,
  Transcript,
  User,
  UserProjectPageResponse,
} from "@/lib/types";

/**
 * Mount support for the REAL project-page route
 * (`/users/{username}/projects/{projectHash}`) and the REAL profile route that
 * links into it, with REST stubbed at `fetch`.
 *
 * Both routes are mounted exactly as the app mounts them — inside the
 * `AuthProvider` the owner-only control reads from — so a test asserts what a
 * user sees, not what a component would render given hand-built props.
 */

/** One recorded outbound request, so a test can assert the ROUTE a control hit. */
export interface RecordedRequest {
  method: string;
  url: string;
  body: unknown;
}

/**
 * One transcript row in the project payload.
 *
 * `title` is all most cases say. A case about a session another session started
 * also names the harness session ids, because the fold matches a row's
 * `parentSessionID` against another row's `localID`.
 */
export interface ProjectTranscriptRow {
  title: string;
  /** The transcript's own id, which its row links to. Defaults to
   *  `transcript-N`; a case whose assertions name rows sets it. */
  id?: string;
  /** The id the recording harness used. Defaults to a per-index `local-N`. */
  localID?: string;
  /** The harness id of the session that started this one, or null. */
  parentSessionID?: string | null;
}

export interface ProjectRouteFixture {
  /** Signed-in viewer, or `null` for an anonymous one (`/auth/me` answers 401). */
  viewer: string | null;
  ownerUsername: string;
  projectHash: string;
  displayName: string;
  nameSource: NameSource;
  /** `""` when the project has no known git remote. */
  remoteLabel: string;
  transcripts: ProjectTranscriptRow[];
  collectives: ProjectCollectiveRollupEntry[];
  /** When set, the project route answers this status instead of a payload. */
  errorStatus?: number;
  /** Error body message paired with {@link errorStatus}. */
  errorMessage?: string;
  /** Identity the correction routes answer with, simulating the server's re-resolution. */
  afterCorrection?: {
    displayName: string;
    nameSource: NameSource;
  };
}

function makeUser(username: string): User {
  return {
    id: `user-${username}`,
    github_id: 1,
    github_username: username,
    display_name: username,
    avatar_url: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    is_discoverable: true,
    username_chosen: true,
    provider_username: username,
  };
}

function makeTranscript(
  row: ProjectTranscriptRow,
  index: number,
  fixture: ProjectRouteFixture,
): Transcript {
  // Only the fields this route's cases are about; the rest of the wire row
  // comes from the shared fixture builder.
  return makeTranscriptFixture({
    id: row.id ?? `transcript-${index}`,
    owner_id: `user-${fixture.ownerUsername}`,
    local_id: row.localID ?? `local-${index}`,
    parent_session_id: row.parentSessionID ?? null,
    title: row.title,
    project_hash: fixture.projectHash,
    project_name: fixture.displayName,
    project_display_name: fixture.displayName,
    project_name_source: fixture.nameSource,
    project_remote_label: fixture.remoteLabel,
  });
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });

/**
 * Stubs `fetch` for one project-page fixture and returns the list every request
 * is appended to, so a test can assert the METHOD and PATH a control triggered
 * rather than inspecting a mutation object.
 */
export function installProjectRouteREST(fixture: ProjectRouteFixture): RecordedRequest[] {
  const requests: RecordedRequest[] = [];
  const owner = makeUser(fixture.ownerUsername);
  let displayName = fixture.displayName;
  let nameSource = fixture.nameSource;

  const projectPayload = (): UserProjectPageResponse => ({
    project: {
      project_hash: fixture.projectHash,
      project_display_name: displayName,
      project_name_source: nameSource,
      project_remote_label: fixture.remoteLabel,
    },
    owner,
    transcripts: fixture.transcripts.map((row, i) =>
      makeTranscript(row, i, { ...fixture, displayName, nameSource }),
    ),
    collectives: fixture.collectives,
  });

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();
    let body: unknown = null;
    if (typeof init?.body === "string") {
      try {
        body = JSON.parse(init.body);
      } catch {
        body = init.body;
      }
    }
    requests.push({ method, url, body });

    if (url.endsWith("/auth/me")) {
      if (fixture.viewer == null) {
        return json({ error: "unauthenticated" }, 401);
      }
      return json(makeUser(fixture.viewer));
    }
    if (method === "PATCH" && /\/users\/me\/projects\/[^/]+$/.test(url)) {
      const next = fixture.afterCorrection;
      displayName = next?.displayName ?? String((body as { display_name?: string })?.display_name ?? displayName);
      nameSource = next?.nameSource ?? "override";
      return json({
        project_hash: fixture.projectHash,
        project_display_name: displayName,
        project_name_source: nameSource,
        project_remote_label: fixture.remoteLabel,
      });
    }
    if (method === "DELETE" && url.endsWith("/display-name")) {
      const next = fixture.afterCorrection;
      displayName = next?.displayName ?? displayName;
      nameSource = next?.nameSource ?? "consented";
      return json({
        project_hash: fixture.projectHash,
        project_display_name: displayName,
        project_name_source: nameSource,
        project_remote_label: fixture.remoteLabel,
      });
    }
    if (url.includes("/projects/")) {
      if (fixture.errorStatus != null) {
        return json({ error: fixture.errorMessage ?? "refused" }, fixture.errorStatus);
      }
      return json(projectPayload());
    }
    throw new Error(`project-page fixture received an unexpected ${method} request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return requests;
}

/** One project group as the PROFILE route's transcript list reports it. */
export interface ProfileProjectFixture {
  projectHash: string;
  projectDisplayName: string;
}

/** Stubs `fetch` for the profile route that links into the project page. */
export function installProfileRouteREST(
  username: string,
  viewer: string | null,
  projects: ProfileProjectFixture[],
  /** When set, `GET /users/{username}` answers this status instead of a
   *  profile. That is the only way to reach the route's own not-found state,
   *  which renders its own crumbs and its own way back. */
  profileStatus?: number,
): void {
  const owner = makeUser(username);
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();
    if (url.endsWith("/auth/me")) {
      if (viewer == null) return json({ error: "unauthenticated" }, 401);
      return json(makeUser(viewer));
    }
    if (url.includes("/transcripts?")) {
      return json({
        transcripts: projects.map((p, i) => ({
          transcript: makeTranscript({ title: `session ${i}` }, i, {
            viewer,
            ownerUsername: username,
            projectHash: p.projectHash,
            displayName: p.projectDisplayName,
            nameSource: "consented",
            remoteLabel: "",
            transcripts: [],
            collectives: [],
          }),
          tags: [],
          owner,
        })),
        total: projects.length,
        agent_total: 0,
        page: 1,
        limit: 20,
      });
    }
    if (/\/users\/[^/?]+$/.test(url)) {
      if (profileStatus != null && profileStatus >= 400) {
        return json({ error: "not found" }, profileStatus);
      }
      return json(owner);
    }
    throw new Error(`profile fixture received an unexpected ${method} request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
}

function Providers({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return (
    <QueryClientProvider client={client}>
      <AuthProvider>
        <Suspense fallback={<div>loading</div>}>{children}</Suspense>
      </AuthProvider>
    </QueryClientProvider>
  );
}

/** Renders the real project-page route for one `(username, projectHash)`. */
export async function renderProjectRoute(username: string, projectHash: string): Promise<void> {
  await act(async () => {
    render(
      <Providers>
        <UserProjectPage params={Promise.resolve({ username, projectHash })} />
      </Providers>,
    );
  });
}

/** Renders the real profile route whose project headings link into the page. */
export async function renderProfileRoute(username: string): Promise<void> {
  await act(async () => {
    render(
      <Providers>
        <UserProfilePage params={Promise.resolve({ username })} />
      </Providers>,
    );
  });
}

/** Shared teardown: unmount, drop the `fetch` stub, reset the document theme. */
export function installProjectRouteTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    globalThis.localStorage?.clear();
    document.documentElement.setAttribute("data-theme", "dark");
  });
}
