import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import {
  zVillageGroupedContributableResponse,
  zVillageGroupedGroupDetailResponse,
  zVillageSessionListPayload,
  type VillageSessionListItem,
} from "@peasant-labs/schema";
import { useAuth } from "@/providers/AuthProvider";
import { api } from "../api";

/** Parsed filters owned by the collective route, not by a helper disclosure. */
export interface CollectiveListFilters {
  page?: number;
  limit?: number;
  q?: string;
  project_hash?: string;
}

function listQuery(filters: CollectiveListFilters): string {
  const query = new URLSearchParams({ view: "grouped", page: String(filters.page ?? 1), limit: String(filters.limit ?? 20) });
  if (filters.q) query.set("q", filters.q);
  if (filters.project_hash) query.set("project_hash", filters.project_hash);
  return query.toString();
}

// The decoder is the published canonical validator, not a host wire model.
// Existing mutation-key prefixes remain valid, with viewer and filter identity
// below them so an account/filter change cannot borrow another list's scopes.

/** The fields every collective grouped page shares, whatever its route arm carries. */
interface CollectiveGroupedPage {
  items: VillageSessionListItem[];
  page: number;
  limit: number;
  totalItems: number;
}

/**
 * One collective grouped read, with a reachable continuation.
 *
 * A grouped page holds at most the list endpoint's own `limit` of grouped
 * top-level units, and the collective's flat list stays the authority for its
 * own rows and pages. Without a continuation the later grouped owners and
 * helper-only context containers would never get their grouped exits, so these
 * read page 1 and then offer `fetchNextPage`/`hasNextPage` from the server's own
 * `totalItems` -- the same continuation the profile/project grouped exits use.
 *
 * Each page is validated against the page/limit it asked for before it can
 * become cache data, and the whole set shares one query key, so a re-minted
 * scope's refresh invalidates the continuation rather than leaving a stale later
 * page behind. Flat and grouped item units differ, so the grouped page is never
 * paired to a flat offset; the server's count alone decides the next page.
 */
function useCollectiveReadPaged<T>(
  key: string,
  groupId: string,
  suffix: string,
  filters: CollectiveListFilters,
  enabled: boolean,
  decode: (value: unknown) => T,
  pageOf: (value: T) => CollectiveGroupedPage,
) {
  const { user, isLoading } = useAuth();
  const client = useQueryClient();
  const limit = filters.limit ?? 20;
  const filterParams = { ...filters, view: "grouped", limit };
  const queryKey = [key, groupId, "grouped-paged", user?.id ?? "anonymous", filterParams] as const;
  const query = useInfiniteQuery({
    queryKey,
    queryFn: async ({ pageParam, signal }) => {
      const requestFilters = { ...filters, page: pageParam, limit };
      const raw = await api<unknown>(`/groups/${encodeURIComponent(groupId)}${suffix}?${listQuery(requestFilters)}`, { signal });
      const response = decode(raw);
      const page = pageOf(response);
      if (page.page !== pageParam || page.limit !== limit) {
        throw new Error("collective grouped read refused before caching: the response pagination differs from the requested scope; no selectable rows were retained; refresh the originating collective list");
      }
      return response;
    },
    initialPageParam: 1,
    // The server reports how many grouped top-level units the SAME filters
    // select, so "is there another page" is its answer, never a client count.
    getNextPageParam: (last) => {
      const page = pageOf(last);
      return page.page * page.limit < page.totalItems ? page.page + 1 : undefined;
    },
    enabled: !isLoading && !!groupId && enabled,
    retry: false,
    staleTime: 0,
  });
  const items = query.data?.pages.flatMap((page) => pageOf(page).items) ?? [];
  const totalItems = query.data?.pages[0] ? pageOf(query.data.pages[0]).totalItems : 0;
  return {
    ...query,
    items,
    totalItems,
    // What the continuation may offer. It follows the SAME answer the query
    // uses to decide whether another page exists, so a surface can never show a
    // control whose next page the query would not request.
    remainingItems: query.hasNextPage ? Math.max(0, totalItems - items.length) : 0,
    // Refresh is explicitly initiated by the host's recovery control. An
    // expired expansion must not automatically broaden, reopen or select rows.
    refreshOrigin: () => client.invalidateQueries({ queryKey, exact: true }),
  };
}

export function useGroupedCollective(groupId: string, filters: CollectiveListFilters, enabled = true) {
  return useCollectiveReadPaged("group", groupId, "", filters, enabled, (raw) => zVillageGroupedGroupDetailResponse.parse(raw), (response) => response.transcriptList);
}

export function useGroupedContributable(groupId: string, filters: CollectiveListFilters, enabled = true) {
  return useCollectiveReadPaged("group-contributable", groupId, "/contributable", filters, enabled, (raw) => zVillageGroupedContributableResponse.parse(raw), (response) => response.transcriptList);
}

export function useGroupedPendingShares(groupId: string, filters: CollectiveListFilters, enabled: boolean) {
  return useCollectiveReadPaged("group-pending", groupId, "/pending", filters, enabled, (raw) => zVillageSessionListPayload.parse(raw), (response) => response);
}

// One grouped read per collective route variant, each with a reachable
// continuation. The my-shares arm is the same `VillageSessionListPayload` the
// pending arm serves, so it uses the identical paged read: the panel's own flat
// contribution list stays the authority for its rows, and a grouped page holds
// only `limit` top-level units, so the later owner rows would otherwise never
// get their grouped exit.
export function useGroupedMyShares(groupId: string, filters: CollectiveListFilters, enabled = true) {
  return useCollectiveReadPaged("group-my-shares", groupId, "/my-shares", filters, enabled, (raw) => zVillageSessionListPayload.parse(raw), (response) => response);
}
