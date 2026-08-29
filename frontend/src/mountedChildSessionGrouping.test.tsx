import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import ExploreRoute from "@/app/explore/page";
import { AuthProvider } from "@/providers/AuthProvider";
import {
  installHomeRouteREST,
  renderAppRoute,
} from "@/test/mountedHomeRoute";
import {
  installProjectRouteREST,
  renderProjectRoute,
} from "@/test/mountedProjectRoute";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import type { HomeTranscriptCase } from "@/test/homePageFixtures";
import type { TranscriptListItem, TranscriptListResponse, User } from "@/lib/types";
import {
  loadChildSessionGroupingFixtures,
  type ChildSessionGroupingCase,
  type ChildSessionRow,
  type ChildSessionSurface,
} from "@/test/childSessionGroupingFixtures";

/**
 * Mounted evidence for a session that another session started.
 *
 * Three REAL routes answer the same question differently, and each is mounted
 * here as the app mounts it, with only HTTP controlled:
 *
 *   /explore   public discovery. A started session is folded away under the
 *              session that started it and the grid keeps the parent row alone.
 *              There is NO control to reveal what was folded: a browse card
 *              names no parent, so a count hanging off one card would ask a
 *              visitor to guess which card it belonged to.
 *   /          a signed-in visitor's own home. Its recent-sessions list hangs
 *              an expandable chip off the row that started them.
 *   /users/{username}/projects/{projectHash}   the same chip.
 *
 * The permutations live in src/testdata/child-session-grouping.yaml, and each
 * case names the surfaces it is asserted on.
 */

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    refresh: vi.fn(),
    back: vi.fn(),
    forward: vi.fn(),
    prefetch: vi.fn(),
  }),
  usePathname: () => "/",
  useSearchParams: () => new URLSearchParams(),
}));

const fixtures = loadChildSessionGroupingFixtures();

