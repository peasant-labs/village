import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { Suspense, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, within } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { parse } from "yaml";
import type { VillageCollectiveSearchResult } from "@peasant-labs/schema";
import { AuthProvider } from "@/providers/AuthProvider";
import CollectivesPage from "@/app/collectives/page";
import DiscoverPage from "@/app/discover/page";
import { resetNextNavigation } from "@/test/nextNavigationMock";
import type { ContributedCollective, User, UserOrg, VisibleGroup } from "@/lib/types";
import { profileOwner } from "@/test/publicOwnerFixture";

/**
 * Mount support for the collectives list, the discover page and the
 * new-collective dialog, each through its REAL route with REST stubbed at
 * `fetch`. The stub is a small world read from a YAML fixture: the visible
 * collectives, the caller's contributions, what the search answers, the
 * caller's orgs, and the refusals the server would send. Every request is
 * recorded, so a test asserts the route and body a control sent.
 */

// ── the world ────────────────────────────────────────────────────────────

/** One collective as the fixture states it; the rest of the row is filled in. */
export interface WorldCollective {
  id: string;
  name: string;
  /** The caller's role, or null for a collective they only see. */
  role: VisibleGroup["role"];
  mode: VisibleGroup["acceptance_mode"];
  org: string | null;
  description?: string | null;
  members?: number;
  transcripts?: number;
}

/** A refused request: the method and path after `/api/v1`, and the answer. */
export interface WorldRefusal {
  method: string;
  path: string;
  status: number;
  error: string;
  /** Refuse only a POST whose JSON body carries this username. */
  username?: string;
}

export interface SurfacesWorld {
  signedIn: boolean;
  viewer: { id: string; username: string; discoverable: boolean };
  visible: WorldCollective[];
  /** The ids of the collectives the caller has a live (approved) contribution to. */
  contributed: string[];
  /** Search hits: ids from `visible`, or rows of their own. */
  searchable: Array<string | WorldCollective>;
  orgs: Array<{ login: string; visible: boolean }>;
  refusals: WorldRefusal[];
  /** The id the server gives a collective it creates. */
  createdId: string;
}

export interface RecordedRequest {
  method: string;
  /** The path after `/api/v1`, query included. */
  path: string;
  body: unknown;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value != null && typeof value === "object" && !Array.isArray(value);
}

/** Reads one YAML fixture under `src/testdata`. */
export function readFixture(file: string): Record<string, unknown> {
  const parsed: unknown = parse(readFileSync(resolve(process.cwd(), "src/testdata", file), "utf8"), { strict: true });
  if (!isPlainObject(parsed)) throw new Error(`${file} must hold an object`);
  return parsed;
}

/**
 * Fails when the fixture's cases are not exactly the named ones, so deleting
 * or renaming a case is a failure rather than a silently smaller run.
 */
export function assertCaseNames(file: string, names: readonly string[], required: readonly string[]): void {
  const missing = required.filter((name) => !names.includes(name));
  const extra = names.filter((name) => !required.includes(name));
  if (new Set(names).size !== names.length) throw new Error(`${file}: case names must be unique`);
  if (missing.length || extra.length) {
    throw new Error(`${file}: missing case(s) ${missing.join(", ") || "none"}; unlisted case(s) ${extra.join(", ") || "none"}`);
  }
}

/** A world with the fixture's base filled in, and a case's overrides on top. */
export function worldFrom(base: Partial<SurfacesWorld>, overrides: Partial<SurfacesWorld> = {}): SurfacesWorld {
  return {
    signedIn: true,
    viewer: { id: "user-viewer", username: "alice-dev", discoverable: true },
    visible: [],
    contributed: [],
    searchable: [],
    orgs: [],
    refusals: [],
    createdId: "c0000000-0000-4000-8000-0000000000aa",
    ...base,
    ...overrides,
  };
}

function viewerUser(world: SurfacesWorld): User {
  return {
    id: world.viewer.id,
    github_id: 1,
    github_username: world.viewer.username,
    display_name: world.viewer.username,
    avatar_url: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    is_discoverable: world.viewer.discoverable,
    username_chosen: true,
    provider_username: world.viewer.username,
  };
}

