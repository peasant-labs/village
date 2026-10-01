import { cleanup, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ContributableTranscript } from "@/lib/contribute/types";
import { installGroupRouteREST, renderGroupContributeRoute } from "@/test/mountedGroupRoute";
import { assertDisclosures, chippedParentIDs, flush } from "@/test/childSessionDom";
import {
  loadChildSessionGroupingFixtures,
  type ChildSessionGroupingCase,
  type ChildSessionRow,
  type ChildSessionSurface,
} from "@/test/childSessionGroupingFixtures";

/**
 * Mounted evidence for a session that another session started, on the REAL
 * collective contribute route, with only HTTP controlled.
 *
 *   /groups/{id}/contribute   the project > branch > session selection tree.
 *
 * The tree draws a checkbox row rather than a link, so its rows are read by the
 * test id each row carries, while the expectations, the labels and the fold
 * itself come from the ONE corpus in src/testdata/child-session-grouping.yaml,
 * shared with the discovery, home, project and library surfaces in
 * src/mountedChildSessionGrouping.test.tsx.
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
  globalThis.localStorage?.clear();
});

// ── the wire rows a case's transcripts arrive as ─────────────────────────────

const GROUP_ID = "collective-1";
/** The signed-in person for every case here. The contribute route is
 *  membership-gated, so a viewer is always named. */
const VIEWER = "ada";

/**
 * Published times descending with the row's position, so the corpus order IS
 * the order the listing answers in.
 */
const PUBLISHED_BASE = Date.UTC(2026, 7, 20, 12, 0, 0);

function publishedAt(index: number): string {
  return new Date(PUBLISHED_BASE - index * 60_000).toISOString();
}

const ROW_TITLE = (row: ChildSessionRow) => `Session ${row.name}`;
const ROW_PROVIDER = "claude-code";
const ROW_BRANCH = "main";

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
