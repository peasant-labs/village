import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { act } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import type { GroupTranscript, UserGroupShare } from "@/lib/types";
import type { ContributableTranscript } from "@/lib/contribute/types";
import {
  installGroupRouteREST,
  renderGroupContributeRoute,
  renderGroupDetailRoute,
  type PendingShareFixtureRow,
  type RecordedGroupRequest,
} from "@/test/mountedGroupRoute";
import {
  assertDisclosures,
  chippedParentIDs,
  disclosureFor,
  expandDisclosure,
  flush,
  linkedIDs,
  rowFor,
} from "@/test/childSessionDom";
import {
  loadChildSessionGroupingFixtures,
  type ChildSessionGroupingCase,
  type ChildSessionRow,
  type ChildSessionSurface,
} from "@/test/childSessionGroupingFixtures";

/**
 * Mounted evidence for a session that another session started, on the REAL
 * collective routes, with only HTTP controlled.
 *
 *   /groups/{id}              a collective's contributions, read as a list and
 *                             read by repository; the owner's review queue of a
 *                             curated collective; and a member's own
 *                             contributions to it.
 *   /groups/{id}/contribute   the project > branch > session selection tree.
 *
 * These four lists do not agree on what a row IS -- a transcript list draws an
 * anchor, a review queue draws a queue item with its own approve and reject
 * actions, a person's contributions draw an unshare action, a selection tree
 * draws a checkbox -- so each is read here through its own row reader while the
 * expectations, the labels and the fold itself come from the ONE corpus in
 * src/testdata/child-session-grouping.yaml, shared with the discovery, home,
 * project and library surfaces in src/mountedChildSessionGrouping.test.tsx.
 *
 * Beyond the fold, these are the assertions the collective surfaces owe on
 * their own account: a browse row still states every column its dropped table
 * stated, the owner's select-everything control reaches a folded row, a
 * repository's rows still state the model and the branch they ran on, and a
 * revealed submission can still be approved.
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

function casesFor(surface: ChildSessionSurface): ChildSessionGroupingCase[] {
  return fixtures.cases.filter((testCase) => testCase.surfaces.includes(surface));
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

// ── the wire rows a case's transcripts arrive as ─────────────────────────────

const GROUP_ID = "collective-1";
/** The signed-in person for every case here. The collective surfaces are
 *  membership-gated, so a viewer is always named. */
const VIEWER = "ada";
/** One remote across every row, so the repository view draws ONE repository and
 *  what is asserted there is the fold inside it. A session and the sessions it
 *  starts share a working tree, so one remote is also the realistic case. */
const REPO_REMOTE = "github.com/ada/commons";

/**
 * Published times descending with the row's position, so the corpus order IS
 * the order the collective's own most-recent-first repository grouping
 * produces. Without this the repository view would reorder the rows and the
 * expectations would be asserting the sort rather than the fold.
 */
const PUBLISHED_BASE = Date.UTC(2026, 7, 20, 12, 0, 0);

function publishedAt(index: number): string {
  return new Date(PUBLISHED_BASE - index * 60_000).toISOString();
}

/** The date a row states, written from the same Intl request the design system
 *  makes of every date in this app -- stated here independently, so dropping
 *  the date from a row fails rather than agreeing with itself. */
