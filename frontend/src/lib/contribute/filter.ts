/** The facts the search + harness filter reads from ONE row. Both the
 *  contribute list and the review queue state them, so the filter is written
 *  once rather than once per list. `title` falls back to `id` so a search
 *  never hides an untitled row a viewer typed its short id for. */
export interface FilterableRow {
  id: string;
  title: string | null;
  model_provider: string;
}

/** The search + harness facet state the contribute page's filter bar drives.
 *  `harness` is `null` for "every harness"; `search` matches case-insensitively
 *  against a row's title (falling back to its id when untitled, so a search
 *  never hides an untitled row a viewer typed its short id for). */
export interface ContributeFilters {
  search: string;
  harness: string | null;
}

function matchesSearch(row: FilterableRow, search: string): boolean {
  const needle = search.trim().toLowerCase();
  if (needle === "") return true;
  const haystack = (row.title ?? row.id).toLowerCase();
  return haystack.includes(needle);
}

/** Narrows `rows` to those matching BOTH the search text and the selected
 *  harness (when set). Applied before the tree is built, so a filtered-out
 *  row's branch/project also disappears once it has nothing left under it. */
export function applyFilters<Row extends FilterableRow>(
  rows: Row[],
  filters: ContributeFilters,
): Row[] {
  return rows.filter(
    (row) =>
      matchesSearch(row, filters.search) &&
      (filters.harness == null || row.model_provider === filters.harness),
  );
}

/** Per-harness row counts AFTER the search text narrows `rows` — so a facet
 *  count reflects what a click would actually reveal, not the pre-search
 *  total. Keyed by the raw `model_provider` wire value. */
export function harnessCounts(
  rows: FilterableRow[],
  search: string,
): Map<string, number> {
  const searched = applyFilters(rows, { search, harness: null });
  const counts = new Map<string, number>();
  for (const row of searched) {
    counts.set(row.model_provider, (counts.get(row.model_provider) ?? 0) + 1);
  }
  return counts;
}
