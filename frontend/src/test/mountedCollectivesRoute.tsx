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
  /** Refusal EVENTS. A past event, never a standing. */
  rejected: number;
  /** Withdrawal EVENTS. A past event, never a standing. */
  withdrawn: number;
  expect: CollectiveBadgeExpectation;
}

export interface CollectiveBadgeFixtures {
  rows: CollectiveBadgeRow[];
}

/**
 * Required-NAME manifest (never a bare count): the fixture must carry exactly
 * these rows. Four are the combinations of the two independent axes; the fifth
 * is the collective whose every attempt ended in a refusal or a withdrawal,
 * which is the only case that can catch a badge rule widened to count past
 * events. Deleting one, or adding an unlisted one, fails the load rather than
 * silently changing which cases run.
 */
const requiredRowNames = [
  "member_with_contribution",
  "member_without_contribution",
  "visible_non_member_with_pending_contribution",
  "visible_non_member_without_contribution",
  "visible_non_member_with_only_refused_and_withdrawn_attempts",
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
    const rejected = assertCount(rawRow.rejected, `rows[${index}] ("${name}").rejected`);
    const withdrawn = assertCount(rawRow.withdrawn, `rows[${index}] ("${name}").withdrawn`);
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
      rejected,
      withdrawn,
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
  // The row NAMED for a history of only past events must actually carry one.
  // Checking "some row does" instead would let that row be zeroed out while an
  // unrelated row kept the check green, leaving a case whose name and stated
  // reason no longer describe what it exercises.
  const pastEventsOnly = rows.find(
    (r) => r.name === "visible_non_member_with_only_refused_and_withdrawn_attempts",
  );
  if (
    pastEventsOnly &&
    !(pastEventsOnly.approved + pastEventsOnly.pending === 0 && pastEventsOnly.rejected + pastEventsOnly.withdrawn > 0)
  ) {
    throw new Error(
      `case "${pastEventsOnly.name}" is named for a contribution history of only refusals and withdrawals, ` +
        `but carries approved ${pastEventsOnly.approved}, pending ${pastEventsOnly.pending}, rejected ` +
        `${pastEventsOnly.rejected}, withdrawn ${pastEventsOnly.withdrawn}. It is the only case that can catch a ` +
        "badge rule widened to count past events, so it must have some and no live contribution.",
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
    // Past events, never a standing.
    rejected_attempt_count: row.rejected,
    withdrawn_attempt_count: row.withdrawn,
  };
}

/**
 * Whether the real contributions endpoint would carry a row for this
 * collective.
 *
 * It lists the collectives the caller has OFFERED transcripts to, so a
 * collective whose every attempt ended in a refusal or a withdrawal IS listed,
 * with approved and pending both zero. Filtering on a live contribution instead
 * would make that row unreachable, and a badge rule widened to count past
 * events would then pass unnoticed.
 */
function everOffered(row: CollectiveBadgeRow): boolean {
  return row.approved + row.pending + row.rejected + row.withdrawn > 0;
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
 * The contributions body carries a row for every collective this caller ever
 * OFFERED something to, which is what the real endpoint does: a collective
 * whose attempts all ended in a refusal or a withdrawal is listed with approved
 * and pending both zero. A collective the caller never approached is absent
 * entirely, not present with zeroes, so a page that keyed off mere presence
 * fails here.
 */
export function installCollectivesRouteREST(rows: readonly CollectiveBadgeRow[]): void {
  const groups = rows.map(makeVisibleGroup);
  const contributions = rows.filter(everOffered).map(makeContribution);

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

/**
 * The selectors the shipped stylesheet uses to collapse the card's empty
 * standing slot and the separator that follows it.
 *
 * They are READ FROM `globals.css` rather than restated here, so the test
 * cannot drift from the rule that actually ships.
 *
 * Be precise about what this can prove. The test environment loads no
 * stylesheet, so nothing here evaluates a declaration. It proves the rule still
 * has a TARGET: the selectors still match an element on a card with no
 * standing, so a design system that renames or reorders the footer fails here
 * instead of leaving the override silently inert.
 *
 * It does NOT prove the rule still HIDES that element. Changing `display: none`
 * to something else, or losing to a more specific rule elsewhere, brings the
 * stray separator back with this still green. That residual risk needs a
 * browser, and it is covered by the both-theme capture and the computed-style
 * probe run against the real build before release, not here.
 */
export function emptyStandingSelectors(): string[] {
  const cssPath = resolve(process.cwd(), "src/app/globals.css");
  const css = readFileSync(cssPath, "utf8");
  const selectors = [...css.matchAll(/^(\.cmg-col-foot [^{\n]*:empty[^{\n]*)\{/gm)].map((m) =>
    m[1].trim(),
  );
  if (selectors.length === 0) {
    throw new Error(
      "globals.css no longer collapses the collectives card's empty standing slot. If the design system " +
        "now draws its footer separators between items, delete this check with the rule; otherwise a row " +
        "with no standing has regained its stray leading separator.",
    );
  }
  return selectors;
}

/** Every rendered card carrying this row's name. */
function cardsNamed(name: string): Element[] {
  return [...document.querySelectorAll(".cmg-col-card")].filter(
    (card) => card.querySelector(".cmg-col-name")?.textContent === name,
  );
}

/**
 * The card the design system rendered for one fixture row.
 *
 * ONE way to reach a card, shared by the render helper's wait condition and by
 * the assertions, so a change to the card markup breaks in one place instead of
 * being fixed in one file and silently missed in the other. Two matches is a
 * failure, not a coin toss: the page shows one list, and a duplicated row would
 * otherwise be read as the row the test meant.
 */
export function collectiveCard(row: CollectiveBadgeRow): HTMLElement {
  const name = collectiveNameFor(row);
  const found = cardsNamed(name);
  if (found.length === 0) {
    throw new Error(`the collectives page shows no card named "${name}"`);
  }
  if (found.length > 1) {
    throw new Error(`the collectives page shows ${found.length} cards named "${name}"; it must show one`);
  }
  return found[0] as HTMLElement;
}

/**
 * What one card claims about the caller.
 *
 * Read from the standing slot rather than the whole card, because the card's
 * member COUNT ("4 members") would otherwise be mistaken for a member badge.
 */
export function standingTextFor(row: CollectiveBadgeRow): string {
  return (collectiveCard(row).querySelector(".cmg-col-role")?.textContent ?? "").trim();
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
    const standing =
      (cardsNamed(collectiveNameFor(contributing))[0]?.querySelector(".cmg-col-role")?.textContent ?? "").trim();
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