function expectedDate(index: number): string {
  return new Date(publishedAt(index)).toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

/** Turns and tokens vary by row, so an assertion cannot pass on a list that
 *  states one row's numbers against every row. */
function expectedTurns(index: number): number {
  return 3 + index;
}

function tokenCount(index: number): number {
  return 1000 * (index + 1) + 500;
}

/** What {@link tokenCount} reads as once the list has shortened it. */
function expectedTokens(index: number): string {
  return `${(tokenCount(index) / 1000).toFixed(1)}K tok`;
}

const ROW_TITLE = (row: ChildSessionRow) => `Session ${row.name}`;
const ROW_PROVIDER = "claude-code";
const ROW_MODEL_NAME = "sonnet-4-5";
/** {@link ROW_MODEL_NAME} as the list writes it out. */
const EXPECTED_MODEL = "Sonnet 4.5";
const ROW_BRANCH = "main";

function groupTranscript(row: ChildSessionRow, index: number): GroupTranscript {
  return {
    ...makeTranscriptFixture({
      id: row.name,
      owner_id: row.ownerID,
      local_id: row.localID,
      parent_session_id: row.parentSessionID,
      title: ROW_TITLE(row),
      model_provider: ROW_PROVIDER,
      model_name: ROW_MODEL_NAME,
      turn_count: expectedTurns(index),
      token_count: tokenCount(index),
      published_at: publishedAt(index),
      git_branch: ROW_BRANCH,
      git_remote: REPO_REMOTE,
      project_remote_label: "github.com:ada/commons",
    }),
    owner_username: row.ownerID,
    owner_avatar_url: null,
    owner_is_discoverable: true,
  };
}

function pendingShare(row: ChildSessionRow, index: number): PendingShareFixtureRow {
  return {
    transcript_id: row.name,
    title: ROW_TITLE(row),
    model_provider: ROW_PROVIDER,
    owner_id: row.ownerID,
    local_id: row.localID,
    parent_session_id: row.parentSessionID,
    owner_username: row.ownerID,
    owner_is_discoverable: true,
    shared_at: publishedAt(index),
  };
}

function myShare(row: ChildSessionRow, index: number): UserGroupShare {
  return {
    id: row.name,
    owner_id: row.ownerID,
    local_id: row.localID,
    parent_session_id: row.parentSessionID,
    title: ROW_TITLE(row),
    model_provider: ROW_PROVIDER,
    model_name: ROW_MODEL_NAME,
    visibility: "shared",
    published_at: publishedAt(index),
    turn_count: expectedTurns(index),
    tokens_in: null,
    tokens_out: null,
    status: "approved",
    shared_at: publishedAt(index),
  };
}

function contributableRow(row: ChildSessionRow, index: number): ContributableTranscript {
  return {
    id: row.name,
    local_id: row.localID,
    title: ROW_TITLE(row),
    visibility: "private",
    // ONE project across every row: the contribute tree folds per project,
    // because a session id is a per-project value there.
    project_hash: "commons-project",
    project_display_name: "commons",
    project_name_source: "consented",
    git_branch: ROW_BRANCH,
    parent_session_id: row.parentSessionID,
    session_origin: "user",
    model_provider: ROW_PROVIDER,
    published_at: publishedAt(index),
    already_shared: false,
  };
}

// ── the collective's contributions, read as a list ───────────────────────────

/** The row one transcript is drawn into, found by the row-wide link the list
 *  puts over it. */
function browseRowFor(row: ChildSessionRow): HTMLElement {
  const anchor = document.querySelector<HTMLAnchorElement>(
    `a[aria-label="Open transcript ${ROW_TITLE(row)}"]`,
  );
  if (anchor == null) throw new Error(`${row.name} is not drawn on the collective's browse list`);
  return anchor.parentElement!;
}

/** The selection box on one row, root or folded. */
function selectionBoxFor(row: ChildSessionRow): HTMLInputElement {
  return within(browseRowFor(row)).getByRole("checkbox", {
    name: `Select transcript ${ROW_TITLE(row)}`,
  }) as HTMLInputElement;
}

async function renderCollectiveDetail(
  fixtureRows: ChildSessionRow[],
  extra: Partial<Parameters<typeof installGroupRouteREST>[0]> = {},
): Promise<RecordedGroupRequest[]> {
  const requests = installGroupRouteREST({
    viewer: VIEWER,
    groupId: GROUP_ID,
    groupName: "commons",
    role: "owner",
    transcripts: fixtureRows.map(groupTranscript),
    ...extra,
  });
  await renderGroupDetailRoute(GROUP_ID);
  await flush();
  return requests;
}

describe("a collective's contributions read a started session under the session that started it", () => {
  for (const testCase of casesFor("collective-browse")) {
    it(testCase.name, async () => {
      await renderCollectiveDetail(testCase.rows);
      await waitFor(() => expect(linkedIDs().length).toBeGreaterThan(0));

      // The browse list is the only thing on this page that links a transcript,
      // so the whole document is the list's own root here.
      await assertDisclosures(testCase, document.body, testCase.expectedRootRows, linkedIDs);
    });
  }

  it("states every column the dropped table stated, on a row and on a folded row alike", async () => {
    const testCase = fixtures.cases.find(
      (c) => c.name === "a-collectives-contributions-read-a-started-session-under-its-starter",
    )!;
    await renderCollectiveDetail(testCase.rows);
    await waitFor(() => expect(linkedIDs().length).toBeGreaterThan(0));

    // Reveal the folded rows, so a folded row is held to the same columns as a
    // row that kept its place. A row that lost its columns on the way into a
    // group would be a worse row for having been folded.
    const group = testCase.expectedGroups[0];
    const { chip, toggle } = disclosureFor(group.parent);
    await expandDisclosure(toggle, chip);

    for (const [index, row] of testCase.rows.entries()) {
      const rowElement = browseRowFor(row);
      const text = rowElement.textContent ?? "";
      const where = `${row.name}: the collective's browse row`;
      expect(text, `${where} states its title`).toContain(ROW_TITLE(row));
      expect(text, `${where} states who contributed it`).toContain(row.ownerID);
      expect(text, `${where} states the provider`).toContain(ROW_PROVIDER);
      expect(text, `${where} states the turns`).toContain(`${expectedTurns(index)} turns`);
      expect(text, `${where} states the tokens`).toContain(expectedTokens(index));
      expect(text, `${where} states the date`).toContain(expectedDate(index));

      // The row opens the transcript, and the handle leads to that person's
      // library -- the app's own way from a collective into a contributor's
      // work.
      expect(
        rowElement.querySelector('a[aria-label^="Open transcript"]')!.getAttribute("href"),
        `${where} links to the transcript`,
      ).toBe(`/transcripts/${row.name}`);
      expect(
        rowElement.querySelector(`a[href="/users/${row.ownerID}"]`),
        `${where} links the contributor's handle`,
      ).not.toBeNull();
    }
  });

  it("lets the owner pick out a folded row, exactly like a row that kept its place", async () => {
    const testCase = fixtures.cases.find(
      (c) => c.name === "a-collectives-contributions-read-a-started-session-under-its-starter",
    )!;
    await renderCollectiveDetail(testCase.rows);
    await waitFor(() => expect(linkedIDs().length).toBeGreaterThan(0));

    const group = testCase.expectedGroups[0];
    const { chip, toggle } = disclosureFor(group.parent);
    await expandDisclosure(toggle, chip);

    const foldedRow = testCase.rows.find((row) => row.name === group.children[0])!;
    const box = selectionBoxFor(foldedRow);
    expect(box.checked, `${foldedRow.name} starts unpicked`).toBe(false);

    await act(async () => {
      await userEvent.click(box);
    });
    await flush();

    expect(selectionBoxFor(foldedRow).checked, `${foldedRow.name} is picked out`).toBe(true);
    // The page reports the picked-out rows, which is what the remove action
    // then acts on; a box that ticked without joining the set would remove
    // nothing.
    expect(document.body.textContent, "the page counts the picked-out row").toContain("1 selected");
  });

  it("ticks every row on the page with one control, folded rows included", async () => {
    const testCase = fixtures.cases.find(
      (c) => c.name === "a-collectives-contributions-read-a-started-session-under-its-starter",
    )!;
    await renderCollectiveDetail(testCase.rows);
    await waitFor(() => expect(linkedIDs().length).toBeGreaterThan(0));

    const group = testCase.expectedGroups[0];
    const { chip, toggle } = disclosureFor(group.parent);
    await expandDisclosure(toggle, chip);

    const selectAll = screen.getByRole("checkbox", {
      name: "Select every transcript on this page",
    });
    await act(async () => {
      await userEvent.click(selectAll);
    });
    await flush();

    // "Select everything" means everything the page holds. A folded row that
    // stayed unpicked here would be silently left behind by a remove the owner
    // believed covered the page -- which is the regression this asserts.
    for (const row of testCase.rows) {
      expect(
        selectionBoxFor(row).checked,
        `${row.name} is picked out by the select-everything control`,
      ).toBe(true);
    }
    expect(document.body.textContent, "the page counts every row on it").toContain(
      `${testCase.rows.length} selected`,
    );
  });
});

// ── the same contributions, read by repository ───────────────────────────────

/** Switches the collective's browse panel to its repository view. */
async function showRepositories(): Promise<void> {
  await act(async () => {
    await userEvent.click(screen.getByRole("button", { name: "repos" }));
  });
  await flush();
}

describe("a repository's rows read a started session under the session that started it", () => {
  for (const testCase of casesFor("collective-repos")) {
    it(testCase.name, async () => {
      await renderCollectiveDetail(testCase.rows);
      await waitFor(() => expect(linkedIDs().length).toBeGreaterThan(0));
      await showRepositories();

      await assertDisclosures(testCase, document.body, testCase.expectedRootRows, linkedIDs);
    });
  }

  it("states the model, the branch and the date under a repository", async () => {
    const testCase = fixtures.cases.find(
      (c) => c.name === "a-collectives-contributions-read-a-started-session-under-its-starter",
    )!;
    await renderCollectiveDetail(testCase.rows);
    await waitFor(() => expect(linkedIDs().length).toBeGreaterThan(0));
    await showRepositories();

    const group = testCase.expectedGroups[0];
    const { chip, toggle } = disclosureFor(group.parent);
    await expandDisclosure(toggle, chip);

    for (const [index, row] of testCase.rows.entries()) {
      const text = browseRowFor(row).textContent ?? "";
      const where = `${row.name}: the row under its repository`;
      expect(text, `${where} states its title`).toContain(ROW_TITLE(row));
      expect(text, `${where} states which model ran`).toContain(EXPECTED_MODEL);
      expect(text, `${where} states the branch`).toContain(ROW_BRANCH);
      expect(text, `${where} states the date`).toContain(expectedDate(index));
    }
  });
});

// ── the review queue of a curated collective ─────────────────────────────────

/**
 * The review queue this page renders for the owner.
 *
 * The shared governance summary above it renders a queue of its own, so the
 * queue is found by what only this one has: a link to each submission's
 * transcript. Taking "the first queue on the page" would let an assertion pass
 * against the summary while the real queue was wrong.
 */
function reviewQueue(): HTMLElement {
  const queue = [...document.querySelectorAll<HTMLElement>("section.mod-queue")].find(
    (candidate) => candidate.querySelector('a[href^="/transcripts/"]') != null,
  );
  if (queue == null) throw new Error("the collective rendered no review queue of its submissions");
  return queue;
}

async function renderCuratedQueue(
  testCase: ChildSessionGroupingCase,
): Promise<RecordedGroupRequest[]> {
  const requests = await renderCollectiveDetail([], {
    acceptanceMode: "curated",
    transcripts: [],
    pendingShares: testCase.rows.map(pendingShare),
  });
  await waitFor(() => expect(reviewQueue()).toBeTruthy());
  return requests;
}

/**
 * The review queue is the ONE list here that does not read a started submission
 * under the submission that started it.
 *
 * That is a decision, not an oversight, so it is asserted rather than left as
 * an absence of assertions. The queue component cannot nest a row, and forcing
 * one made the queue worse to work in: a revealed submission truncated its own
 * title, and a row's approve and reject drifted away from the title they
 * decide. Every row here is an irreversible decision, so a flat list is the
 * better answer until the component gains the affordance
 * (peasant-labs/fairtrade-design-system#75); the review page that replaces this
 * queue folds natively.
 *
 * What must hold is the property the fold could have taken away: every
 * submission is listed, and every one can still be decided.
 */
describe("a review queue lists every submission side by side, each still decidable", () => {
  for (const testCase of casesFor("pending-queue")) {
    it(testCase.name, async () => {
      await renderCuratedQueue(testCase);

      // Every row, in server order -- including the ones another submission
      // started. A build that folded them would list fewer.
      expect(
        linkedIDs(reviewQueue()),
        `${testCase.name}: the submissions on screen, in order`,
      ).toEqual(testCase.rows.map((row) => row.name));

      // And no collapsed control anywhere on the queue. Asserted positively so
      // a fold arriving here fails, rather than passing unnoticed because
      // nothing looked for one.
      expect(
        chippedParentIDs(reviewQueue()),
        `${testCase.name}: the review queue draws no collapsed control`,
      ).toEqual([]);
    });
  }

  it("gives every submission its own approve and reject, and sends the decision for the row clicked", async () => {
    const testCase = fixtures.cases.find(
      (c) => c.name === "a-review-queue-lists-a-started-submission-beside-its-starter",
    )!;
    const requests = await renderCuratedQueue(testCase);

    // The started submission, the one a fold would have moved. It is reachable
    // where it is, and carries both decisions.
    const startedID = testCase.rows.find((row) => row.parentSessionID !== null)!.name;
    const row = rowFor(reviewQueue(), startedID);
    expect(
      within(row).getByRole("button", { name: /reject/i }),
      `${startedID} keeps its reject action`,
    ).toBeTruthy();
    const approve = within(row).getByRole("button", { name: /approve/i });

    await act(async () => {
      await userEvent.click(approve);
    });
    await flush();

    // The decision must name the row that was clicked, never its starter.
    const decisions = requests.filter((request) => request.method === "PATCH");
    expect(
      decisions.map((request) => ({
        path: new URL(request.url, "https://village.test").pathname,
        body: request.body,
      })),
      "the decision a moderator's click sent",
    ).toEqual([
      {
        path: `/api/v1/groups/${GROUP_ID}/shares/${startedID}`,
        body: { status: "approved" },
      },
    ]);
  });
});

// ── a member's own contributions to a collective ─────────────────────────────

describe("your contributions read a started contribution under the one that started it", () => {
  for (const testCase of casesFor("my-contributions")) {
    it(testCase.name, async () => {
      await renderCollectiveDetail([], {
        role: "member",
        transcripts: [],
        myShares: testCase.rows.map(myShare),
      });
      await waitFor(() => expect(linkedIDs().length).toBeGreaterThan(0));

      // Nothing else on this page links a transcript for this fixture: the
      // collective holds no browsable contributions here, so the whole document
      // is this list's root.
      await assertDisclosures(testCase, document.body, testCase.expectedRootRows, linkedIDs);
    });
  }

  it("keeps the unshare action on a revealed contribution", async () => {
    const testCase = fixtures.cases.find(
      (c) => c.name === "your-contributions-read-a-started-contribution-under-its-starter",
    )!;
    await renderCollectiveDetail([], {
      role: "member",
      transcripts: [],
      myShares: testCase.rows.map(myShare),
    });
    await waitFor(() => expect(linkedIDs().length).toBeGreaterThan(0));

    const group = testCase.expectedGroups[0];
    const { chip, toggle } = disclosureFor(group.parent);
    const revealed = await expandDisclosure(toggle, chip);

    expect(linkedIDs(revealed), "the revealed contribution").toEqual(group.children);
    expect(
      within(revealed).getAllByTitle("Unshare from this collective"),
      "a revealed contribution can still be taken back",
    ).toHaveLength(group.children.length);
  });
});

// ── the contribute selection tree ────────────────────────────────────────────

/** Every session the tree currently draws, in document order. The tree draws a
 *  checkbox row rather than a link, so its rows are read by the test id each
 *  row carries. */
function treeRowIDs(root: ParentNode = document): string[] {
  return [...root.querySelectorAll<HTMLElement>('[data-testid^="contribute-session-row-"]')].map(
    (row) => row.getAttribute("data-testid")!.replace("contribute-session-row-", ""),
  );
}

describe("the contribute tree nests a started session under the session that started it", () => {
  for (const testCase of casesFor("contribute")) {
    it(testCase.name, async () => {
      installGroupRouteREST({
        viewer: VIEWER,
        groupId: GROUP_ID,
        groupName: "commons",
        role: "member",
        contributable: testCase.rows.map(contributableRow),
      });
      await renderGroupContributeRoute(GROUP_ID);
      await flush();
      await waitFor(() => expect(treeRowIDs().length).toBeGreaterThan(0));

      await assertDisclosures(testCase, document.body, testCase.expectedRootRows, treeRowIDs);
      // The tree is the one surface here that draws no transcript link, so its
      // controls are also proven to be the shared ones rather than a second
      // control of its own.
      expect(
        chippedParentIDs(),
        `${testCase.name}: the controls the tree draws`,
      ).toEqual(testCase.expectedGroups.map((group) => group.parent).sort());
    });
  }
});
