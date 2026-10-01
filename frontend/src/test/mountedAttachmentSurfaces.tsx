import { Suspense, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import GroupSettingsPage from "@/app/groups/[id]/settings/page";
import PullRequestPage from "@/app/pulls/[owner]/[name]/[number]/page";
import type { PromptDigest, VillagePullRequestAttachmentResponse } from "@peasant-labs/schema";

/**
 * Mount support for the real pull request page, with REST stubbed at `fetch`
 * and every outbound request recorded so a test asserts the route a control
 * hit rather than inspecting a mutation object.
 *
 * The pull request page reads everything it needs from its own payload, so it
 * mounts without an auth provider. The collective settings page mounts through
 * `src/test/mountedCollectivePages.tsx`.
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
  return <QueryOnly><AuthProvider>{children}</AuthProvider></QueryOnly>;
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
  detachStatus?: number;
  detachMessage?: string;
  confirmMessage?: string;
  /** Status the attachment read answers; unset (or 200) serves the attachment. */
  attachmentStatus?: number;
  /**
   * What `GET /transcripts/{id}` and `GET /transcripts/{id}/collectives` answer
   * for each transcript the author's page reads. A read with a `status` of 400
   * or more answers that status instead, so a failed read can be staged.
   */
  transcriptReads?: TranscriptReadFixture[];
}

/** One transcript's own reads, as the author's pull request page fetches them. */
export interface TranscriptReadFixture {
  id: string;
  title: string | null;
  visibility: "public" | "private" | "shared";
  collectives: string[];
  status?: number;
}

/** A complete transcript-read answer with only the fields the page reads varied. */
function transcriptReadResponse(read: TranscriptReadFixture) {
  return {
    transcript: {
      id: read.id,
      owner_id: "11111111-1111-1111-1111-111111111111",
      local_id: `local-${read.id}`,
      title: read.title,
      description: null,
      visibility: read.visibility,
      model_provider: "claude-code",
      model_name: null,
      harness_version: null,
      session_start: "2026-01-01T00:00:00Z",
      session_end: null,
      turn_count: 2,
    },
    tags: [],
    shares: [],
    enriched_shares: [],
    owner: {
      id: "11111111-1111-1111-1111-111111111111",
      github_id: 1,
      github_username: "alice-dev",
      display_name: null,
      avatar_url: null,
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      is_discoverable: true,
      username_chosen: true,
      provider_username: "alice-dev",
    },
  };
}

function transcriptCollectivesResponse(read: TranscriptReadFixture) {
  return {
    collectives: read.collectives.map((name, index) => ({
      id: `00000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
      name,
      description: null,
      linked_github_org: null,
      shared_at: "2026-01-01T00:00:00Z",
    })),
  };
}

/**
 * A minimal but complete digest, so every branch of the renderer is exercised.
 * It advertises one transcript per id it is given, because the page decides a
 * row is "not available" by asking whether the digest still carries its id.
 */
export function makeDigest(
  transcriptIds: string[] = ["11111111-1111-1111-1111-111111111111"],
  commits: { covered: number; total: number } = { covered: 1, total: 2 },
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
      commitsCovered: commits.covered,
      commitsTotal: commits.total,
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
      if (fixture.detachStatus && fixture.detachStatus >= 400) return json({ error: fixture.detachMessage ?? "refused" }, fixture.detachStatus);
      attachment = { ...attachment, state: "detached" };
      return json(makeAttachmentResponse({ ...fixture, attachment }));
    }
    const transcriptRead = url.match(/\/transcripts\/([^/?]+)(\/collectives)?$/);
    if (method === "GET" && transcriptRead) {
      const read = fixture.transcriptReads?.find((row) => row.id === decodeURIComponent(transcriptRead[1]));
      if (!read) {
        throw new Error(`pull request fixture has no read for ${url}`);
      }
      if (read.status && read.status >= 400) {
        return json({ error: "refused" }, read.status);
      }
      return json(transcriptRead[2] ? transcriptCollectivesResponse(read) : transcriptReadResponse(read));
    }
    if (method === "GET" && url.includes(`/pulls/`)) {
      if (fixture.attachmentStatus && fixture.attachmentStatus >= 400) {
        return json({ error: "No attachment exists for this pull request" }, fixture.attachmentStatus);
      }
      return json(makeAttachmentResponse({ ...fixture, attachment }));
    }
    throw new Error(`pull request fixture received an unexpected ${method} request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return requests;
}

export async function renderPullRequestRoute(owner: string, name: string, number: number | string): Promise<void> {
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
  // The org the collective already records, as the install handshake leaves it.
  linkedGithubOrg?: string | null;
  // Who may browse the collective's data, as saved. Defaults to members only.
  dataAccess?: "members_only" | "contributors" | "public";
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
  let group = {
    id: fixture.id,
    name: "fixture-collective",
    description: "",
    acceptance_mode: "open",
    data_access: fixture.dataAccess ?? "members_only",
    linked_github_org: fixture.linkedGithubOrg ?? null,
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
      group = { ...group, ...(body as object) };
      return json(group);
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

export function installAttachmentSurfacesTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    globalThis.localStorage?.clear();
    document.documentElement.setAttribute("data-theme", "dark");
  });
}
