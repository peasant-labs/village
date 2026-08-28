import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { Suspense, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { parse } from "yaml";
import { AuthProvider } from "@/providers/AuthProvider";
import GroupDetailPage from "@/app/groups/[id]/page";
import GroupContributePage from "@/app/groups/[id]/contribute/page";
import type { Group, GroupMember, GroupTranscript, User } from "@/lib/types";

/**
 * Mount support for the REAL `/groups/{id}` and `/groups/{id}/contribute`
 * routes, with REST stubbed at `fetch`, mirroring
 * `src/test/mountedProjectRoute.tsx`. Both routes mount inside the
 * `AuthProvider` the membership-gated header action and the contribute
 * page's own gate both read from, so a test asserts what a signed-in
 * member or a non-member visitor actually sees.
 */

// ── Fixture rows ─────────────────────────────────────────────────────────

/** A row's viewer role in the collective, or `null` for a non-member/signed-out
 *  viewer. Owner and member are distinct rows because the header contribute
 *  action reaches them by different paths. */
export type ContributeNavRole = "owner" | "member" | null;

export interface ContributeNavRow {
  name: string;
  why: string;
  role: ContributeNavRole;
}

export interface ContributeNavFixtures {
  rows: ContributeNavRow[];
}

/**
 * Required-NAME manifest (never a bare count): the fixture file must carry
 * exactly these rows, no more, no fewer. Deleting a row, or adding an
 * unlisted one, fails the load rather than silently changing which cases
 * run.
 */
const requiredRowNames = [
  "member_navigates",
  "owner_navigates_via_village_action",
  "owner_no_double_button_even_if_manage_renders_its_own",
  "non_member_no_button",
  "contribute_page_member_panel",
  "contribute_page_non_member_notice",
] as const;

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === "object" && !Array.isArray(value);
}

function assertString(value: unknown, location: string): string {
  if (typeof value !== "string") {
    throw new Error(`${location} must be a string, got ${JSON.stringify(value)}`);
  }
  return value;
}

/** Loads and validates `src/testdata/groups-contribute-nav.yaml` against the required-name manifest. */
export function loadGroupsContributeNavFixtures(): ContributeNavFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/groups-contribute-nav.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (!isPlainObject(parsed)) {
    throw new Error("groups-contribute-nav fixture root must be an object");
  }
  const rawRows = parsed.rows;
  if (!Array.isArray(rawRows) || rawRows.length === 0) {
    throw new Error("groups-contribute-nav fixture must have a non-empty rows array");
  }

  const rows: ContributeNavRow[] = rawRows.map((rawRow, index) => {
    if (!isPlainObject(rawRow)) throw new Error(`rows[${index}] must be an object`);
    const name = assertString(rawRow.name, `rows[${index}].name`);
    const why = assertString(rawRow.why, `rows[${index}].why`);
    const rawRole = rawRow.role;
    if (rawRole !== null && rawRole !== "member" && rawRole !== "owner") {
      throw new Error(
        `rows[${index}] ("${name}").role must be "owner", "member" or null, got ${JSON.stringify(rawRole)}`
      );
    }
    return { name, why, role: rawRole as ContributeNavRole };
  });

  const names = rows.map((r) => r.name);
  if (new Set(names).size !== names.length) {
    throw new Error(`groups-contribute-nav fixture row names must be unique: ${names.join(", ")}`);
  }
  const missing = requiredRowNames.filter((n) => !names.includes(n));
  if (missing.length > 0) {
    throw new Error(`groups-contribute-nav fixture is missing required row(s): ${missing.join(", ")}`);
  }
  const unexpected = names.filter((n) => !(requiredRowNames as readonly string[]).includes(n));
  if (unexpected.length > 0) {
    throw new Error(
      `groups-contribute-nav fixture has row(s) not in the required-name manifest: ${unexpected.join(", ")}`
    );
  }

  return { rows };
}

// ── Route fixture ────────────────────────────────────────────────────────

export interface GroupRouteFixture {
  /** Signed-in viewer's GitHub username, or `null` for an anonymous one (`/auth/me` answers 401). */
  viewer: string | null;
  groupId: string;
  groupName: string;
  /** `your_role` the `/groups/{id}` payload reports, or `null` for a non-member. */
  role: "contributor" | "member" | "owner" | null;
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

function makeGroup(fixture: GroupRouteFixture): Group {
  return {
    id: fixture.groupId,
    name: fixture.groupName,
    description: null,
    linked_github_org: null,
    display_members: true,
    transcript_deletion_policy: "user_choice",
    created_by: "user-owner",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    acceptance_mode: "open",
    data_access: "members_only",
    role: fixture.role ?? "",
    member_since: fixture.role ? "2026-01-02T00:00:00Z" : null,
  };
}

function makeMember(fixture: GroupRouteFixture): GroupMember {
  const username = fixture.viewer ?? "owner";
  return {
    role: fixture.role ?? "owner",
    joined_at: "2026-01-02T00:00:00Z",
    id: `user-${username}`,
    github_username: username,
    display_name: null,
    avatar_url: null,
  };
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });

/**
 * Stubs `fetch` for one group-route fixture: `/auth/me`, `/groups/{id}`,
 * `/groups/{id}/my-shares` (fired unconditionally for a signed-in viewer by
 * `useMyGroupShares`), and `/transcripts?owner=...` (fired by the
 * contribute page's `useTranscripts` for a member).
 */
export function installGroupRouteREST(fixture: GroupRouteFixture): void {
  const group = makeGroup(fixture);
  const members = [makeMember(fixture)];

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();

    if (url.endsWith("/auth/me")) {
      if (fixture.viewer == null) return json({ error: "unauthenticated" }, 401);
      return json(makeUser(fixture.viewer));
    }
    if (url.includes("/my-shares")) {
      return json([]);
    }
    if (url.includes("/transcripts?")) {
      return json({ transcripts: [], total: 0, agent_total: 0, page: 1, limit: 100 });
    }
    // Fired by the tree-based contribute page's `useContributable`
    // (village#66). Empty by default: the panel's own empty state ("all
    // your transcripts are already shared...") covers this fixture's
    // member-view assertion the same way the interim single-panel shell did.
    if (url.includes("/contributable")) {
      return json({ group_id: fixture.groupId, transcripts: [] });
    }
    if (new RegExp(`/groups/${fixture.groupId}$`).test(url)) {
      return json({
        group,
        members,
        transcripts: [] as GroupTranscript[],
        stats: {
          total_transcripts: 0,
          contributor_count: 0,
          total_turns: 0,
          total_duration_ms: 0,
          total_tokens: 0,
        },
        models: [],
        contributors: [],
        can_read: fixture.role != null,
        your_role: fixture.role ?? "",
        pending_members: [],
      });
    }
    throw new Error(`group-route fixture received an unexpected ${method} request to ${url}`);
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

/** Renders the real `/groups/{id}` route. */
export async function renderGroupDetailRoute(id: string): Promise<void> {
  await act(async () => {
    render(
      <Providers>
        <GroupDetailPage params={Promise.resolve({ id })} />
      </Providers>,
    );
  });
}

/** Renders the real `/groups/{id}/contribute` route. */
export async function renderGroupContributeRoute(id: string): Promise<void> {
  await act(async () => {
    render(
      <Providers>
        <GroupContributePage params={Promise.resolve({ id })} />
      </Providers>,
    );
  });
}

/** Shared teardown: unmount, drop the `fetch` stub, reset the document theme. */
export function installGroupRouteTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    localStorage.clear();
    document.documentElement.setAttribute("data-theme", "dark");
  });
}
