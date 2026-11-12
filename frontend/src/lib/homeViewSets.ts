import { closed } from "@peasant-labs/fairtrade/closed-value";

/**
 * The closed sets and names of the Library view: the groupings, the columns,
 * the storage key and the URL parameter names.
 *
 * This module imports nothing from the app, so the visual scripts under
 * `scripts/visual/` import it directly under Node and read the same values the
 * page does. `homeView.ts` re-exports every member; app code imports from there.
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

/** The stored view this browser writes. */
export const HOME_VIEW_STORAGE_KEY = "peasant-online.library";

/** The URL parameter that carries the grouping. */
export const GROUP_BY_URL_PARAM = "groupBy";

/** The URL parameter names this codec owns. `page`, `perPage`, and `groupBy`
 *  are the complete set: the columns and the closed groups never enter a URL,
 *  so a copied URL reproduces the data view and grouping, not the sender's
 *  columns.
 *
 *  The address calls the page size `perPage`, the vocabulary the plan and the
 *  Peasant home use; the wire calls the same value `per_page`. This codec is
 *  the one place the two names meet. */
export const LIBRARY_URL_PARAMS = ["page", "perPage", GROUP_BY_URL_PARAM] as const;
