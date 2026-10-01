import { Suspense, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import type { VillageCollectiveSearchResult } from "@peasant-labs/schema";
import { AuthProvider } from "@/providers/AuthProvider";
import GroupsPage from "@/app/groups/page";
import GroupDetailPage from "@/app/groups/[id]/page";
import GroupSettingsPage from "@/app/groups/[id]/settings/page";
import { resetNextNavigation } from "@/test/nextNavigationMock";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import type { Group, GroupMember, GroupTranscript, User, VisibleGroup } from "@/lib/types";

/**
 * Mount support for the collective pages: the collectives list, a collective,
 * and its settings, each through its REAL route with REST stubbed at `fetch`.
 * The stub is a small stateful world: a link, an unlink, a settings write or a
 * member change lands in it, so a later read answers what the server would.
 * Every request is recorded, so a test asserts the route and body a control
 * sent rather than the props the page computed.
 */

export interface WorldMember {
  id: string;
  username: string;
  name: string | null;
  role: GroupMember["role"];
}

export interface WorldTranscript {
  id: string;
  title: string;
  author: string;
  /** `owner/name#number`, most recent first. */
  pullRequests: string[];
  pullRequestCount: number;
}

export interface WorldRepo {
  repo: string;
  private: boolean;
  publishers: number;
}

/** A refused request: which write it is, and what the server answers. */
export interface WorldFailure {
  /** Match on the method and the path after `/api/v1`, with ids filled in. */
  method?: string;
  path?: string;
  /** Match the Nth write (1-based) the page sends, whatever its route. */
  write?: number;
  status: number;
  error: string;
}

export interface CollectiveWorld {
  viewer: { id: string; username: string };
  group: Group;
  role: string;
  canRead: boolean;
  members: WorldMember[];
  transcripts: WorldTranscript[];
  totalTranscripts?: number;
  linked: string[];
  available: WorldRepo[];
  failures?: WorldFailure[];
  visible?: VisibleGroup[];
  searchable?: VillageCollectiveSearchResult[];
}

export interface RecordedRequest {
  method: string;
  /** The path after `/api/v1`, query included. */
  path: string;
  body: unknown;
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });

