import { closed } from "@peasant-labs/fairtrade/closed-value";
import { PageSize, isPageSize } from "@peasant-labs/schema";
import { HIDDEN_PROJECT_LABEL } from "@/lib/format";
import { isHarness } from "@/lib/harness";
import type { TranscriptListItem } from "@/lib/types";

/**
 * How the Library is arranged: the URL owns the grouping and the page, the
 * device owns the columns shown and the groups a reader closed. It changes what
 * is shown, never what the server holds.
 *
 * The grouping and the column identifiers are closed sets. A stored or URL value
 * is mapped back to the canonical member before it is compared or passed on, so
 * no consumer compares wires or raw strings.
 */

const GroupByValues = {
  Project: closed("project", "project"),
  Collective: closed("collective", "collective"),
  Agent: closed("agent", "agent"),
  None: closed("none", "nothing"),
} as const;

/** A grouping the shared table offers. */
export type GroupBy = (typeof GroupByValues)[keyof typeof GroupByValues];
export const GroupBy = GroupByValues;

/** Every grouping, in the order the picker offers them. */
export const ALL_GROUP_BYS = Object.freeze([
  GroupByValues.Project,
  GroupByValues.Collective,
  GroupByValues.Agent,
  GroupByValues.None,
] as const);

const ColumnIdValues = {
  Transcript: closed("transcript", "transcript"),
  Project: closed("project", "project"),
  Readers: closed("readers", "who can read"),
  Agent: closed("agent", "agent"),
  PullRequests: closed("pullRequests", "pull requests"),
  Turns: closed("turns", "turns"),
  When: closed("when", "when"),
} as const;

/** One host column the Library can show. `Transcript` is the row identity and
 *  is declared first; the shared table always draws the first declared column. */
export type ColumnId = (typeof ColumnIdValues)[keyof typeof ColumnIdValues];
export const ColumnId = ColumnIdValues;

/** Every column, in the order they are drawn. */
export const ALL_VILLAGE_COLUMNS = Object.freeze([
  ColumnIdValues.Transcript,
  ColumnIdValues.Project,
  ColumnIdValues.Readers,
  ColumnIdValues.Agent,
  ColumnIdValues.PullRequests,
  ColumnIdValues.Turns,
  ColumnIdValues.When,
] as const);

/** The view this browser remembers: the columns shown and the groups closed.
 *  The grouping and the page come from the URL, never from storage. */
export interface HomeView {
  columns: readonly ColumnId[];
  /** The qualified group ids (`${groupBy.wire}:${key}`) the reader closed. An
   *  empty list opens every group, as the shared table defines it. */
  collapsed: string[];
}

export const DEFAULT_HOME_VIEW: HomeView = {
  columns: [
    ColumnIdValues.Transcript,
    ColumnIdValues.Project,
    ColumnIdValues.Readers,
    ColumnIdValues.Agent,
    ColumnIdValues.When,
  ],
  collapsed: [],
};

/** The stored view this browser writes. */
export const HOME_VIEW_STORAGE_KEY = "peasant-online.library";
/** The key an earlier release wrote. Read once, migrated, never written again. */
export const LEGACY_HOME_VIEW_STORAGE_KEY = "peasant-online.home-view";

/** The URL half of the Library view: page, page size, and grouping. */
export interface LibraryUrlView {
  page: number;
  perPage: PageSize;
  groupBy: GroupBy;
}

export const DEFAULT_LIBRARY_URL_VIEW: LibraryUrlView = {
  page: 1,
  perPage: PageSize.P25,
  groupBy: GroupBy.Project,
};

/** The URL parameter names this codec owns. `page`, `per_page`, and `groupBy`
 *  are the complete set: the columns and the closed groups never enter a URL,
 *  so a copied URL reproduces the data view and grouping, not the sender's
 *  columns. */
export const LIBRARY_URL_PARAMS = ["page", "per_page", "groupBy"] as const;

