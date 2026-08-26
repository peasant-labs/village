import { Suspense, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import UserProjectPage from "@/app/users/[username]/projects/[projectHash]/page";
import UserProfilePage from "@/app/users/[username]/page";
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

export interface ProjectRouteFixture {
  /** Signed-in viewer, or `null` for an anonymous one (`/auth/me` answers 401). */
  viewer: string | null;
  ownerUsername: string;
  projectHash: string;
  displayName: string;
  nameSource: NameSource;
  /** `""` when the project has no known git remote. */
  remoteLabel: string;
  transcriptTitles: string[];
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

function makeTranscript(title: string, index: number, fixture: ProjectRouteFixture): Transcript {
  return {
    id: `transcript-${index}`,
    owner_id: `user-${fixture.ownerUsername}`,
    local_id: `local-${index}`,
    title,
    description: null,
    visibility: "public",
    model_provider: "claude-code",
    model_name: "claude-fable-5",
    harness_version: null,
    session_start: "2026-08-20T09:00:00Z",
    session_end: "2026-08-20T09:30:00Z",
    turn_count: 12,
    token_count: 900,
    blob_size_bytes: null,
    schema_version: "0.13.0",
    published_at: "2026-08-20T10:00:00Z",
    updated_at: "2026-08-20T10:00:00Z",
    parent_session_id: null,
    ingested_at: null,
    source_format: null,
    git_branch: null,
    git_remote: null,
    project_hash: fixture.projectHash,
    project_name: fixture.displayName,
    project_display_name: fixture.displayName,
    project_name_source: fixture.nameSource,
    project_remote_label: fixture.remoteLabel,
    tool_call_count: null,
    subagent_count: null,
    duration_ms: null,
    tokens_in: null,
    tokens_out: null,
    subagents: null,
    diagnostics_warnings: null,
    diagnostics_partial: null,
    title_generated: null,
    outcome: null,
    files_touched: null,
    lines_changed: null,
    retry_loops: null,
    retry_tokens_wasted: null,
    within_session_reverts: null,
    signal_density: null,
    spec_quality_score: null,
    exploration_ratio: null,
    scope_breadth: null,
    discovery_turns: null,
    m2_token_outcome_ratio: null,
    m3_unique_tool_count: null,
    m4_error_recovery_count: null,
    m4_consecutive_error_max: null,
    m5_context_utilization_pct: null,
    m5_peak_context_tokens: null,
    m5_avg_message_tokens: null,
    m6_output_survival_pct: null,
    m6_lines_survived: null,
    m6_lines_total: null,
    m7_spec_word_count: null,
    m7_spec_has_examples: null,
    m7_spec_has_constraints: null,
    computed_at: null,
    compute_version: null,
    content_hash: null,
    license_id: null,
    session_origin: "user",
  };
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
    transcripts: fixture.transcriptTitles.map((t, i) =>
      makeTranscript(t, i, { ...fixture, displayName, nameSource }),
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
          transcript: makeTranscript(`session ${i}`, i, {
            viewer,
            ownerUsername: username,
            projectHash: p.projectHash,
            displayName: p.projectDisplayName,
            nameSource: "consented",
            remoteLabel: "",
            transcriptTitles: [],
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
    localStorage.clear();
    document.documentElement.setAttribute("data-theme", "dark");
  });
}
