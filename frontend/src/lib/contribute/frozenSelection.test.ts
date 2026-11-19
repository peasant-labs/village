import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { parse } from "yaml";
import {
  zVillageContributionSnapshotResponse,
  type VillageContributableTranscript,
  type VillageContributionSnapshotResponse,
  type VillageShareStatus,
  type VillageTranscriptVisibility,
} from "@peasant-labs/schema";
import {
  allSelected,
  bySubmissionProject,
  familyState,
  isLiveStatus,
  loadedProjectState,
  mergeSnapshot,
  selectedInProject,
  toggleFamily,
  toggleLoadedProject,
  withoutLive,
  type CheckState,
  type FrozenSelection,
  type LoadedRow,
  type SelectedRecord,
} from "./frozenSelection";
import { GROUP_ID, projectHashOf, transcriptIdOf, transcriptOf } from "./testTokens";

interface RowToken {
  id: string;
  project: string;
  root: string;
  family: string;
  locked?: boolean;
}

interface SelectedToken {
  id: string;
  project: string;
  family: string;
}

interface SnapshotToken {
  project: string;
  records: { id: string; project: string; visibility: VillageTranscriptVisibility; status?: VillageShareStatus }[];
}

interface Fixtures {
  live_status: { status: VillageShareStatus | null; live: boolean }[];
  family_toggle: {
    name: string;
    rows: RowToken[];
    selected: SelectedToken[];
    state_before: CheckState;
    state_after?: CheckState;
    expect_selected: string[];
  }[];
  loaded_project_toggle: {
    name: string;
    rows: RowToken[];
    selected: SelectedToken[];
    project: string;
    state_before: CheckState;
    expect_selected: string[];
  }[];
  snapshot_merge: {
    name: string;
    selected: SelectedToken[];
    snapshot: SnapshotToken;
    expect: {
      selected: string[];
      selectable: string[];
      added: number;
      locked: number;
      omitted: string[];
      all_selected: boolean;
      records?: SelectedToken[];
    };
  }[];
  without_live: {
    name: string;
    rows: RowToken[];
    selected: SelectedToken[];
    expect_became_live: string[];
    expect_selected: string[];
  }[];
  projects: {
    name: string;
    selected: SelectedToken[];
    expect_counted: Record<string, number>;
    expect_submitted: Record<string, string[]>;
  }[];
}

const fixtures = parse(readFileSync(resolve(process.cwd(), "src/testdata/frozen-selection.yaml"), "utf8")) as Fixtures;

function rowOf(token: RowToken): LoadedRow {
  return {
    transcript: transcriptOf({ id: token.id, project: token.project, already_shared: token.locked ?? false }) as VillageContributableTranscript,
    family_project_hash: projectHashOf(token.family),
    root_id: transcriptIdOf(token.root),
    is_root: token.id === token.root,
  };
}

function selectionOf(tokens: SelectedToken[]): FrozenSelection {
  const selection = new Map<string, SelectedRecord>();
  for (const token of tokens) {
    const id = transcriptIdOf(token.id);
    selection.set(id, { id, project_hash: projectHashOf(token.project), family_project_hash: projectHashOf(token.family), visibility: "private" });
  }
  return selection;
}

function snapshotOf(token: SnapshotToken): VillageContributionSnapshotResponse {
  return zVillageContributionSnapshotResponse.parse({
    group_id: GROUP_ID,
    project_hash: projectHashOf(token.project),
    count: token.records.length,
    records: token.records.map((record) => ({
      id: transcriptIdOf(record.id),
      project_hash: projectHashOf(record.project),
      visibility: record.visibility,
      ...(record.status ? { latest_status: record.status } : {}),
    })),
  });
}

const ids = (tokens: string[]) => tokens.map(transcriptIdOf).sort();
const selectedIds = (selection: FrozenSelection) => [...selection.keys()].sort();
const named = <T extends { name: string }>(cases: T[]) => cases.map((c) => [c.name, c] as const);

describe("live status", () => {
  it.each(fixtures.live_status.map((c) => [String(c.status), c] as const))("%s", (_name, testCase) => {
    expect(isLiveStatus(testCase.status ?? undefined)).toBe(testCase.live);
  });
});

describe("family toggle", () => {
  it.each(named(fixtures.family_toggle))("%s", (_name, testCase) => {
    const family = testCase.rows.map(rowOf);
    const before = selectionOf(testCase.selected);
    expect(familyState(before, family)).toBe(testCase.state_before);
    const after = toggleFamily(before, family);
    expect(selectedIds(after)).toEqual(ids(testCase.expect_selected));
    if (testCase.state_after) expect(familyState(after, family)).toBe(testCase.state_after);
  });
});

describe("loaded project toggle", () => {
  it.each(named(fixtures.loaded_project_toggle))("%s", (_name, testCase) => {
    const rows = testCase.rows.map(rowOf);
    const project = projectHashOf(testCase.project);
    const before = selectionOf(testCase.selected);
    expect(loadedProjectState(before, rows, project)).toBe(testCase.state_before);
    expect(selectedIds(toggleLoadedProject(before, rows, project))).toEqual(ids(testCase.expect_selected));
  });
});

describe("snapshot merge", () => {
  it.each(named(fixtures.snapshot_merge))("%s", (_name, testCase) => {
    const before = selectionOf(testCase.selected);
    const merge = mergeSnapshot(before, snapshotOf(testCase.snapshot));
    const want = testCase.expect;
    expect(selectedIds(merge.selection)).toEqual(ids(want.selected));
    expect([...merge.selectableIds].sort()).toEqual(ids(want.selectable));
    expect(merge.added).toBe(want.added);
    expect(merge.locked).toBe(want.locked);
    expect([...merge.omitted].sort()).toEqual(ids(want.omitted));
    expect(allSelected(merge.selection, merge.selectableIds)).toBe(want.all_selected);
    for (const record of want.records ?? []) {
      const got = merge.selection.get(transcriptIdOf(record.id));
      expect(got?.project_hash).toBe(projectHashOf(record.project));
      expect(got?.family_project_hash).toBe(projectHashOf(record.family));
    }
    // The merge never mutates the selection it was given.
    expect(selectedIds(before)).toEqual(ids(testCase.selected.map((s) => s.id)));
  });
});

describe("rows that became live", () => {
  it.each(named(fixtures.without_live))("%s", (_name, testCase) => {
    const result = withoutLive(selectionOf(testCase.selected), testCase.rows.map(rowOf));
    expect([...result.becameLive].sort()).toEqual(ids(testCase.expect_became_live));
    expect(selectedIds(result.selection)).toEqual(ids(testCase.expect_selected));
  });
});

describe("picker counts and submission projects", () => {
  it.each(named(fixtures.projects))("%s", (_name, testCase) => {
    const selection = selectionOf(testCase.selected);
    for (const [project, count] of Object.entries(testCase.expect_counted)) {
      expect(selectedInProject(selection, projectHashOf(project))).toBe(count);
    }
    const submitted = bySubmissionProject(selection);
    expect([...submitted.keys()].sort()).toEqual(Object.keys(testCase.expect_submitted).map(projectHashOf).sort());
    for (const [project, members] of Object.entries(testCase.expect_submitted)) {
      expect((submitted.get(projectHashOf(project)) ?? []).map((record) => record.id).sort()).toEqual(ids(members));
    }
  });
});
