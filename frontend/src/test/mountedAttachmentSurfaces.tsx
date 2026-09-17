import { Suspense, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import PullRequestPage from "@/app/pulls/[owner]/[name]/[number]/page";
import GroupSettingsPage from "@/app/groups/[id]/settings/page";
import UserProfilePage from "@/app/users/[username]/page";
import type { PromptDigest, VillagePullRequestAttachmentResponse } from "@peasant-labs/schema";

/**
 * Mount support for the real routes this change touches: the pull request page,
 * the collective settings page, and the signed-in user's own profile rail, with
 * REST stubbed at `fetch` and every outbound request recorded so a test asserts
 * the route a control hit rather than inspecting a mutation object.
 *
 * The pull request page reads everything it needs from its own payload, so it
 * mounts without an auth provider; the two settings surfaces read the caller, so
 * they mount inside the provider the app mounts them in.
 */

/** One recorded outbound request. */
export interface RecordedRequest {
  method: string;
  url: string;
  body: unknown;
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });

function QueryOnly({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return (
    <QueryClientProvider client={client}>
      <Suspense fallback={<div>loading</div>}>{children}</Suspense>
    </QueryClientProvider>
  );
}

function WithAuth({ children }: { children: ReactNode }) {
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

// ---------------------------------------------------------------------------
// Pull request page
// ---------------------------------------------------------------------------

export interface PullRequestFixture {
  owner: string;
  name: string;
  number: number;
  viewerIsAuthor: boolean;
  attachment: VillagePullRequestAttachmentResponse["attachment"];
  digest: PromptDigest | null;
  transcripts: VillagePullRequestAttachmentResponse["transcripts"];
  /** Status a confirm answers; 200 (or unset) returns the attached response. */
  confirmStatus?: number;
  confirmMessage?: string;
}

/**
 * A minimal but complete digest, so every branch of the renderer is exercised.
 * It advertises one transcript per id it is given, because the page decides a
 * row is "not available" by asking whether the digest still carries its id.
 */
export function makeDigest(
  transcriptIds: string[] = ["11111111-1111-1111-1111-111111111111"],
): PromptDigest {
  const timestamp = "2026-01-01T00:00:00Z";
  const items: PromptDigest["items"] = [];
  transcriptIds.forEach((transcriptId, index) => {
    items.push(
      { kind: "session", transcriptId, timestamp, text: `session ${index + 1}`, promptCount: 2, commitCount: 1 },
      {
        kind: "prompt",
        transcriptId,
        timestamp,
        text: index === 0 ? "please add the picker" : `prompt for ${transcriptId}`,
        ordinal: 1,
        turnIndex: 0,
      },
      { kind: "skill", transcriptId, timestamp, text: "/commit", args: "-m seed", turnIndex: 1 },
      {
        kind: "commit",
        transcriptId,
        timestamp,
        text: "abc1234",
        commitSha: "abc1234000000000000000000000000000000001",
        additions: 3,
        deletions: 1,
        filesChanged: 2,
      },
    );
  });
  return {
    header: {
      sessionCount: transcriptIds.length,
      promptCount: 2,
      commitsCovered: 1,
      commitsTotal: 2,
      harness: "claude-code",
      redactionLevel: "standard",
      villageUrl: "https://village.test",
    },
    skills: [{ name: "commit", invocationCount: 1 }],
    items,
  };
}

export function makeAttachmentResponse(fixture: PullRequestFixture): VillagePullRequestAttachmentResponse {
  return {
    attachment: fixture.attachment,
    digest: fixture.digest,
    transcripts: fixture.transcripts,
    viewer_is_author: fixture.viewerIsAuthor,
  };
}

export function installPullRequestREST(fixture: PullRequestFixture): RecordedRequest[] {
  const requests: RecordedRequest[] = [];
  let attachment = { ...fixture.attachment };
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

    if (method === "POST" && url.endsWith("/confirm")) {
      if (fixture.confirmStatus && fixture.confirmStatus >= 400) {
        return json({ error: fixture.confirmMessage ?? "refused" }, fixture.confirmStatus);
      }
      attachment = { ...attachment, state: "attached" };
      return json(makeAttachmentResponse({ ...fixture, attachment }));
    }
    if (method === "DELETE") {
      attachment = { ...attachment, state: "detached" };
      return json(makeAttachmentResponse({ ...fixture, attachment }));
    }
    return json(makeAttachmentResponse({ ...fixture, attachment }));
  });
  vi.stubGlobal("fetch", fetchMock);
  return requests;
}

