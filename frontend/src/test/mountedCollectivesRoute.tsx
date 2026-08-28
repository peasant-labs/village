import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { Suspense, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { parse } from "yaml";
import { AuthProvider } from "@/providers/AuthProvider";
import GroupsPage from "@/app/groups/page";
import type { ContributedCollective, User, VisibleGroup } from "@/lib/types";

/**
 * Mount support for the REAL `/groups` collectives route, with REST stubbed at
 * `fetch`, mirroring `src/test/mountedGroupRoute.tsx`.
 *
 * The route mounts inside the `AuthProvider` the page's signed-in gate reads,
 * and the design system's real `CollectivesView` renders the cards, so a test
 * asserts what a signed-in person actually sees on the page rather than the
 * props the page computed.
 */

// ── Fixture rows ─────────────────────────────────────────────────────────

export interface CollectiveBadgeExpectation {
  /** The role the row must show, or null when the row must show no member badge. */
  member_badge: string | null;
  /** Whether the row must say "contributed". */
  contributed_badge: boolean;
}

export interface CollectiveBadgeRow {
  name: string;
  why: string;
  /** `role` as GET /groups/visible reports it; null for a collective the caller only sees. */
  role: string | null;
  /** This caller's approved contributions to this collective. */
  approved: number;
  /** This caller's contributions to this collective still awaiting review. */
  pending: number;
  expect: CollectiveBadgeExpectation;
}

export interface CollectiveBadgeFixtures {
  rows: CollectiveBadgeRow[];
}

/**
 * Required-NAME manifest (never a bare count): the fixture must carry exactly
 * these four rows, which are the four combinations of the two independent
 * axes. Deleting one, or adding an unlisted one, fails the load rather than
 * silently changing which cases run.
 */
const requiredRowNames = [
  "member_with_contribution",
  "member_without_contribution",
  "visible_non_member_with_pending_contribution",
  "visible_non_member_without_contribution",
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

function assertCount(value: unknown, location: string): number {
  if (typeof value !== "number" || !Number.isInteger(value) || value < 0) {
    throw new Error(`${location} must be a non-negative integer, got ${JSON.stringify(value)}`);
  }
  return value;
}

function assertNullableString(value: unknown, location: string): string | null {
  if (value === null) return null;
  return assertString(value, location);
}

/** Loads and validates `src/testdata/collectives-visible-badges.yaml` against the required-name manifest. */
export function loadCollectiveBadgeFixtures(): CollectiveBadgeFixtures {
  const fixturePath = resolve(process.cwd(), "src/testdata/collectives-visible-badges.yaml");
  const parsed: unknown = parse(readFileSync(fixturePath, "utf8"), { strict: true });
  if (!isPlainObject(parsed)) {
    throw new Error("collectives-visible-badges fixture root must be an object");
  }
  const rawRows = parsed.rows;
  if (!Array.isArray(rawRows) || rawRows.length === 0) {
    throw new Error("collectives-visible-badges fixture must have a non-empty rows array");
  }

  const rows: CollectiveBadgeRow[] = rawRows.map((rawRow, index) => {
    if (!isPlainObject(rawRow)) throw new Error(`rows[${index}] must be an object`);
    const name = assertString(rawRow.name, `rows[${index}].name`);
    const why = assertString(rawRow.why, `rows[${index}].why`);
    const role = assertNullableString(rawRow.role, `rows[${index}] ("${name}").role`);
    const approved = assertCount(rawRow.approved, `rows[${index}] ("${name}").approved`);
    const pending = assertCount(rawRow.pending, `rows[${index}] ("${name}").pending`);
    const rawExpect = rawRow.expect;
    if (!isPlainObject(rawExpect)) {
      throw new Error(`rows[${index}] ("${name}").expect must be an object`);
    }
    const memberBadge = assertNullableString(
      rawExpect.member_badge,
      `rows[${index}] ("${name}").expect.member_badge`,
    );
    if (typeof rawExpect.contributed_badge !== "boolean") {
      throw new Error(`rows[${index}] ("${name}").expect.contributed_badge must be a boolean`);
    }
    // The expectation must follow from the row's own facts, so a case cannot
    // declare an outcome its inputs could never produce and then pass.
    if (memberBadge !== role) {
      throw new Error(
        `rows[${index}] ("${name}") reports role ${JSON.stringify(role)} but expects member badge ` +
          `${JSON.stringify(memberBadge)}; the member badge IS the role the row carries`,
      );
    }
    if (rawExpect.contributed_badge !== approved + pending > 0) {
      throw new Error(
        `rows[${index}] ("${name}") has ${approved} approved and ${pending} pending contributions but expects ` +
          `contributed_badge ${rawExpect.contributed_badge}; a live contribution is approved or pending`,
      );
    }
    return {
      name,
      why,
      role,
      approved,
      pending,
      expect: { member_badge: memberBadge, contributed_badge: rawExpect.contributed_badge },
    };
  });

  const names = rows.map((r) => r.name);
  if (new Set(names).size !== names.length) {
    throw new Error(`collectives-visible-badges row names must be unique: ${names.join(", ")}`);
  }
  const missing = requiredRowNames.filter((n) => !names.includes(n));
  if (missing.length > 0) {
    throw new Error(
      `collectives-visible-badges fixture is missing required row(s): ${missing.join(", ")}. ` +
        "Each is one of the four combinations of membership and contribution; restore it rather than " +
        "deleting it from this manifest.",
    );
  }
  const unexpected = names.filter((n) => !(requiredRowNames as readonly string[]).includes(n));
  if (unexpected.length > 0) {
    throw new Error(
      `collectives-visible-badges fixture has row(s) not in the required-name manifest: ${unexpected.join(", ")}`,
    );
  }

  return { rows };
}

// ── Route fixture ────────────────────────────────────────────────────────

/** The id GET /groups/visible reports for one fixture row. */
export function collectiveIdFor(row: CollectiveBadgeRow): string {
  return `collective-${row.name}`;
}

/** The name GET /groups/visible reports for one fixture row. */
export function collectiveNameFor(row: CollectiveBadgeRow): string {
  return `collective ${row.name}`;
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

function makeVisibleGroup(row: CollectiveBadgeRow): VisibleGroup {
  return {
    id: collectiveIdFor(row),
    name: collectiveNameFor(row),
    description: `what ${row.name} is for`,
    linked_github_org: null,
    display_members: true,
    transcript_deletion_policy: "user_choice",
    created_by: "user-owner",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    acceptance_mode: "open",
    data_access: "public",
    // The two nullable membership columns move together: a collective the
    // caller only sees has neither a role nor a join date.
    role: row.role,
    member_since: row.role ? "2026-01-02T00:00:00Z" : null,
    member_count: 4,
    transcript_count: 7,
  };
}

function makeContribution(row: CollectiveBadgeRow): ContributedCollective {
  return {
    id: collectiveIdFor(row),
    name: collectiveNameFor(row),
    description: null,
    linked_github_org: null,
    approved_count: row.approved,
    pending_count: row.pending,
    // Past events, never a standing: a row that carried only these must show
    // no contributed badge, which is why they are non-zero here.
    rejected_attempt_count: 3,
    withdrawn_attempt_count: 2,
  };
}

/**
 * The rows the current `fetch` stub is serving. The render helper needs them to
 * know which row proves the second request has landed.
 */
let servedRows: readonly CollectiveBadgeRow[] = [];

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });

/**
 * Stubs `fetch` for the collectives route: `/auth/me`, `/auth/orgs`,
 * `/groups/visible`, and `/users/me/collectives/contributions`.
 *
 * Every fixture row is served in ONE `/groups/visible` body, because the page
 * shows one list and the point of the change is that a person sees collectives
 * they do not belong to beside ones they do.
 *
 * The contributions body carries a row ONLY for a collective this caller
 * actually contributed to, which is what the real endpoint does. A collective
 * with no live contribution is absent from it, not present with zeroes, so a
 * page that keyed off mere presence would fail here.
 */
export function installCollectivesRouteREST(rows: readonly CollectiveBadgeRow[]): void {
  const groups = rows.map(makeVisibleGroup);
  const contributions = rows.filter((r) => r.approved + r.pending > 0).map(makeContribution);

  servedRows = rows;

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();

    if (url.endsWith("/auth/me")) return json(makeUser("collectives-viewer"));
    if (url.endsWith("/auth/orgs")) return json([]);
    if (url.endsWith("/groups/visible")) return json(groups);
    if (url.endsWith("/users/me/collectives/contributions")) return json({ collectives: contributions });
    // A request to the membership-only list is a real failure here: the page
    // must ask the visible-collectives route, and answering both would let a
    // regression to the old hook pass unnoticed.
    throw new Error(`the collectives route made an unexpected ${method} request to ${url}`);
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

/** The standing slot of the card whose name is `name`, or null if it is absent. */
function standingSlotFor(name: string): Element | null {
  for (const card of document.querySelectorAll(".cmg-col-card")) {
    if (card.querySelector(".cmg-col-name")?.textContent === name) {
      return card.querySelector(".cmg-col-role");
    }
  }
  return null;
}

/**
 * Renders the real `/groups` route and waits until BOTH of its requests have
 * landed and been rendered.
 *
 * The viewer's identity and the contribution counters arrive over the same
 * stubbed `fetch` as everything else, and the cards render as soon as the
 * collectives answer, before the counters do. A test that asserted then would
 * find every "shows no contributed badge" expectation true merely because the
 * counters had not arrived, and would pass against a page that never reads them
 * at all.
 *
 * So the wait is on a POSITIVE signal that can only be true after the counters
 * are rendered: the one row the corpus guarantees must say "contributed". A
 * page that never asks for the counters, or ignores them, times out here rather
 * than passing.
 */
export async function renderCollectivesRoute(): Promise<void> {
  const contributing = servedRows.find((row) => row.expect.contributed_badge);
  if (!contributing) {
    throw new Error(
      "the served corpus has no row expecting a contributed badge, so there is no signal that the " +
        "contribution counters were ever rendered; restore that case rather than waiting on nothing",
    );
  }
  await act(async () => {
    render(
      <Providers>
        <GroupsPage />
      </Providers>,
    );
  });
  await waitFor(() => {
    if (document.querySelector(".cmg-col-card") == null) {
      throw new Error("the collectives route rendered no collective cards");
    }
    const standing = standingSlotFor(collectiveNameFor(contributing))?.textContent ?? "";
    if (!standing.includes("contributed")) {
      throw new Error(
        `the contribution counters have not reached the page: "${collectiveNameFor(contributing)}" ` +
          `has ${contributing.approved} approved and ${contributing.pending} pending contributions but its ` +
          `standing reads "${standing}"`,
      );
    }
  });
}

/** Shared teardown: unmount, drop the `fetch` stub, reset the document theme. */
export function installCollectivesRouteTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    localStorage.clear();
    document.documentElement.setAttribute("data-theme", "dark");
  });
}
