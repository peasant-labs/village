import { describe, expect, it } from "vitest";
import { buildContributeTree } from "@/lib/contribute/tree";
import { leafIds, nodeState } from "@/lib/contribute/selection";
import { applyFilters, harnessCounts } from "@/lib/contribute/filter";
import {
  caseByName,
  loadGroupsContributeTreeFixtures,
  toContributableTranscript,
} from "@/test/groupsContributeTreeFixtures";

const cases = loadGroupsContributeTreeFixtures();

describe("buildContributeTree", () => {
  it("collapses a project's two branches and nests a root's own child under it", () => {
    const c = caseByName(cases, "tree", "two_branch_project_collapses_children");
    const tree = buildContributeTree(c.rows.map(toContributableTranscript));
    expect(tree).toHaveLength(1);
    const [project] = tree;
    expect(project.label).toBe(c.expect.projectLabel);
    const branches = project.children.filter((n) => n.kind === "branch");
    expect(branches.map((b) => b.label)).toEqual(c.expect.branchLabels);

    const main = branches.find((b) => b.label === "main")!;
    expect(main.children.map((s) => s.id)).toEqual(c.expect.mainRootIds);
    expect(main.children[0].children.map((s) => s.id)).toEqual(c.expect.mainRootChildIds);

    const feature = branches.find((b) => b.label === "feature/new-ui")!;
    expect(feature.children.map((s) => s.id)).toEqual(c.expect.featureRootIds);
  });

  it("groups an orphaned row under the project's synthetic orphans node", () => {
    const c = caseByName(cases, "tree", "orphans_under_synthetic_node");
    const tree = buildContributeTree(c.rows.map(toContributableTranscript));
    const [project] = tree;
    expect(project.label).toBe(c.expect.projectLabel);
    const branches = project.children.filter((n) => n.kind === "branch");
    expect(branches.map((b) => b.label)).toEqual(c.expect.branchLabels);
    const orphans = project.children.find((n) => n.kind === "orphans");
    expect(orphans).toBeDefined();
    expect(orphans!.label).toBe(c.expect.orphanLabel);
    expect(orphans!.children.map((s) => s.id)).toEqual(c.expect.orphanSessionIds);
    // The synthetic node's own id must never look like a transcript id a
    // selection or a POST body could name.
    expect(orphans!.id).toBe(`${project.id}::orphans`);
  });
});

describe("harnessCounts", () => {
  it("counts every harness present, computed after the search text narrows", () => {
    const c = caseByName(cases, "tree", "multi_harness_counts");
    const rows = c.rows.map(toContributableTranscript);
    const counts = harnessCounts(rows, c.expect.search as string);
    const expected = c.expect.countsAfterSearch as Record<string, number>;
    for (const [harness, count] of Object.entries(expected)) {
      expect(counts.get(harness)).toBe(count);
    }
    expect(counts.has(c.expect.excludedHarnessAfterSearch as string)).toBe(false);
  });
});

describe("applyFilters (search)", () => {
  it("narrows rows by title, and matches an untitled row by its id", () => {
    const c = caseByName(cases, "tree", "search_narrows_rows");
    const rows = c.rows.map(toContributableTranscript);

    const byTitle = applyFilters(rows, { search: c.expect.searchByTitle as string, harness: null });
    expect(byTitle.map((r) => r.id)).toEqual(c.expect.matchedByTitle);

    const byId = applyFilters(rows, { search: c.expect.searchById as string, harness: null });
    expect(byId.map((r) => r.id)).toEqual(c.expect.matchedById);
  });
});

describe("leafIds / nodeState (already-shared exclusion)", () => {
  it("excludes an already-shared row from the selectable leaf ids", () => {
    const c = caseByName(cases, "tree", "already_shared_disabled");
    const tree = buildContributeTree(c.rows.map(toContributableTranscript));
    const [project] = tree;
    const ids = leafIds(project);
    expect(ids).toEqual(c.expect.selectableLeafIds);
    expect(ids).not.toContain(c.expect.excludedLeafId);

    // A project made ENTIRELY of already-shared rows has no selectable
    // leaf, so its tri-state must read "none", never "all" (there is
    // nothing to call "all" of).
    const allSharedTree = buildContributeTree(
      c.rows
        .map(toContributableTranscript)
        .filter((r) => r.id === c.expect.excludedLeafId)
        .map((r) => ({ ...r, already_shared: true })),
    );
    expect(nodeState(new Set(), allSharedTree[0])).toBe("none");
  });
});