/** Every case asserted on one surface. */
function casesFor(surface: ChildSessionSurface): ChildSessionGroupingCase[] {
  return fixtures.cases.filter((testCase) => testCase.surfaces.includes(surface));
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

// ── shared DOM readers ───────────────────────────────────────────────────────

/** Every transcript id the given root currently links to. */
function linkedIDs(root: ParentNode = document): string[] {
  return [...root.querySelectorAll<HTMLAnchorElement>('a[href^="/transcripts/"]')].map((anchor) =>
    anchor.getAttribute("href")!.replace("/transcripts/", ""),
  );
}

/** The expandable chip hanging under one row. */
function chipFor(parentID: string): HTMLElement {
  const chip = document.querySelector<HTMLElement>(`[data-parent-transcript-id="${parentID}"]`);
  if (chip == null) throw new Error(`no chip of started sessions is rendered under ${parentID}`);
  return chip;
}

/** Ids of every chip on screen, so a surface can be held to exactly the chips
 *  its case expects and no others. */
function chippedParentIDs(): string[] {
  return [...document.querySelectorAll<HTMLElement>("[data-parent-transcript-id]")]
    .map((chip) => chip.getAttribute("data-parent-transcript-id")!)
    .sort();
}

async function flush(): Promise<void> {
  await act(async () => {
    for (let i = 0; i < 4; i += 1) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
  });
}

/**
 * Walks one case's chips on a surface showing `visibleRows`, in that order:
 * the list shows exactly those rows, each chip is collapsed, hangs off its own
 * parent's row, and reveals exactly the sessions that row started.
 *
 * `visibleRows` is passed rather than derived because the two surfaces differ:
 * a project page lists every row that kept its place, and home shows the first
 * few groups. Each case states its own answer for the surface it is asserted
 * on, so neither assertion has to re-implement the rule it is checking.
 */
async function assertChips(
  testCase: ChildSessionGroupingCase,
  listRoot: HTMLElement,
  visibleRows: string[],
): Promise<void> {
  // Before any chip is opened, every link in the list is one of its rows, so
  // this pins the rows AND their order, and fails on an extra row as well as a
  // missing one.
  expect(linkedIDs(listRoot), `${testCase.name}: the rows on screen, in order`).toEqual(visibleRows);

  const shownGroups = testCase.expectedGroups.filter((group) => visibleRows.includes(group.parent));
  expect(chippedParentIDs(), `${testCase.name}: the chips on screen`).toEqual(
    shownGroups.map((group) => group.parent).sort(),
  );

  for (const expectedGroup of shownGroups) {
    const chip = chipFor(expectedGroup.parent);

    // The chip needs no words to say whose children these are, so what it
    // hangs off has to be true of the DOM: the chip and its parent's row are
    // one unit of the list.
    const unit = chip.parentElement;
    if (unit == null) throw new Error(`the chip under ${expectedGroup.parent} has no row beside it`);
    expect(
      linkedIDs(unit),
      `${testCase.name}: the chip under ${expectedGroup.parent} must sit with that row`,
    ).toContain(expectedGroup.parent);

    // A row that carries a chip closes up underneath it by one design-system
    // step, so the chip reads as part of that row rather than as the next thing
    // in the list. jsdom applies no stylesheet, so this asserts the WIRING; the
    // rendered distance is asserted as computed style on the served build by
    // scripts/visual/child-session-shoot.mjs.
    const parentRowClasses = (unit.firstElementChild as HTMLElement).className;
    expect(
      parentRowClasses,
      `${testCase.name}: the row carrying the chip under ${expectedGroup.parent} closes up underneath it`,
    ).toContain("pb-[var(--sp-2)]");
    // Only the BOTTOM moves. Without this the shorthand could be dropped for a
    // bottom-only padding and the row would lose the distance it opens below
    // the row above it, with nothing failing.
    expect(
      parentRowClasses,
      `${testCase.name}: the row carrying the chip under ${expectedGroup.parent} keeps its space above`,
    ).toContain("pt-[var(--sp-3)]");

    const toggle = within(chip).getByTestId("child-session-disclosure-toggle");
    // The EXACT text, not a substring of it. The chip announces a bare count
    // and the agent group beside it announces a leading `+`; a containment
    // check would pass on either, so it could not tell the two apart and would
    // not notice the `+` coming back.
    const chipLabel = within(chip).getByTestId("child-session-disclosure-label");
    expect(
      chipLabel.textContent,
      `${testCase.name}: the collapsed label under ${expectedGroup.parent}`,
    ).toBe(expectedGroup.label);
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    expect(within(chip).queryByTestId("child-session-disclosure-rows")).toBeNull();
    for (const id of expectedGroup.children) {
      expect(
        linkedIDs(listRoot),
        `${testCase.name}: ${id} is inside a collapsed chip, so it must not be on screen yet`,
      ).not.toContain(id);
    }

    await act(async () => {
      await userEvent.click(toggle);
    });
    await flush();

    const rows = within(chip).getByTestId("child-session-disclosure-rows");
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    // Opening it does not reword it: the chip reads the same either way, so the
    // count a viewer decided to open is the count they still see.
    expect(
      chipLabel.textContent,
      `${testCase.name}: the label under ${expectedGroup.parent} once it is open`,
    ).toBe(expectedGroup.label);
    // The control names its own rows for assistive technology.
    expect(toggle.getAttribute("aria-controls")).toBe(rows.getAttribute("id"));
    expect(linkedIDs(rows).sort(), `${testCase.name}: the rows under ${expectedGroup.parent}`).toEqual(
      [...expectedGroup.children].sort(),
    );
  }

  // A row that carries no chip keeps its ordinary rhythm. Without this, giving
  // EVERY row the tighter spacing would satisfy the assertion above while
  // changing every list in the app.
  const chipParents = new Set(shownGroups.map((group) => group.parent));
  for (const row of visibleRows.filter((name) => !chipParents.has(name))) {
    const anchor = listRoot.querySelector<HTMLAnchorElement>(`a[href="/transcripts/${row}"]`);
    if (anchor == null) throw new Error(`${testCase.name}: ${row} is not on screen`);
    expect(
      anchor.parentElement!.className,
      `${testCase.name}: ${row} carries no chip, so it keeps an ordinary row's spacing`,
    ).toContain("py-3");
  }

  // With every chip open, the surface shows exactly its rows and the sessions
  // they started, each once: nothing is duplicated, and nothing appears that
  // the case did not put on screen.
  const reachable = linkedIDs(listRoot);
  const wanted = [...visibleRows, ...shownGroups.flatMap((group) => group.children)];
  expect([...reachable].sort(), `${testCase.name}: everything reachable with the chips open`).toEqual(
    [...wanted].sort(),
  );
}

// ── /explore ─────────────────────────────────────────────────────────────────

function agentRow(id: string): ChildSessionRow {
  return { name: id, ownerID: "owner-agent", localID: id, parentSessionID: null };
}

function wireItem(row: ChildSessionRow, sessionOrigin: "user" | "agent" = "user"): TranscriptListItem {
  return {
    transcript: makeTranscriptFixture({
      id: row.name,
      owner_id: row.ownerID,
      local_id: row.localID,
      parent_session_id: row.parentSessionID,
      title: `Session ${row.name}`,
      project_name: "commons-grouping",
      // Fixed across every wired row so Fairtrade's own hash-keyed card
      // grouping puts them all in ONE project group; this test is about the
      // parent/child fold, not about project grouping.
      project_hash: "1".repeat(64),
      project_display_name: "commons-grouping",
      session_origin: sessionOrigin,
    }),
    tags: [],
    owner: {
      id: row.ownerID,
      github_username: row.ownerID,
      display_name: row.ownerID,
      avatar_url: null,
    } as unknown as User,
  };
}

function listResponse(testCase: ChildSessionGroupingCase): TranscriptListResponse {
  return {
    transcripts: testCase.rows.map((row) => wireItem(row)),
    // The server's own count for the active filters. A case whose result set is
    // larger than one response reports more than it carries, which is what the
    // real paged endpoint does.
    total: testCase.serverTotal,
    agent_total: testCase.agentSessions.length,
    page: 1,
    limit: 24,
  };
}

/** The `origin=agent` scope, which the server serves separately. */
function agentListResponse(testCase: ChildSessionGroupingCase): TranscriptListResponse {
  return {
    transcripts: testCase.agentSessions.map((id) => wireItem(agentRow(id), "agent")),
    total: testCase.agentSessions.length,
    agent_total: testCase.agentSessions.length,
    page: 1,
    limit: 24,
  };
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

/** Serves the two discovery scopes the way the server does: the default list
 *  carries every row the viewer may read except the agent-driven ones and
 *  reports how many it left out, and `origin=agent` carries only those. */
function installExploreREST(testCase: ChildSessionGroupingCase): void {
  const fetchMock = vi.fn(async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input);
    if (url.includes("/auth/me")) return jsonResponse({ error: "not signed in" }, 401);
    if (url.includes("/tags/popular")) return jsonResponse([]);
    if (url.includes("/groups/search")) return jsonResponse({ collectives: [] });
    if (url.includes("/transcripts")) {
      if (url.includes("origin=agent")) return jsonResponse(agentListResponse(testCase));
      return jsonResponse(listResponse(testCase));
    }
    return jsonResponse({});
  });
  vi.stubGlobal("fetch", fetchMock);
}

