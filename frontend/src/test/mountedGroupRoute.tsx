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
import type {
  Group,
  GroupMember,
  GroupTranscript,
  User,
  UserGroupShare,
} from "@/lib/types";
import type { ContributableTranscript } from "@/lib/contribute/types";
import type { PendingShare } from "@/lib/review/types";

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
  /** How many submissions the collective's review queue holds. A row that
   *  sets this also makes the collective CURATED, because only a curated
   *  collective has a queue at all. Omitted means an open collective with no
   *  queue, which is what every navigation row wants. */
  pendingCount?: number;
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
  "owner_reaches_the_review_page_from_the_queue",
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
    const rawPending = rawRow.pendingCount;
    if (rawPending !== undefined && (typeof rawPending !== "number" || !Number.isInteger(rawPending) || rawPending < 0)) {
      throw new Error(
        `rows[${index}] ("${name}").pendingCount must be a non-negative integer when present, got ${JSON.stringify(rawPending)}`,
      );
    }
    return {
      name,
      why,
      role: rawRole as ContributeNavRole,
      ...(rawPending === undefined ? {} : { pendingCount: rawPending as number }),
    };
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

/**
 * One pending submission as `GET /groups/{id}/pending` answers it.
 *
 * This is the WIRE type, not a second copy of it, so a fixture row cannot
 * drift from what the route actually serves. The three identity columns are
 * what the review queue folds on: a queue holds rows from several publishers,
 * and a session id is unique per owner rather than globally, so the owner is
 * part of the match. The project and branch columns are what the review page
 * groups by.
 */
export type PendingShareFixtureRow = PendingShare;

export interface GroupRouteFixture {
  /** Signed-in viewer's GitHub username, or `null` for an anonymous one (`/auth/me` answers 401). */
  viewer: string | null;
  groupId: string;
  groupName: string;
  /** `your_role` the `/groups/{id}` payload reports, or `null` for a non-member. */
  role: "contributor" | "member" | "owner" | null;
  /** How the collective accepts contributions. `curated` is what opens the
   *  owner's review queue; the default keeps every existing case's `open`
   *  collective unchanged. */
  acceptanceMode?: "open" | "verified_only" | "curated";
  /** The collective's contributions, served on `/groups/{id}` and read by both
   *  the browse list and the repository view. */
  transcripts?: GroupTranscript[];
  /** `GET /groups/{id}/pending`, the owner's review queue of a curated
   *  collective. */
  pendingShares?: PendingShareFixtureRow[];
  /** `GET /groups/{id}/my-shares`, this person's own contributions. */
  myShares?: UserGroupShare[];
  /** `GET /groups/{id}/contributable`, what the contribute tree offers. */
  contributable?: ContributableTranscript[];
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
    acceptance_mode: fixture.acceptanceMode ?? "open",
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

/** One request the mounted route made, with its parsed body, so a test asserts
 *  WHAT was sent rather than only that something was. */
export interface RecordedGroupRequest {
  method: string;
  url: string;
  body: unknown;
}

/**
 * Stubs `fetch` for one group-route fixture: `/auth/me`, `/groups/{id}`,
 * `/groups/{id}/my-shares` (fired unconditionally for a signed-in viewer by
 * `useMyGroupShares`), `/groups/{id}/pending` (the owner's review queue of a
 * curated collective), `/groups/{id}/repositories`, `/groups/{id}/contributable`,
 * the `PATCH /groups/{id}/shares/{transcriptId}` a moderator's decision sends,
 * and `/transcripts?owner=...` (fired by the contribute page's `useTranscripts`
 * for a member).
 *
 * Answers with the array it RECORDS every request into, so a test can assert
 * the decision a moderator's click actually sent, and for which submission.
 */
export function installGroupRouteREST(fixture: GroupRouteFixture): RecordedGroupRequest[] {
  const group = makeGroup(fixture);
  const members = [makeMember(fixture)];
  const requests: RecordedGroupRequest[] = [];

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();
    requests.push({
      method,
      url,
      body: typeof init?.body === "string" ? JSON.parse(init.body) : null,
    });
    if (method === "PATCH" && /\/shares\/[^/]+$/.test(url)) {
      return json({ ok: true });
    }

    if (url.endsWith("/auth/me")) {
      if (fixture.viewer == null) return json({ error: "unauthenticated" }, 401);
      return json(makeUser(fixture.viewer));
    }
    if (url.includes("/my-shares")) {
      return json(fixture.myShares ?? []);
    }
    if (url.includes("/pending")) {
      return json(fixture.pendingShares ?? []);
    }
    if (url.includes("/repositories")) {
      // The repository-link feature is optional server-side and answers 501
      // when it is not configured, which the panel renders as its own notice.
      return json({ error: "not configured" }, 501);
    }
    if (url.includes("/transcripts?")) {
      return json({ transcripts: [], total: 0, agent_total: 0, page: 1, limit: 100 });
    }
    // Fired by the tree-based contribute page's `useContributable`
    // (village#66). Empty by default: the panel's own empty state ("all
    // your transcripts are already shared...") covers this fixture's
    // member-view assertion the same way the interim single-panel shell did.
    if (url.includes("/contributable")) {
      return json({ group_id: fixture.groupId, transcripts: fixture.contributable ?? [] });
    }
    // The detail payload, with or without the paging query the "browse all"
    // control adds; both answer from the same list, which is what the real
    // endpoint does.
    if (new RegExp(`/groups/${fixture.groupId}(\\?|$)`).test(url)) {
      const groupTranscripts = fixture.transcripts ?? [];
      return json({
        group,
        members,
        transcripts: groupTranscripts,
        stats: {
          total_transcripts: groupTranscripts.length,
          contributor_count: new Set(groupTranscripts.map((t) => t.owner_id)).size,
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