export function makeViewer(id: string, username: string): User {
  return {
    id,
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

/** A complete collective row with the fields a case does not state filled in. */
export function makeGroup(overrides: Partial<Group> & Pick<Group, "id" | "name">): Group {
  return {
    description: null,
    acceptance_mode: "open",
    data_access: "members_only",
    linked_github_org: null,
    display_members: true,
    transcript_deletion_policy: "user_choice",
    post_prompts_check: true,
    prompts_check_mode: "informational",
    created_by: "user-owner",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    role: "",
    member_since: null,
    ...overrides,
  } as Group;
}

function groupMember(member: WorldMember): GroupMember {
  return {
    id: member.id,
    github_username: member.username,
    display_name: member.name,
    avatar_url: null,
    github_orgs: [],
    joined_at: "2026-01-02T00:00:00Z",
    role: member.role,
  };
}

function groupTranscript(t: WorldTranscript, world: CollectiveWorld): GroupTranscript {
  const author = world.members.find((m) => m.username === t.author);
  const recent = t.pullRequests.slice(0, 3).map((label) => {
    const match = /^([^/]+)\/([^#]+)#(\d+)$/.exec(label);
    if (!match) throw new Error(`pull request "${label}" is not owner/name#number`);
    return { owner: match[1], name: match[2], number: Number(match[3]) };
  });
  return {
    ...makeTranscriptFixture({ id: t.id, title: t.title, owner_id: author?.id ?? `user-${t.author}` }),
    owner_username: t.author,
    owner_avatar_url: null,
    owner_is_discoverable: true,
    pull_requests: { count: t.pullRequestCount, recent },
  } as GroupTranscript;
}

function linkedRow(world: CollectiveWorld, repo: string) {
  const [owner, name] = repo.split("/");
  return {
    id: `linked-${repo}`,
    group_id: world.group.id,
    owner,
    name,
    is_private: world.available.find((r) => r.repo === repo)?.private ?? false,
    installation_id: 4242,
    linked_by: world.viewer.id,
    created_at: "2026-01-03T00:00:00Z",
    last_synced_at: null,
  };
}

/**
 * Stubs `fetch` with the world and returns every request the page made, in
 * order. A request the world has no route for fails the test.
 */
export function installCollectiveREST(world: CollectiveWorld): RecordedRequest[] {
  const requests: RecordedRequest[] = [];
  let writes = 0;
  const id = world.group.id;

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "https://village.test");
    const path = url.pathname.replace(/^\/api\/v1/, "");
    const method = (init?.method ?? "GET").toUpperCase();
    let body: unknown = null;
    if (typeof init?.body === "string") body = JSON.parse(init.body);
    requests.push({ method, path: `${path}${url.search}`, body });

    if (method !== "GET") writes += 1;
    const failure = (world.failures ?? []).find(
      (f) =>
        (f.write === undefined || (method !== "GET" && f.write === writes)) &&
        (f.method === undefined || f.method === method) &&
        (f.path === undefined || f.path === path),
    );
    if (failure) return json({ error: failure.error }, failure.status);

    if (path === "/auth/me") return json(makeViewer(world.viewer.id, world.viewer.username));
    if (path === "/auth/orgs") return json([]);
    if (path === "/groups" && method === "POST") {
      return json({ ...world.group, ...(body as object) });
    }
    if (path === "/groups/visible") return json(world.visible ?? []);
    if (path === "/users/me/collectives/contributions") return json({ collectives: [] });
    if (path === "/groups/search") {
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      const hits = (world.searchable ?? []).filter(
        (c) => c.name.toLowerCase().includes(q) || (c.linked_github_org ?? "").toLowerCase().includes(q),
      );
      return json({ collectives: hits });
    }

    const base = `/groups/${id}`;
    if (path === base && method === "GET") {
      const limit = Number(url.searchParams.get("limit") ?? 20);
      const offset = Number(url.searchParams.get("offset") ?? 0);
      const transcripts = world.canRead
        ? world.transcripts.slice(offset, offset + limit).map((t) => groupTranscript(t, world))
        : [];
      return json({
        group: world.group,
        members: world.members.map(groupMember),
        transcripts,
        stats: {
          total_transcripts: world.totalTranscripts ?? world.transcripts.length,
          contributor_count: 3,
          total_turns: 400,
          total_duration_ms: 3_600_000,
          total_tokens: 1_200_000,
          pull_request_count: 7,
        },
        models: [],
        contributors: [],
        can_read: world.canRead,
        your_role: world.role,
      });
    }
    if (path === base && method === "PATCH") {
      world.group = { ...world.group, ...(body as Partial<Group>) };
      return json(world.group);
    }
    if (path === `${base}/my-shares`) return json([]);
    if (path === `${base}/join` && method === "POST") return json({ status: "joined", role: "contributor" });
    if (path === `${base}/repositories` && method === "GET") {
      return json({ repositories: world.linked.map((repo) => linkedRow(world, repo)) });
    }
    if (path === `${base}/repositories/available` && method === "GET") {
      if (world.role !== "owner") return json({ error: "Owner access required" }, 403);
      return json({
        repositories: world.available.map((r) => {
          const [owner, name] = r.repo.split("/");
          return { owner, name, is_private: r.private, publisher_count: r.publishers };
        }),
      });
    }
    if (path === `${base}/repositories` && method === "POST") {
      const { owner, name } = body as { owner: string; name: string };
      const repo = `${owner}/${name}`;
      if (!world.linked.includes(repo)) world.linked = [...world.linked, repo];
      return json(linkedRow(world, repo), 201);
    }
    const unlink = path.match(new RegExp(`^${base}/repositories/([^/]+)/([^/]+)$`));
    if (unlink && method === "DELETE") {
      const repo = `${decodeURIComponent(unlink[1])}/${decodeURIComponent(unlink[2])}`;
      world.linked = world.linked.filter((r) => r !== repo);
      return json({ status: "unlinked" });
    }
    const role = path.match(new RegExp(`^${base}/members/([^/]+)/role$`));
    if (role && method === "PATCH") {
      const next = (body as { role: GroupMember["role"] }).role;
      world.members = world.members.map((m) => (m.id === role[1] ? { ...m, role: next } : m));
      return json({ status: "updated", role: next });
    }
    const member = path.match(new RegExp(`^${base}/members/([^/]+)$`));
    if (member && method === "DELETE") {
      world.members = world.members.filter((m) => m.id !== member[1]);
      return json({ status: "removed" });
    }
    if (path === `${base}/members` && method === "POST") return json({ status: "added" });
    throw new Error(`the collective world has no route for ${method} ${path}${url.search}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return requests;
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

/** Renders the real `/groups` route and waits for its heading. */
export async function renderCollectivesList(): Promise<void> {
  await act(async () => {
    render(
      <Providers>
        <GroupsPage />
      </Providers>,
    );
  });
  await screen.findByRole("heading", { name: "collectives" });
}

/**
 * Renders the real `/groups/{id}` route and waits until the collective and the
 * viewer have both arrived: the policy boxes are drawn from the one and the
 * header actions from the other.
 */
export async function renderCollectivePage(world: CollectiveWorld, expectReadFailure = false): Promise<void> {
  await act(async () => {
    render(
      <Providers>
        <GroupDetailPage params={Promise.resolve({ id: world.group.id })} />
      </Providers>,
    );
  });
  if (expectReadFailure) {
    await screen.findByRole("alert");
    return;
  }
  await screen.findByRole("heading", { name: world.group.name });
  await waitFor(() => {
    if (!document.querySelector(".cmg-policy")) throw new Error("the policy boxes have not rendered");
  });
}

/** Renders the real `/groups/{id}/settings` route and waits for its sections. */
export async function renderCollectiveSettings(world: CollectiveWorld, expectReadFailure = false): Promise<void> {
  await act(async () => {
    render(
      <Providers>
        <GroupSettingsPage params={Promise.resolve({ id: world.group.id })} />
      </Providers>,
    );
  });
  if (expectReadFailure) {
    await screen.findByRole("alert");
    return;
  }
  await screen.findByRole("navigation", { name: "settings sections" });
}

/** Shared teardown: unmount, drop the `fetch` stub, reset the URL and the recorded navigation. */
export function installCollectivePagesTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    globalThis.localStorage?.clear();
    window.history.replaceState(null, "", "/");
    resetNextNavigation();
    document.documentElement.setAttribute("data-theme", "dark");
  });
}

/** An element's visible text with its whitespace collapsed. */
export function textOf(element: Element | null | undefined): string {
  return (element?.textContent ?? "").replace(/\s+/g, " ").trim();
}
