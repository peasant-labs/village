import { useQuery } from "@tanstack/react-query";
import type {
  VillageGroupedContributableResponse,
  VillageGroupedGroupDetailResponse,
  VillageSessionListPayload,
} from "@peasant-labs/schema";
import { api } from "../api";

/** Parsed filters owned by the collective route, not by a helper disclosure. */
export interface CollectiveListFilters {
  page?: number;
  limit?: number;
  q?: string;
  project_hash?: string;
}

function listQuery(filters: CollectiveListFilters): string {
  const query = new URLSearchParams({ view: "grouped" });
  if (filters.page !== undefined) query.set("page", String(filters.page));
  if (filters.limit !== undefined) query.set("limit", String(filters.limit));
  if (filters.q) query.set("q", filters.q);
  if (filters.project_hash) query.set("project_hash", filters.project_hash);
  return query.toString();
}

export function useGroupedCollective(groupId: string, filters: CollectiveListFilters) {
  return useQuery({
    queryKey: ["group", groupId, "grouped", filters],
    queryFn: () => api<VillageGroupedGroupDetailResponse>(`/groups/${groupId}?${listQuery(filters)}`),
    enabled: !!groupId,
  });
}

export function useGroupedContributable(groupId: string, filters: CollectiveListFilters, enabled = true) {
  return useQuery({
    queryKey: ["group-contributable", groupId, "grouped", filters],
    queryFn: () => api<VillageGroupedContributableResponse>(`/groups/${groupId}/contributable?${listQuery(filters)}`),
    enabled: !!groupId && enabled,
  });
}

export function useGroupedPendingShares(groupId: string, filters: CollectiveListFilters, enabled: boolean) {
  return useQuery({
    queryKey: ["group-pending", groupId, "grouped", filters],
    queryFn: () => api<VillageSessionListPayload>(`/groups/${groupId}/pending?${listQuery(filters)}`),
    enabled: !!groupId && enabled,
  });
}

export function useGroupedMyShares(groupId: string, filters: CollectiveListFilters, enabled = true) {
  return useQuery({
    queryKey: ["group-my-shares", groupId, "grouped", filters],
    queryFn: () => api<VillageSessionListPayload>(`/groups/${groupId}/my-shares?${listQuery(filters)}`),
    enabled: !!groupId && enabled,
  });
}