async function renderExplore(): Promise<void> {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <AuthProvider>
          <ExploreRoute />
        </AuthProvider>
      </QueryClientProvider>,
    );
  });
  await flush();
}

/** The number the list header shows above the browse grid. */
function headerTranscriptCount(): string {
  const eyebrow = [...document.querySelectorAll<HTMLElement>(".cex-eyebrow")].find((el) =>
    (el.textContent ?? "").trim().startsWith("transcripts"),
  );
  if (eyebrow == null) throw new Error("the browse list rendered no transcripts count above the grid");
  const count = eyebrow.querySelector(".cex-count");
  if (count == null) throw new Error("the transcripts eyebrow rendered no count element");
  return (count.textContent ?? "").trim();
}

describe("discovery folds a started session away and offers no control", () => {
  for (const testCase of casesFor("explore")) {
    it(testCase.name, async () => {
      installExploreREST(testCase);
      await renderExplore();

      const foldedRows = testCase.expectedGroups.flatMap((group) => group.children);

      // The browse list shows exactly the rows that were not folded away.
      await waitFor(() => {
        for (const id of testCase.expectedRootRows) {
          expect(linkedIDs(), `${testCase.name}: ${id} must stay in the browse list`).toContain(id);
        }
      });
      for (const id of foldedRows) {
        expect(
          linkedIDs(),
          `${testCase.name}: ${id} was started by a session in this same response, so it must not occupy a browse row`,
        ).not.toContain(id);
      }
      for (const id of testCase.expectedRootRows) {
        expect(
          linkedIDs().filter((seen) => seen === id).length,
          `${testCase.name}: ${id} appears once`,
        ).toBe(1);
      }

      // The count above the grid describes the cards under it.
      expect(headerTranscriptCount(), `${testCase.name}: the count above the browse grid`).toBe(
        String(testCase.expectedVisibleCount),
      );

      // Discovery carries NO control for the rows it folded, on any case,
      // whether or not this one folded anything. A card here names no parent,
      // so a count hanging off one would ask a visitor to guess whose it was.
      expect(
        chippedParentIDs(),
        `${testCase.name}: discovery must offer no control for the sessions it folded away`,
      ).toEqual([]);
      expect(screen.queryByTestId("child-session-disclosure")).toBeNull();
      expect(screen.queryByTestId("child-session-disclosure-toggle")).toBeNull();

      // The agent group is discovery's own collapsed group and is untouched by
      // the fold: it carries the rows no person prompted.
      if (testCase.agentSessions.length === 0) {
        expect(screen.queryByTestId("agent-session-group")).toBeNull();
      } else {
        const agentToggle = screen.getByTestId("agent-session-group-toggle");
        expect(agentToggle.textContent, `${testCase.name}: the agent group's label`).toContain(
          `+ ${testCase.agentSessions.length} agent session`,
        );
        for (const id of testCase.agentSessions) {
          expect(
            linkedIDs(),
            `${testCase.name}: the agent row ${id} stays out of the browse list`,
          ).not.toContain(id);
        }
        await act(async () => {
          await userEvent.click(agentToggle);
        });
        await flush();
        expect(
          linkedIDs(screen.getByTestId("agent-session-group-rows")).sort(),
          `${testCase.name}: the rows in the agent group`,
        ).toEqual([...testCase.agentSessions].sort());
        // Opening the agent group never let a folded row back into the grid.
        for (const id of foldedRows) {
          expect(
            linkedIDs(document.querySelector<HTMLElement>(".cex-results")!.parentElement!),
            `${testCase.name}: ${id} stays folded while the agent group is open`,
          ).not.toContain(id);
        }
      }
    });
  }
});

