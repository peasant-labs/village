import {
  zVillageContributionStatus,
  type VillageContributableTranscript,
  type VillageContributionPageItem,
  type VillageContributionSnapshotResponse,
  type VillageShareStatus,
  type VillageTranscriptVisibility,
} from "@peasant-labs/schema";

/**
 * The contribute page's selection: a set of transcript ids, each with the few
 * facts the submission needs, that the person built from loaded rows and from
 * per-project snapshots.
 *
 * The set is FROZEN. Nothing joins it except by an action of the person: a
 * row that starts to match a filter later, or a family that grows a helper
 * after a snapshot, stays out until it is selected. Changing the filters
 * reloads the list and keeps the set as it is.
 *
 * Every record knows two projects. `project_hash` is the transcript's own
 * project, the one a submission names it under. `family_project_hash` is the
 * project of its family's root, the picker row it counts under: a helper in
 * another project rides with its root and is counted there.
 */
export interface SelectedRecord {
  id: string;
  project_hash: string;
  family_project_hash: string;
  visibility: VillageTranscriptVisibility;
  /** The title, when the record was selected from a loaded row. A snapshot
   *  record carries no title. */
  title?: string | null;
}

export type FrozenSelection = ReadonlyMap<string, SelectedRecord>;

export const EMPTY_SELECTION: FrozenSelection = new Map();

/**
 * Whether a latest attempt status is live: an open contribution, pending or
 * approved. The set is the contract's `VillageContributionStatus`, the status a
 * contribution holds while it is open, so this reads the contract's own list.
 * A closed attempt (rejected, retracted, revoked) leaves the row selectable.
 */
export function isLiveStatus(status: VillageShareStatus | undefined): boolean {
  return status !== undefined && zVillageContributionStatus.safeParse(status).success;
}

/** One loaded transcript, with the project of the family it was listed in. */
export interface LoadedRow {
  transcript: VillageContributableTranscript;
  family_project_hash: string;
  root_id: string;
  is_root: boolean;
  matched?: boolean;
}

/** Flattens loaded families into rows, root first, helpers in server order. */
export function loadedRows(items: readonly VillageContributionPageItem[]): LoadedRow[] {
  const rows: LoadedRow[] = [];
  for (const item of items) {
    const family = item.root.transcript.project_hash;
    const rootId = item.root.transcript.id;
    rows.push({ transcript: item.root.transcript, family_project_hash: family, root_id: rootId, is_root: true, matched: item.root.matched });
    for (const helper of item.helpers) {
      rows.push({ transcript: helper.transcript, family_project_hash: family, root_id: rootId, is_root: false, matched: helper.matched });
    }
  }
  return rows;
}

/** A loaded row is locked when the server reports it already shared. */
export function isLockedRow(row: LoadedRow): boolean {
  return row.transcript.already_shared;
}

function recordOf(row: LoadedRow): SelectedRecord {
  return {
    id: row.transcript.id,
    project_hash: row.transcript.project_hash,
    family_project_hash: row.family_project_hash,
    visibility: row.transcript.visibility,
    title: row.transcript.title,
  };
}

/** Adds every unlocked row; a locked row never enters the set. */
export function selectRows(selection: FrozenSelection, rows: readonly LoadedRow[]): FrozenSelection {
  const next = new Map(selection);
  for (const row of rows) if (!isLockedRow(row)) next.set(row.transcript.id, recordOf(row));
  return next;
}

export function deselectIds(selection: FrozenSelection, ids: Iterable<string>): FrozenSelection {
  const next = new Map(selection);
  for (const id of ids) next.delete(id);
  return next;
}

export type CheckState = "none" | "some" | "all";

function stateOf(selection: FrozenSelection, rows: readonly LoadedRow[]): CheckState {
  const selectable = rows.filter((row) => !isLockedRow(row));
  if (selectable.length === 0) return "none";
  const selected = selectable.filter((row) => selection.has(row.transcript.id)).length;
  if (selected === 0) return "none";
  return selected === selectable.length ? "all" : "some";
}

/**
 * Selecting a family takes the root and every helper; each helper can then be
 * deselected on its own. A checked family clears all of it.
 */
export function familyState(selection: FrozenSelection, family: readonly LoadedRow[]): CheckState {
  return stateOf(selection, family);
}

