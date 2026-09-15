import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  zVillageGroupedContributableResponse,
  zVillageGroupedGroupDetailResponse,
  zVillageSessionListPayload,
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
function useCollectiveRead<T>(key: string, groupId: string, suffix: string, filters: CollectiveListFilters, enabled: boolean, decode: (value: unknown) => T, pageOf: (value: T) => { page: number; limit: number }) {
  const { user, isLoading } = useAuth();
  const client = useQueryClient();
  const queryKey = [key, groupId, "grouped", user?.id ?? "anonymous", filters] as const;
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      const raw = await api<unknown>(`/groups/${encodeURIComponent(groupId)}${suffix}?${listQuery(filters)}`, { signal });
      const response = decode(raw);
      const page = pageOf(response);
      if (page.page !== (filters.page ?? 1) || page.limit !== (filters.limit ?? 20)) {
        throw new Error("collective grouped read refused before caching: the response pagination differs from the requested scope; no selectable rows were retained; refresh the originating collective list");
      }
      return response;
    },
    enabled: !isLoading && !!groupId && enabled,
    retry: false,
    staleTime: 0,
  });
  return { ...query, data: query.isError ? undefined : query.data, refreshOrigin: () => client.invalidateQueries({ queryKey, exact: true }) };
}

export function useGroupedCollective(groupId: string, filters: CollectiveListFilters, enabled = true) {
  return useCollectiveRead("group", groupId, "", filters, enabled, (raw) => zVillageGroupedGroupDetailResponse.parse(raw), (response) => response.transcriptList);
}

export function useGroupedContributable(groupId: string, filters: CollectiveListFilters, enabled = true) {
  return useCollectiveRead("group-contributable", groupId, "/contributable", filters, enabled, (raw) => zVillageGroupedContributableResponse.parse(raw), (response) => response.transcriptList);
}

export function useGroupedPendingShares(groupId: string, filters: CollectiveListFilters, enabled: boolean) {
  return useCollectiveRead("group-pending", groupId, "/pending", filters, enabled, (raw) => zVillageSessionListPayload.parse(raw), (response) => response);
}

export function useGroupedMyShares(groupId: string, filters: CollectiveListFilters, enabled = true) {
  return useCollectiveRead("group-my-shares", groupId, "/my-shares", filters, enabled, (raw) => zVillageSessionListPayload.parse(raw), (response) => response);
}