/**
 * The columns a stored value names, or the default when it names none.
 *
 * Two shipped shapes migrate:
 *   - a `columns` record keyed by the old column names, keeping the keys whose
 *     value is `true`, ordered by {@link ALL_VILLAGE_COLUMNS};
 *   - a `visibleColumns` array, validating each member against the closed set.
 *
 * An unknown member is dropped with one warning; a readable record or array is
 * never reset to the default. Both paths end in the same ordered list.
 */
export function migrateStoredView(raw: unknown): readonly ColumnId[] {
  if (raw == null || typeof raw !== "object") return DEFAULT_HOME_VIEW.columns;
  const record = raw as { columns?: unknown; visibleColumns?: unknown };
  let dropped: string[] = [];
  let migrated: readonly ColumnId[] | null = null;

  const migrateArray = (values: readonly unknown[]): readonly ColumnId[] => {
    const members: ColumnId[] = [];
    for (const value of values) {
      const member = ALL_VILLAGE_COLUMNS.find((column) => column.wire === value);
      if (member !== undefined) {
        if (!members.includes(member)) members.push(member);
      } else if (typeof value === "string") {
        dropped.push(value);
      }
    }
    return ALL_VILLAGE_COLUMNS.filter((column) => members.includes(column));
  };

  if (Array.isArray(record.columns)) {
    // The current stored shape: the columns as closed-set wires.
    migrated = migrateArray(record.columns);
  } else if (record.columns !== undefined && record.columns !== null && typeof record.columns === "object") {
    const stored = record.columns as Record<string, unknown>;
    // The old record never named the title column: it always drew. The migrated
    // list states it explicitly, because the closed set now carries it.
    migrated = ALL_VILLAGE_COLUMNS.filter(
      (column) => column === ColumnIdValues.Transcript || stored[column.wire] === true,
    );
    dropped = Object.keys(stored).filter((key) => !ALL_VILLAGE_COLUMNS.some((column) => column.wire === key));
  } else if (Array.isArray(record.visibleColumns)) {
    migrated = migrateArray(record.visibleColumns);
  }

  if (migrated === null) return DEFAULT_HOME_VIEW.columns;
  if (dropped.length > 0) {
    console.warn(
      `the saved library view named ${dropped.join(", ")}, which ${dropped.length === 1 ? "is" : "are"} no longer a column; dropped ${dropped.length === 1 ? "it" : "them"} and kept the saved order`,
    );
  }
  return migrated;
}

/** Reads the remembered view from this device, migrating the earlier key on
 *  first read. Anything unreadable falls back to the default. */
export function loadHomeView(): HomeView {
  const storage = globalThis.localStorage;
  if (storage == null) return DEFAULT_HOME_VIEW;

  let raw: string | null = null;
  try {
    raw = storage.getItem(HOME_VIEW_STORAGE_KEY);
    if (raw == null) raw = storage.getItem(LEGACY_HOME_VIEW_STORAGE_KEY);
  } catch {
    return DEFAULT_HOME_VIEW;
  }
  if (raw == null) return DEFAULT_HOME_VIEW;

  try {
    const parsed: unknown = JSON.parse(raw);
    const columns = migrateStoredView(parsed);
    const stored = parsed as { open?: unknown; collapsed?: unknown };
    // `open` is the earlier field name; both name qualified group ids, and the
    // table's `collapsed` is the closed one. Read either, write `collapsed`.
    const collapsedValue = stored.collapsed ?? stored.open;
    const collapsed = Array.isArray(collapsedValue)
      ? collapsedValue.filter((key): key is string => typeof key === "string")
      : [];
    const view: HomeView = { columns, collapsed };
    // Write the canonical shape back on every successful read. That is what
    // completes the earlier-key migration and what normalizes a stored
    // `visibleColumns` array or a legacy `columns` record, so the next read
    // needs no migration at all.
    saveHomeView(view);
    return view;
  } catch {
    return DEFAULT_HOME_VIEW;
  }
}

