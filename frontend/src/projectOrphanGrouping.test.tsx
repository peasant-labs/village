import { act, cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { groupProjectSessionRows, type SessionIdentity } from "@/lib/childSessions";
import {
  loadProjectOrphanGroupingFixtures,
  type ProjectOrphanRow,
  type ProjectOrphanTransition,
} from "@/test/projectOrphanGroupingFixtures";
import { installProjectRouteREST, renderProjectRoute } from "@/test/mountedProjectRoute";
import { linkedIDs } from "@/test/childSessionDom";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn(), back: vi.fn(), forward: vi.fn(), prefetch: vi.fn() }),
  usePathname: () => "/users/alice-dev/projects/project-hash",
  useSearchParams: () => new URLSearchParams(),
}));

const fixtures = loadProjectOrphanGroupingFixtures();
const identify = (row: ProjectOrphanRow): SessionIdentity => ({
  rowID: row.name,
  ownerID: row.ownerID,
  sessionID: row.localID,
  parentSessionID: row.parentSessionID,
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  globalThis.localStorage?.clear();
});

describe("project ancestry classification", () => {
  for (const testCase of fixtures.cases) {
    it(testCase.name, () => {
      const result = groupProjectSessionRows(testCase.rows, identify, (row) => row.origin === "agent");
      expect(result.rootItems.map((row) => row.name)).toEqual(testCase.expectedRoots);
      expect(result.groups.map((group) => ({ parent: group.parent.name, children: group.children.map((row) => row.name) }))).toEqual(testCase.expectedGroups);
      expect(result.orphanItems.map((row) => row.name)).toEqual(testCase.expectedOrphans);
    });
  }
});

function routeRows(rows: ProjectOrphanRow[]) {
  return rows.map((row) => ({
    id: row.name,
    title: `Session ${row.name}`,
    localID: row.localID,
    parentSessionID: row.parentSessionID,
    sessionOrigin: row.origin,
  }));
}

async function mountTransition(transition: ProjectOrphanTransition) {
  const projectHash = "4".repeat(64);
  installProjectRouteREST({
    viewer: "alice-dev",
    ownerUsername: "alice-dev",
    projectHash,
    displayName: "orphan grouping",
    nameSource: "consented",
    remoteLabel: "github.com/alice-dev/orphans",
    transcripts: [],
    transcriptResponses: [routeRows(transition.initial), routeRows(transition.refreshed)],
    collectives: [],
  });
  const client = await renderProjectRoute("alice-dev", projectHash);
  await screen.findByTestId("project-display-name");
  return client;
}

describe("the mounted project route groups unresolved ancestry", () => {
  it("renders one collapsed display-only group with real rows and owner actions", async () => {
    const testCase = fixtures.cases.find((c) => c.name === "only-orphans")!;
    await mountTransition({
      name: "static",
      initial: testCase.rows,
      refreshed: testCase.rows,
      expectedOrphansBefore: testCase.expectedOrphans,
      expectedRootsAfter: testCase.expectedRoots,
      expectedGroupsAfter: testCase.expectedGroups,
      expectedOrphansAfter: testCase.expectedOrphans,
    });

    const toggle = screen.getByTestId("project-orphan-session-group-toggle");
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByTestId("project-orphan-session-group-label")).toHaveTextContent(`orphan sessions ${testCase.expectedOrphans.length}`);
    expect(toggle.closest("a")).toBeNull();
    toggle.focus();
    await userEvent.keyboard("{Enter}");
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const rows = screen.getByTestId("project-orphan-session-rows");
    expect(linkedIDs(rows)).toEqual(testCase.expectedOrphans);
    for (const id of testCase.expectedOrphans) {
      expect(rows.querySelector(`a[href="/transcripts/${id}"]`)).not.toBeNull();
    }
    expect(rows.querySelectorAll('button[aria-label="Edit transcript"]')).toHaveLength(testCase.expectedOrphans.length);
    expect(rows.querySelectorAll('button[aria-label="Delete transcript"]')).toHaveLength(testCase.expectedOrphans.length);
    await userEvent.click(rows.querySelector<HTMLButtonElement>('button[aria-label="Edit transcript"]')!);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByTestId("project-transcript-count").textContent?.trim()).toBe(String(testCase.rows.length));
  });

  for (const transition of fixtures.transitions) {
    it(transition.name, async () => {
      const client = await mountTransition(transition);
      const before = screen.getByTestId("project-orphan-session-group-toggle");
      expect(before).toHaveTextContent(`orphan sessions ${transition.expectedOrphansBefore.length}`);
      expect(screen.getByTestId("project-transcript-count").textContent?.trim()).toBe(String(transition.initial.length));

      await act(async () => { await client.refetchQueries({ queryKey: ["user-project"] }); });

      await waitFor(() => {
        for (const root of transition.expectedRootsAfter) {
          expect(document.querySelector(`a[href="/transcripts/${root}"]`)).not.toBeNull();
        }
        const group = screen.queryByTestId("project-orphan-session-group-toggle");
        if (transition.expectedOrphansAfter.length === 0) expect(group).toBeNull();
        else expect(group).toHaveTextContent(`orphan sessions ${transition.expectedOrphansAfter.length}`);
      });

      if (transition.expectedOrphansAfter.length > 0) {
        await userEvent.click(screen.getByTestId("project-orphan-session-group-toggle"));
        expect(linkedIDs(screen.getByTestId("project-orphan-session-rows"))).toEqual(transition.expectedOrphansAfter);
      }

      for (const expectedGroup of transition.expectedGroupsAfter) {
        const disclosure = document.querySelector<HTMLElement>(
          `[data-parent-transcript-id="${expectedGroup.parent}"]`,
        );
        expect(disclosure, `${transition.name}: disclosure under ${expectedGroup.parent}`).not.toBeNull();
        await userEvent.click(disclosure!.querySelector<HTMLButtonElement>(
          '[data-testid="child-session-disclosure-toggle"]',
        )!);
        expect(linkedIDs(disclosure!.querySelector<HTMLElement>(
          '[data-testid="child-session-disclosure-rows"]',
        )!)).toEqual(expectedGroup.children);
      }

      const refreshedIDs = transition.refreshed.map((row) => row.name).sort();
      const renderedIDs = linkedIDs().filter((id) => refreshedIDs.includes(id)).sort();
      expect(renderedIDs).toEqual(refreshedIDs);
      expect(new Set(renderedIDs).size).toBe(transition.refreshed.length);
      expect(screen.getByTestId("project-transcript-count").textContent?.trim()).toBe(String(transition.refreshed.length));
    });
  }
});
