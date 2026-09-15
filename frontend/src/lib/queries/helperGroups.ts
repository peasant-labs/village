"use client";

import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  zVillageHelperMembersPayload,
  zVillageSessionListPayload,
  type HelperGroupSummary,
} from "@peasant-labs/schema";
import { useAuth } from "@/providers/AuthProvider";
import { api, isApiErrorStatus } from "../api";

function assertPageMatches(page: number, limit: number, response: { page: number; limit: number }) {
  if (response.page !== page || response.limit !== limit) {
    throw new Error(
      "Village grouped-list fetch rejected mismatched pagination before caching the response; " +
      "the server returned a different page or limit, so these rows cannot represent your request; " +
      "retry the originating list and check the deployed API if this persists.",
    );
  }
}

/**
 * The global/profile/project query uses the server's complete grouped page, not
 * a client fold over a flat page. Owner/project inputs must be settled before
 * enabling the query; an empty owner is global discovery, not an owner lookup.
 */
export function useGroupedTranscripts(
  params: Record<string, string>,
  options: { enabled?: boolean } = {},
) {
  const { user, isLoading } = useAuth();
  const client = useQueryClient();
  const queryParams = { ...params, view: "grouped", page: params.page ?? "1", limit: params.limit ?? "20" };
  const queryKey = ["transcripts", "grouped", user?.id ?? "anonymous", queryParams] as const;
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      const value = await api<unknown>(`/transcripts?${new URLSearchParams(queryParams)}`, { signal });
      const response = zVillageSessionListPayload.parse(value);
      assertPageMatches(Number(queryParams.page), Number(queryParams.limit), response);
      return response;
    },
    // Never borrow rows or member scopes from another viewer/filter/page.
    enabled: !isLoading && (options.enabled ?? true),
    retry: false,
    staleTime: 0,
  });
  return {
    ...query,
    // Refresh is explicitly initiated by the host's recovery control. An
    // expired expansion must not automatically broaden, reopen or select rows.
    refreshOrigin: () => client.invalidateQueries({ queryKey, exact: true }),
  };
}

/**
 * The grouped top-level unit count one server page may carry.
 *
 * The list endpoint caps `limit` at 100, so a mount that reads ONE page covers
 * only the first 100 grouped top-level units. A later owner or helper-only
 * context container on such a surface would silently lose its grouped exit.
 */
export const GROUPED_TOP_LEVEL_PAGE_SIZE = 100;

/**
 * The global/profile/project grouped read, with a reachable continuation.
 *
 * A browse surface that renders more than `GROUPED_TOP_LEVEL_PAGE_SIZE` grouped
 * units needs the later pages too, or the owners and helper-only context
 * containers on them never get their grouped exits. This reads page 1 and then
 * offers `fetchNextPage`/`hasNextPage`, so the continuation is explicit and
 * every request after the first is bounded by what the server reports.
 *
 * The flat list above these mounts stays the authority for its own rows, pages
 * and ordinary child folds; this hook only supplements it. Each page is
 * validated against its own requested page/limit before it can become cache
 * data, and the whole set shares one query key, so a re-minted scope's refresh
 * invalidates the continuation rather than leaving a stale later page behind.
 */
export function useGroupedTranscriptsPaged(
  params: Record<string, string>,
  options: { enabled?: boolean } = {},
) {
  const { user, isLoading } = useAuth();
  const client = useQueryClient();
  const limit = Number(params.limit ?? GROUPED_TOP_LEVEL_PAGE_SIZE);
  const filterParams = { ...params, view: "grouped", limit: String(limit) };
  const queryKey = ["transcripts", "grouped-paged", user?.id ?? "anonymous", filterParams] as const;
  const query = useInfiniteQuery({
    queryKey,
    queryFn: async ({ pageParam, signal }) => {
      const requestParams = { ...filterParams, page: String(pageParam) };
      const value = await api<unknown>(`/transcripts?${new URLSearchParams(requestParams)}`, { signal });
      const response = zVillageSessionListPayload.parse(value);
      assertPageMatches(pageParam, limit, response);
      return response;
    },
    initialPageParam: 1,
    // The server reports how many grouped top-level units the SAME filters
    // select, so "is there another page" is its answer, never a client count.
    getNextPageParam: (last) =>
      last.page * last.limit < last.totalItems ? last.page + 1 : undefined,
    // Never borrow rows or member scopes from another viewer/filter/page.
    enabled: !isLoading && (options.enabled ?? true),
    retry: false,
    staleTime: 0,
  });
  const items = query.data?.pages.flatMap((page) => page.items) ?? [];
  const totalItems = query.data?.pages[0]?.totalItems ?? 0;
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

/**
 * Shared by browse and collective hosts: membership comes only from the opaque
 * scope on a server summary. The originating filters are deliberately not an
 * argument, so no caller can add search/project/collective filters to this URL.
 * Members are transcript-only display items: key by item.transcript.session.id
 * and render item.helperGroups as independent nested disclosures. The row and
 * its applicable collective/review arm remain under item.transcript.
 */
export function useHelperGroupMembers(
  group: HelperGroupSummary | undefined,
  options: { expanded: boolean; page?: number; limit?: number },
) {
  const { user, isLoading } = useAuth();
  const page = options.page ?? 1;
  const limit = options.limit ?? 20;
  const query = useQuery({
    queryKey: ["transcripts", "helper-members", user?.id ?? "anonymous", group?.groupId, group?.memberScope, page, limit],
    queryFn: async ({ signal }) => {
      if (!group?.memberScope || !group.groupId) {
        throw new Error("Village helper expansion has no original member scope; no request was sent; refresh the originating list and expand its returned helper group.");
      }
      const params = new URLSearchParams({ scope: group.memberScope, page: String(page), limit: String(limit) });
      const value = await api<unknown>(`/transcript-groups/${encodeURIComponent(group.groupId)}/members?${params}`, { signal });
      const response = zVillageHelperMembersPayload.parse(value);
      assertPageMatches(page, limit, response);
      return response;
    },
    enabled: !isLoading && options.expanded && !!group?.memberScope,
    retry: false,
    staleTime: 0,
    gcTime: 0,
  });
  return {
    ...query,
    // Failed current authorization or scope replay must not leave formerly
    // eligible members available for selection from TanStack's retained data.
    data: query.isError ? undefined : query.data,
    refreshRequired: isApiErrorStatus(query.error, 409),
  };
}