// ── the signed-in home page ──────────────────────────────────────────────────

/**
 * The case's rows as the owner-scoped list serves them.
 *
 * `publishedAt` descends with the row's position, so the page's own
 * most-recent-first ordering keeps the corpus order and the corpus does not
 * have to carry timestamps it says nothing about.
 */
function homeRows(testCase: ChildSessionGroupingCase): HomeTranscriptCase[] {
  const base = Date.UTC(2026, 7, 24, 12, 0, 0);
  return testCase.rows.map((row, index) => ({
    id: row.name,
    title: `Session ${row.name}`,
    projectHash: "1".repeat(64),
    projectDisplayName: "commons grouping",
    publishedAt: new Date(base - index * 60_000).toISOString(),
    localID: row.localID,
    parentSessionID: row.parentSessionID,
  }));
}

describe("the home page lists a started session inside its parent's chip", () => {
  for (const testCase of casesFor("home")) {
    it(testCase.name, async () => {
      installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: homeRows(testCase) });
      await renderAppRoute("/");
      await flush();

      const panel = await screen.findByTestId("home-recent-sessions");
      await assertChips(testCase, panel, testCase.expectedHomeRows);
    });
  }
});

// ── a project page ───────────────────────────────────────────────────────────

describe("a project page lists a started session under the session that started it", () => {
  for (const testCase of casesFor("project")) {
    it(testCase.name, async () => {
      const projectHash = "2".repeat(64);
      installProjectRouteREST({
        viewer: "alice-dev",
        ownerUsername: "alice-dev",
        projectHash,
        displayName: "commons grouping",
        nameSource: "consented",
        remoteLabel: "github.com/alice-dev/commons",
        transcripts: testCase.rows.map((row) => ({
          id: row.name,
          title: `Session ${row.name}`,
          localID: row.localID,
          parentSessionID: row.parentSessionID,
        })),
        collectives: [],
      });
      await renderProjectRoute("alice-dev", projectHash);
      await flush();

      // The page has settled once its header is on screen; the rows below it
      // come from the same payload.
      await screen.findByTestId("project-display-name");
      // A project page lists every row the fold left in place, in server order.
      await assertChips(testCase, document.body, testCase.expectedRootRows);
    });
  }
});