/** Remembers the view on this device. Best effort: without storage it lasts the
 *  visit. */
export function saveHomeView(view: HomeView): void {
  try {
    globalThis.localStorage?.setItem(HOME_VIEW_STORAGE_KEY, JSON.stringify(view));
  } catch {
    // Storage is a convenience; the view still applies until the page is left.
  }
}

function positiveInteger(raw: string | null): number | null {
  if (raw === null || raw.trim() === "") return null;
  const value = Number(raw);
  return Number.isSafeInteger(value) && value > 0 ? value : null;
}

/** Reads the URL half of the view. An unreadable page falls back to one, an
 *  out-of-set page size to the 25-row default, and an unknown grouping to
 *  project, so a hand-built URL still asks for a page the server serves. */
export function decodeLibraryView(params: URLSearchParams): LibraryUrlView {
  const page = positiveInteger(params.get("page")) ?? DEFAULT_LIBRARY_URL_VIEW.page;
  const perPageValue = Number(params.get("per_page"));
  const perPage = isPageSize(perPageValue) ? perPageValue : DEFAULT_LIBRARY_URL_VIEW.perPage;
  const groupByWire = params.get("groupBy");
  const groupBy = ALL_GROUP_BYS.find((member) => member.wire === groupByWire) ?? DEFAULT_LIBRARY_URL_VIEW.groupBy;
  return { page, perPage, groupBy };
}

/** Renders the URL half of the view as search parameters. The device half is
 *  deliberately absent. */
export function encodeLibraryView(view: LibraryUrlView): URLSearchParams {
  const params = new URLSearchParams();
  params.set("page", String(view.page));
  params.set("per_page", String(view.perPage));
  params.set("groupBy", view.groupBy.wire);
  return params;
}

/** Rewrites the current URL with the URL half of the view, leaving every other
 *  parameter and the history entry alone. */
export function writeLibraryViewToUrl(view: LibraryUrlView): void {
  if (typeof window === "undefined") return;
  const next = encodeLibraryView(view);
  const params = new URLSearchParams(window.location.search);
  for (const name of LIBRARY_URL_PARAMS) params.delete(name);
  for (const [name, value] of next) params.set(name, value);
  const query = params.toString();
  const url = `${window.location.pathname}${query === "" ? "" : `?${query}`}${window.location.hash}`;
  window.history.replaceState(window.history.state, "", url);
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

/**
 * A row's bare group keys under one grouping. The shared table forms
 * `${groupBy.wire}:${key}` from each key and passes that qualified id to
 * {@link groupLabelOf}; it never parses the id back.
 *
 * An ungrouped row returns no key: the table renders an id-less bucket for it.
 * Grouping by collective files a row under every collective that reads it; a
 * row no collective reads goes under the same words its readers cell uses. The
 * project key stays hash-based, so two projects with one display name stay
 * separate.
 */
export function groupKeysOf(row: TranscriptListItem, groupBy: GroupBy): readonly string[] {
  if (groupBy === GroupBy.None) return [];
  if (groupBy === GroupBy.Project) return [projectKey(row)];
  if (groupBy === GroupBy.Agent) return [row.transcript.model_provider?.trim() || agentLabel(row)];
  const readers = readersOf(row);
  return readers.length > 0 ? readers : [NO_READERS];
}

/**
 * The heading for one qualified group id, as the table reports it.
 *
 * `labels` maps a qualified id to the display text the host resolved (a project
 * hash to its display name, say). The map is additive: a group the host has no
 * label for is named by its own bare key.
 */
export function groupLabelOf(
  id: string,
  groupBy: GroupBy,
  labels: ReadonlyMap<string, string> = new Map(),
): string {
  const prefix = `${groupBy.wire}:`;
  const key = id.startsWith(prefix) ? id.slice(prefix.length) : id;
  return labels.get(id) ?? key;
}
