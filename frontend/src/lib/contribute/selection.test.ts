import { describe, expect, it } from "vitest";
import { buildContributeTree } from "@/lib/contribute/tree";
import { groupByProject, nodeState, privateIds, selectAll, toggleNode } from "@/lib/contribute/selection";
import type { ContributableTranscript } from "@/lib/contribute/types";

function row(overrides: Partial<ContributableTranscript>): ContributableTranscript {
  return {
    id: overrides.id ?? "id",
    local_id: overrides.local_id ?? overrides.id ?? "id",
    title: overrides.title ?? null,
    visibility: overrides.visibility ?? "public",
    project_hash: overrides.project_hash ?? "proj",
    project_display_name: overrides.project_display_name ?? "proj",
    project_name_source: "consented",
    git_branch: overrides.git_branch ?? null,
    parent_session_id: overrides.parent_session_id ?? null,
    session_origin: "user",
    model_provider: overrides.model_provider ?? "claude-code",
    published_at: "2026-08-20T10:00:00Z",
    already_shared: overrides.already_shared ?? false,
  };
}

describe("toggleNode / nodeState", () => {
  it("selects every eligible leaf when ticked from none, and clears them when ticked from all", () => {
    const tree = buildContributeTree([
      row({ id: "a", local_id: "a", project_hash: "p" }),
      row({ id: "b", local_id: "b", project_hash: "p" }),
    ]);
    const [project] = tree;

    expect(nodeState(new Set(), project)).toBe("none");
    const afterFirstToggle = toggleNode(new Set(), project);
    expect(nodeState(afterFirstToggle, project)).toBe("all");
    expect([...afterFirstToggle].sort()).toEqual(["a", "b"]);

    const afterSecondToggle = toggleNode(afterFirstToggle, project);
    expect(nodeState(afterSecondToggle, project)).toBe("none");
    expect(afterSecondToggle.size).toBe(0);
  });

  it("reads 'some' when only part of a node's eligible leaves are selected", () => {
    const tree = buildContributeTree([
      row({ id: "a", local_id: "a", project_hash: "p" }),
      row({ id: "b", local_id: "b", project_hash: "p" }),
    ]);
    const [project] = tree;
    expect(nodeState(new Set(["a"]), project)).toBe("some");
  });
});

describe("groupByProject", () => {
  it("groups the current selection by project, dropping a project with nothing selected", () => {
    const tree = buildContributeTree([
      row({ id: "a", local_id: "a", project_hash: "p1" }),
      row({ id: "b", local_id: "b", project_hash: "p2" }),
    ]);
    const grouped = groupByProject(new Set(["a"]), tree);
    expect([...grouped.keys()]).toEqual(["p1"]);
    expect(grouped.get("p1")).toEqual(["a"]);
  });
});

describe("privateIds", () => {
  it("returns only the selected ids whose stored visibility is private", () => {
    const tree = buildContributeTree([
      row({ id: "pub", local_id: "pub", visibility: "public", project_hash: "p" }),
      row({ id: "priv", local_id: "priv", visibility: "private", project_hash: "p" }),
    ]);
    const selection = selectAll(tree);
    expect(privateIds(selection, tree)).toEqual(["priv"]);
  });
});