export async function renderPullRequestRoute(owner: string, name: string, number: number): Promise<void> {
  await act(async () => {
    render(
      <QueryOnly>
        <PullRequestPage params={Promise.resolve({ owner, name, number: String(number) })} />
      </QueryOnly>,
    );
  });
}

// ---------------------------------------------------------------------------
// Collective settings
// ---------------------------------------------------------------------------

export interface GroupSettingsFixture {
  id: string;
  ownerUsername: string;
  postPromptsCheck: boolean;
  promptsCheckMode: "informational" | "required";
}

export function installGroupSettingsREST(fixture: GroupSettingsFixture): RecordedRequest[] {
  const requests: RecordedRequest[] = [];
  const user = {
    id: "22222222-2222-2222-2222-222222222222",
    github_username: fixture.ownerUsername,
    display_name: fixture.ownerUsername,
    avatar_url: "",
    is_discoverable: true,
    created_at: "2026-01-01T00:00:00Z",
  };
  const group = {
    id: fixture.id,
    name: "fixture-collective",
    description: "",
    acceptance_mode: "open",
    data_access: "members_only",
    linked_github_org: null,
    display_members: true,
    transcript_deletion_policy: "user_choice",
    post_prompts_check: fixture.postPromptsCheck,
    prompts_check_mode: fixture.promptsCheckMode,
  };
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
      return json(user);
    }
    if (url.includes("/orgs")) {
      return json([]);
    }
    if (method === "PATCH" && /\/groups\/[^/]+$/.test(url)) {
      return json({ ...group, ...(body as object) });
    }
    if (url.includes("/groups/")) {
      return json({
        group,
        members: [],
        transcripts: [],
        stats: {
          contributor_count: 1,
          total_duration_ms: 0,
          total_tokens: 0,
          total_transcripts: 0,
          total_turns: 0,
        },
        models: [],
        contributors: [],
        can_read: true,
        your_role: "owner",
      });
    }
    throw new Error(`group settings fixture received an unexpected ${method} request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return requests;
}

export async function renderGroupSettingsRoute(id: string): Promise<void> {
  await act(async () => {
    render(
      <WithAuth>
        <GroupSettingsPage params={Promise.resolve({ id })} />
      </WithAuth>,
    );
  });
}

// ---------------------------------------------------------------------------
// The signed-in user's own profile
// ---------------------------------------------------------------------------

export interface ProfileSettingsFixture {
  username: string;
  previewBeforeAttach: boolean;
}

export function installProfileSettingsREST(fixture: ProfileSettingsFixture): RecordedRequest[] {
  const requests: RecordedRequest[] = [];
  let settings = { preview_before_attach: fixture.previewBeforeAttach };
  const user = {
    id: "33333333-3333-3333-3333-333333333333",
    github_username: fixture.username,
    display_name: fixture.username,
    avatar_url: "",
    is_discoverable: true,
    created_at: "2026-01-01T00:00:00Z",
  };
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

    if (url.endsWith("/users/me/settings")) {
      if (method === "PATCH") {
        settings = { preview_before_attach: Boolean((body as { preview_before_attach?: boolean })?.preview_before_attach) };
      }
      return json(settings);
    }
    if (url.endsWith("/auth/me")) {
      return json(user);
    }
    if (url.includes("/transcripts")) {
      return json({ transcripts: [], total: 0, agent_total: 0 });
    }
    if (url.includes("/users/")) {
      return json(user);
    }
    if (url.includes("/collectives")) {
      return json({ collectives: [] });
    }
    throw new Error(`profile fixture received an unexpected ${method} request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return requests;
}

export async function renderProfileSettingsRoute(username: string): Promise<void> {
  await act(async () => {
    render(
      <WithAuth>
        <UserProfilePage params={Promise.resolve({ username })} />
      </WithAuth>,
    );
  });
}

/** Shared teardown: unmount, drop the `fetch` stub, reset the document theme. */
export function installAttachmentSurfacesTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    globalThis.localStorage?.clear();
    document.documentElement.setAttribute("data-theme", "dark");
  });
}