function visibleRow(row: WorldCollective): VisibleGroup {
  return {
    id: row.id,
    name: row.name,
    description: row.description ?? null,
    acceptance_mode: row.mode,
    data_access: "members_only",
    linked_github_org: row.org,
    display_members: true,
    transcript_deletion_policy: "user_choice",
    creator: profileOwner({ id: "user-owner", github_username: "collective-owner" }),
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    role: row.role,
    member_since: row.role ? "2026-01-02T00:00:00Z" : null,
    member_count: row.members ?? 4,
    transcript_count: row.transcripts ?? 7,
  };
}

function searchRow(row: WorldCollective): VillageCollectiveSearchResult {
  return {
    id: row.id,
    name: row.name,
    description: row.description ?? null,
    linked_github_org: row.org,
    member_count: row.members ?? 4,
    transcript_count: row.transcripts ?? 7,
  };
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

/**
 * Stubs `fetch` with the world and returns every request the page made, in
 * order. A request the world has no route for fails the test.
 */
export function installSurfacesREST(world: SurfacesWorld): RecordedRequest[] {
  const requests: RecordedRequest[] = [];
  const byId = new Map(world.visible.map((row) => [row.id, row]));

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "https://village.test");
    const path = url.pathname.replace(/^\/api\/v1/, "");
    const method = (init?.method ?? "GET").toUpperCase();
    const body: unknown = typeof init?.body === "string" ? JSON.parse(init.body) : null;
    requests.push({ method, path: `${path}${url.search}`, body });

    const refusal = world.refusals.find(
      (r) =>
        r.method === method &&
        r.path === path &&
        (r.username === undefined || (isPlainObject(body) && body.username === r.username)),
    );
    if (refusal) return json({ error: refusal.error }, refusal.status);

    if (path === "/auth/me") return world.signedIn ? json(viewerUser(world)) : json({ error: "not signed in" }, 401);
    if (!world.signedIn) return json({ error: "Authentication required" }, 401);
    if (path === "/auth/orgs") {
      return json(
        world.orgs.map(
          (org, index): UserOrg => ({
            org_id: index + 1,
            org_login: org.login,
            avatar_url: null,
            visible: org.visible,
            fetched_at: "2026-01-01T00:00:00Z",
          }),
        ),
      );
    }
    if (path === "/collectives/visible") return json(world.visible.map(visibleRow));
    if (path === "/users/me/collectives/contributions") {
      return json({
        collectives: world.contributed.map(
          (id): ContributedCollective => ({
            id,
            name: byId.get(id)?.name ?? id,
            description: null,
            linked_github_org: null,
            approved_count: 1,
            pending_count: 0,
            rejected_attempt_count: 0,
            withdrawn_attempt_count: 0,
          }),
        ),
      });
    }
    if (path === "/collectives/search") {
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      const rows = world.searchable
        .map((entry) => (typeof entry === "string" ? byId.get(entry) : entry))
        .filter((row): row is WorldCollective => !!row)
        .filter((row) => row.name.toLowerCase().includes(q) || (row.org ?? "").toLowerCase().includes(q));
      return json({ collectives: rows.map(searchRow) });
    }
    if (path === "/collectives" && method === "POST") {
      const sent = body as Record<string, unknown>;
      return json(
        {
          id: world.createdId,
          name: sent.name,
          description: sent.description ?? null,
          acceptance_mode: sent.acceptance_mode ?? "open",
          data_access: sent.data_access ?? "members_only",
          linked_github_org: sent.linked_github_org ?? null,
          display_members: true,
          transcript_deletion_policy: "user_choice",
          post_prompts_check: true,
          prompts_check_mode: "informational",
          creator: profileOwner({ id: world.viewer.id, github_username: "collective-owner" }),
          created_at: "2026-01-03T00:00:00Z",
          updated_at: "2026-01-03T00:00:00Z",
        },
        201,
      );
    }
    const members = path.match(/^\/collectives\/([^/]+)\/members$/);
    if (members && method === "POST") return json({ status: "added" });
    const join = path.match(/^\/collectives\/([^/]+)\/join$/);
    if (join && method === "POST") {
      const row = byId.get(join[1]);
      if (row) {
        // The server's answer after a join: the collective now lists the
        // caller as a contributor.
        row.role = "contributor";
      }
      return json({ status: "joined", role: "contributor" });
    }
    throw new Error(`the collectives world has no route for ${method} ${path}${url.search}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return requests;
}

// ── mounting ─────────────────────────────────────────────────────────────

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

/** Renders the real `/collectives` route and waits for its heading. */
export async function renderCollectivesPage(): Promise<void> {
  await act(async () => {
    render(
      <Providers>
        <CollectivesPage />
      </Providers>,
    );
  });
  await screen.findByRole("heading", { level: 1, name: "collectives" });
}

/** Renders the real `/discover` route and waits for its heading. */
export async function renderDiscoverPage(): Promise<void> {
  await act(async () => {
    render(
      <Providers>
        <DiscoverPage />
      </Providers>,
    );
  });
  await screen.findByRole("heading", { level: 1, name: "discover" });
}

/** Shared teardown: unmount, drop the `fetch` stub, reset the recorded navigation. */
export function installSurfacesTeardown(): void {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.useRealTimers();
    globalThis.localStorage?.clear();
    resetNextNavigation();
    document.documentElement.setAttribute("data-theme", "dark");
  });
}

// ── reading what is drawn ────────────────────────────────────────────────

/** An element's visible text with its whitespace collapsed. */
export function textOf(element: Element | null | undefined): string {
  return (element?.textContent ?? "").replace(/\s+/g, " ").trim();
}

/** One row of the collectives table, as a person reads it. */
export interface TableRow {
  name: string;
  href: string | null;
  role: string;
  contributed: boolean;
  publish: string;
  members: string;
  transcripts: string;
}

/** The collectives table's rows, in the order they are drawn. */
export function tableRows(): TableRow[] {
  const table = screen.getByRole("table", { name: "collectives you belong to" });
  return within(table)
    .getAllByRole("row")
    .slice(1)
    .map((tr) => {
      const cells = tr.querySelectorAll("td");
      const link = cells[0]?.querySelector("a");
      const standing = cells[1];
      return {
        name: textOf(link),
        href: link?.getAttribute("href") ?? null,
        role: textOf(standing?.querySelector(".on-standing > span:first-child")),
        contributed: [...(standing?.querySelectorAll(".on-tag") ?? [])].some((tag) => textOf(tag) === "contributed"),
        publish: textOf(cells[2]),
        members: textOf(cells[3]),
        transcripts: textOf(cells[4]),
      };
    });
}

/** One discover card, as a person reads it. */
export interface CardRead {
  name: string;
  href: string | null;
  members: string;
  who: string;
  tags: string[];
  transcripts: string;
  /** The card's action: its visible label, and whether it is a link (with its address) or a button. */
  action: { label: string; href: string | null; primary: boolean } | null;
  message: string;
}

/** The discover cards, in the order they are drawn. */
export function cards(): CardRead[] {
  return [...document.querySelectorAll<HTMLElement>(".on-cards.is-collectives > li > article")].map(cardRead);
}

export function cardNamed(name: string): HTMLElement {
  const found = [...document.querySelectorAll<HTMLElement>(".on-cards.is-collectives > li > article")].filter(
    (card) => textOf(card.querySelector("h2")) === name,
  );
  if (found.length !== 1) throw new Error(`expected one card named "${name}", found ${found.length}`);
  return found[0];
}

function cardRead(card: HTMLElement): CardRead {
  const foot = card.querySelector(".on-card-foot");
  const action = foot?.querySelector("a, button") ?? null;
  return {
    name: textOf(card.querySelector("h2")),
    href: card.querySelector("h2 a")?.getAttribute("href") ?? null,
    members: textOf(card.querySelector(".on-card-head .on-chrome")),
    who: textOf(card.querySelector(".on-card-who")),
    tags: [...card.querySelectorAll(".on-card-tags .on-tag")].map(textOf),
    transcripts: textOf(foot?.querySelector(".on-chrome")),
    action: action
      ? {
          label: textOf(action),
          href: action.tagName === "A" ? action.getAttribute("href") : null,
          primary: action.classList.contains("btn-primary"),
        }
      : null,
    message: textOf(card.querySelector(".on-card-msg")),
  };
}