export function toggleFamily(selection: FrozenSelection, family: readonly LoadedRow[]): FrozenSelection {
  return familyState(selection, family) === "all"
    ? deselectIds(selection, family.map((row) => row.transcript.id))
    : selectRows(selection, family);
}

function loadedOfProject(rows: readonly LoadedRow[], projectHash: string): LoadedRow[] {
  return rows.filter((row) => row.family_project_hash === projectHash);
}

/** The project checkbox reads and acts on the LOADED rows of that project only. */
export function loadedProjectState(selection: FrozenSelection, rows: readonly LoadedRow[], projectHash: string): CheckState {
  return stateOf(selection, loadedOfProject(rows, projectHash));
}

/**
 * Toggles the loaded rows of one project: a checked box clears them, any other
 * state selects every loaded unlocked row. It never reaches unloaded rows and
 * never cycles to a wider scope; the snapshot button does that.
 */
export function toggleLoadedProject(selection: FrozenSelection, rows: readonly LoadedRow[], projectHash: string): FrozenSelection {
  const projectRows = loadedOfProject(rows, projectHash);
  return stateOf(selection, projectRows) === "all"
    ? deselectIds(selection, projectRows.map((row) => row.transcript.id))
    : selectRows(selection, projectRows);
}

/** What merging one snapshot did, for the page to say. */
export interface SnapshotMerge {
  selection: FrozenSelection;
  /** The unlocked ids of the snapshot: the exact set "select all" took. */
  selectableIds: string[];
  added: number;
  /** Records the snapshot holds that are live and so were not taken. */
  locked: number;
  /** Ids already selected under this project that the snapshot does not hold.
   *  They stay selected; the page says how many there are. */
  omitted: string[];
}

/**
 * Merges the exact set of one project into the selection in one update. The
 * snapshot's records are the authority, not any count the picker showed, and a
 * previously selected record it omits is reported, never removed.
 */
export function mergeSnapshot(
  selection: FrozenSelection,
  snapshot: VillageContributionSnapshotResponse,
  titles: ReadonlyMap<string, string | null> = new Map(),
): SnapshotMerge {
  const next = new Map(selection);
  const inSnapshot = new Set<string>();
  const selectableIds: string[] = [];
  let added = 0;
  let locked = 0;
  for (const record of snapshot.records) {
    inSnapshot.add(record.id);
    if (isLiveStatus(record.latest_status)) {
      locked += 1;
      continue;
    }
    selectableIds.push(record.id);
    if (next.has(record.id)) continue;
    next.set(record.id, {
      id: record.id,
      project_hash: record.project_hash,
      family_project_hash: snapshot.project_hash,
      visibility: record.visibility,
      ...(titles.has(record.id) ? { title: titles.get(record.id) } : {}),
    });
    added += 1;
  }
  const omitted = [...selection.values()]
    .filter((record) => record.family_project_hash === snapshot.project_hash && !inSnapshot.has(record.id))
    .map((record) => record.id);
  return { selection: next, selectableIds, added, locked, omitted };
}

/** Whether every id a snapshot offered is selected: the button then clears. */
export function allSelected(selection: FrozenSelection, ids: readonly string[]): boolean {
  return ids.length > 0 && ids.every((id) => selection.has(id));
}

/**
 * The selection without the ids a loaded row now reports as live. A selected
 * row that became live leaves the selected count and joins the locked count;
 * it is never submitted.
 */
export function withoutLive(selection: FrozenSelection, rows: readonly LoadedRow[]): { selection: FrozenSelection; becameLive: string[] } {
  const becameLive = rows.filter((row) => isLockedRow(row) && selection.has(row.transcript.id)).map((row) => row.transcript.id);
  return becameLive.length === 0 ? { selection, becameLive } : { selection: deselectIds(selection, becameLive), becameLive };
}

/** How many selected records count under one picker project. */
export function selectedInProject(selection: FrozenSelection, projectHash: string): number {
  let count = 0;
  for (const record of selection.values()) if (record.family_project_hash === projectHash) count += 1;
  return count;
}

/** The selection by the project each record is submitted under. */
export function bySubmissionProject(selection: FrozenSelection): Map<string, SelectedRecord[]> {
  const grouped = new Map<string, SelectedRecord[]>();
  for (const record of selection.values()) {
    const bucket = grouped.get(record.project_hash);
    if (bucket) bucket.push(record);
    else grouped.set(record.project_hash, [record]);
  }
  return grouped;
}
