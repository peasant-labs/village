import { HIDDEN_PROJECT_LABEL } from "@/lib/format";
import { isHarness } from "@/lib/harness";
import type { TranscriptListItem } from "@/lib/types";

/**
 * How the home page's transcript table is arranged in THIS browser: how rows are
 * grouped, which columns show, and which groups are open. It changes what is
 * shown, never what the server holds, and it is remembered on this device only.
 */

export const GROUP_BYS = ["project", "collective", "agent", "none"] as const;
export type GroupBy = (typeof GROUP_BYS)[number];

export const GROUP_BY_LABELS: Record<GroupBy, string> = {
  project: "project",
  collective: "collective",
  agent: "agent",
  none: "nothing",
};

/** The table's optional columns, in the order they are drawn. The title always shows. */
export const HOME_COLUMNS = ["project", "readers", "agent", "pullRequests", "turns", "when"] as const;
export type HomeColumn = (typeof HOME_COLUMNS)[number];

export const HOME_COLUMN_LABELS: Record<HomeColumn, string> = {
  project: "project",
  readers: "who can read",
  agent: "agent",
  pullRequests: "pull requests",
  turns: "turns",
  when: "when",
};

export interface HomeView {
  groupBy: GroupBy;
  columns: Record<HomeColumn, boolean>;
  /** The groups a person opened, keyed `${groupBy}:${group key}`. */
  open: string[];
}

export const DEFAULT_HOME_VIEW: HomeView = {
  groupBy: "project",
  columns: { project: true, readers: true, agent: true, pullRequests: false, turns: false, when: true },
  open: [],
};

export const HOME_VIEW_STORAGE_KEY = "peasant-online.home-view";

/** Reads the remembered view, falling back to the default for anything unreadable. */
export function loadHomeView(): HomeView {
  let raw: string | null = null;
  try {
    raw = globalThis.localStorage?.getItem(HOME_VIEW_STORAGE_KEY) ?? null;
  } catch {
    return DEFAULT_HOME_VIEW;
  }
  if (!raw) return DEFAULT_HOME_VIEW;
  try {
    const parsed = JSON.parse(raw) as Partial<HomeView>;
    const groupBy = GROUP_BYS.includes(parsed.groupBy as GroupBy) ? (parsed.groupBy as GroupBy) : DEFAULT_HOME_VIEW.groupBy;
    const columns = { ...DEFAULT_HOME_VIEW.columns };
    for (const key of HOME_COLUMNS) {
      const value = parsed.columns?.[key];
      if (typeof value === "boolean") columns[key] = value;
    }
    const open = Array.isArray(parsed.open) ? parsed.open.filter((k): k is string => typeof k === "string") : [];
    return { groupBy, columns, open };
  } catch {
    return DEFAULT_HOME_VIEW;
  }
}

/** Remembers the view on this device. Best effort: without storage it lasts the visit. */
export function saveHomeView(view: HomeView): void {
  try {
    globalThis.localStorage?.setItem(HOME_VIEW_STORAGE_KEY, JSON.stringify(view));
  } catch {
    // Storage is a convenience; the view still applies until the page is left.
  }
}

/** A project a loaded row belongs to: its identity, and the name to show. */
export interface ProjectOption {
  key: string;
  label: string;
  count: number;
}

/** The project a row is filed under. A row with no project identity keeps its name as its key. */
export function projectKey(item: TranscriptListItem): string {
  return item.transcript.project_hash || `name:${item.transcript.project_display_name ?? ""}`;
}

/** The heading a row's project is filed under. A withheld project (null) has
 *  no name to show, so the heading says so instead. */
export function projectLabel(item: TranscriptListItem): string {
  return item.transcript.project_display_name ?? HIDDEN_PROJECT_LABEL;
}

/** The projects among the rows, most recent first, with how many rows each holds. */
export function projectOptions(rows: TranscriptListItem[]): ProjectOption[] {
  const byKey = new Map<string, ProjectOption>();
  for (const item of rows) {
    const key = projectKey(item);
    const known = byKey.get(key);
    if (known) known.count += 1;
    else byKey.set(key, { key, label: projectLabel(item), count: 1 });
  }
  return [...byKey.values()];
}

/** The collectives that may read a row, as the list row states them. */
export function readersOf(item: TranscriptListItem): string[] {
  const names = [...new Set((item.shares ?? []).map((share) => share.group_name))];
  if (item.transcript.visibility === "public") return ["anyone with the link", ...names];
  if (item.transcript.visibility === "shared") return names;
  return [];
}

/** What a row with no named readers says: approved collectives are the only readers a row names. */
export const NO_READERS = "not shared with collective members";

/** The agent a row was recorded with, by the harness name fairtrade's
 *  `ProviderName` prints beside its mark. */
export function agentLabel(item: TranscriptListItem): string {
  const provider = item.transcript.model_provider;
  if (isHarness(provider)) return provider;
  return provider?.trim() || "unknown agent";
}

export interface HomeGroup {
  /** `${groupBy}:${key}`, stable across renders and visits. */
  id: string;
  label: string;
  rows: TranscriptListItem[];
}

/**
 * The rows in groups, in the order the rows arrive (most recent first), so a
 * group sits where its newest row would. Grouping by collective files a row
 * under every collective that reads it; a row no collective reads goes under
 * the same words its readers cell uses.
 */
export function groupRows(rows: TranscriptListItem[], groupBy: GroupBy): HomeGroup[] {
  if (groupBy === "none") return [{ id: "none:", label: "", rows }];
  const groups = new Map<string, HomeGroup>();
  const file = (key: string, label: string, item: TranscriptListItem) => {
    const id = `${groupBy}:${key}`;
    const group = groups.get(id);
    if (group) group.rows.push(item);
    else groups.set(id, { id, label, rows: [item] });
  };
  for (const item of rows) {
    if (groupBy === "project") {
      file(projectKey(item), projectLabel(item), item);
    } else if (groupBy === "agent") {
      const label = agentLabel(item);
      file(item.transcript.model_provider || label, label, item);
    } else {
      const readers = readersOf(item);
      if (readers.length === 0) file(NO_READERS, NO_READERS, item);
      for (const name of readers) file(name, name, item);
    }
  }
  return [...groups.values()];
}
