import { useId, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import type { VillageCreateCollectiveRequest, VillageCollective, VillageUpdateCollectiveRequest } from "@peasant-labs/schema";
import { api, getAuthHeaders } from "../api";
import { useAuth } from "@/providers/AuthProvider";
import type { Group, VisibleGroup, GroupMember, GroupContributor, GroupTranscript, GroupTranscriptStats, GroupModelBreakdown, CollectiveSearchResponse, UserGroupShare, User } from "../types";

// Each mounted binding owns an opaque namespace and credential epoch. A render
// derives a fresh scope immediately and updates only its own guarded state.
// Credentials stay in memory closures, never query keys or persisted cache data.
export function useCollectiveReadBinding() {
  const { user, isLoading } = useAuth();
  const client = useQueryClient();
  const bindingId = useId();
  const authorization: string | undefined = getAuthHeaders().Authorization;
  const viewer = user?.id ?? "anonymous";
  const [credential, setCredential] = useState(() => ({
    matches: (candidateViewer: string, candidateAuthorization: string | undefined) => candidateViewer === viewer && candidateAuthorization === authorization,
    epoch: 0,
  }));
  const epoch = credential.epoch + (credential.matches(viewer, authorization) ? 0 : 1);
  if (!credential.matches(viewer, authorization)) {
    setCredential({
      matches: (candidateViewer: string, candidateAuthorization: string | undefined) => candidateViewer === viewer && candidateAuthorization === authorization,
      epoch,
    });
  }
  const version = `${bindingId}:${epoch}`;
  const current = () => (client.getQueryData<User>(["me"])?.id ?? "anonymous") === viewer && getAuthHeaders().Authorization === authorization;
  async function read<T>(path: string, signal?: AbortSignal): Promise<T> {
    if (!current()) throw new Error("the signed-in account changed before reading the collective; try again");
    const result = await api<T>(path, { signal });
    if (!current()) throw new Error("the signed-in account changed while reading the collective; try again");
    return result;
  }
  return { viewer, version, isLoading, read };
}

/**
 * The collectives the caller BELONGS to (`GET /collectives`).
 *
 * This is the set to offer somebody who is choosing where to contribute: every
 * row is a membership and carries a role. For the browse surface, which shows
 * every collective a person may see, use {@link useVisibleGroups}.
 */
export function useGroups(options: { enabled?: boolean } = {}) {
  return useQuery({
    queryKey: ["groups"],
    queryFn: () => api<Group[]>("/collectives"),
    enabled: options.enabled ?? true,
  });
}

/**
 * Every collective the caller may SEE (`GET /collectives/visible`), whether or not
 * they belong to it: the server admits a collective whose data is public, one
 * that anybody may join, and one the caller is a member of.
 *
 * Rows the caller does not belong to carry a null role and a null member_since.
 */
export function useVisibleGroups() {
  return useQuery({
    queryKey: ["visible-groups"],
    queryFn: () => api<VisibleGroup[]>("/collectives/visible"),
  });
}

export function useGroup(id: string) {
  const { viewer, version, isLoading, read } = useCollectiveReadBinding();
  return useQuery({
    queryKey: ["group", id, "flat", viewer, version],
    queryFn: ({ signal }) =>
      read<{
        group: Group;
        members: GroupMember[];
        transcripts: GroupTranscript[];
        stats: GroupTranscriptStats;
        models: GroupModelBreakdown[];
        contributors: GroupContributor[];
        can_read: boolean;
        your_role: string;
        pending_members?: GroupMember[];
      }>(`/collectives/${id}`, signal),
    enabled: !isLoading && !!id,
  });
}

export function useGroupTranscripts(groupId: string, page: number, pageSize: number, enabled: boolean) {
  const { viewer, version, isLoading, read } = useCollectiveReadBinding();
  return useQuery({
    queryKey: ["group-transcripts", groupId, viewer, version, page, pageSize],
    queryFn: async ({ signal }) => {
      const res = await read<{
        transcripts: GroupTranscript[];
      }>(`/collectives/${groupId}?limit=${pageSize}&offset=${page * pageSize}`, signal);
      return res.transcripts ?? [];
    },
    enabled: !isLoading && enabled && !!groupId,
    placeholderData: (previous, previousQuery) => previousQuery?.queryKey[2] === viewer && previousQuery.queryKey[3] === version ? previous : undefined,
  });
}

export function useRemoveGroupTranscript() {
  const qc = useQueryClient();
  type Decision = { id: string; authorization: string | undefined };
  const current = (decision?: Decision) => !decision || (qc.getQueryData<User>(["me"])?.id === decision.id && getAuthHeaders().Authorization === decision.authorization);
  return useMutation({
    mutationFn: ({ groupId, transcriptId, decision }: { groupId: string; transcriptId: string; decision?: Decision }) => {
      // React Query may defer dispatch after the page's confirmation guard.
      if (!current(decision)) throw new Error("the signed-in account changed before removal");
      return api(`/collectives/${groupId}/transcripts/${transcriptId}`, { method: "DELETE" });
    },
    onSuccess: (_, vars) => {
      if (!current(vars.decision)) return;
      qc.invalidateQueries({ queryKey: ["group", vars.groupId] });
      qc.invalidateQueries({ queryKey: ["group-transcripts", vars.groupId] });
    },
  });
}

export function useCreateGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: VillageCreateCollectiveRequest) =>
      api<VillageCollective>("/collectives", { method: "POST", body: JSON.stringify(data) }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["groups"] });
      qc.invalidateQueries({ queryKey: ["visible-groups"] });
    },
  });
}

/**
 * One settings change: `body` names the fields it writes and the server keeps
 * every field it leaves out. Build it with `settingPatch`, which proves it with
 * the contract's parser before anything is sent.
 */
export function useUpdateGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: VillageUpdateCollectiveRequest }) =>
      api<VillageCollective>(`/collectives/${id}`, {
        method: "PATCH",
        body: JSON.stringify(body),
      }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["group", vars.id] });
      qc.invalidateQueries({ queryKey: ["groups"] });
      qc.invalidateQueries({ queryKey: ["visible-groups"] });
    },
  });
}

export function useMyGroupShares(groupId: string, enabled = true) {
  const { viewer, version, isLoading, read } = useCollectiveReadBinding();
  return useQuery({
    queryKey: ["group-my-shares", groupId, "flat", viewer, version],
    queryFn: ({ signal }) => read<UserGroupShare[]>(`/collectives/${groupId}/my-shares`, signal),
    enabled: !isLoading && enabled && !!groupId,
  });
}

export function useDeleteGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api(`/collectives/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["groups"] });
      qc.invalidateQueries({ queryKey: ["visible-groups"] });
    },
  });
}

export function useJoinGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (groupId: string) =>
      api(`/collectives/${groupId}/join`, { method: "POST" }),
    onSuccess: (_, groupId) => {
      qc.invalidateQueries({ queryKey: ["group", groupId] });
      qc.invalidateQueries({ queryKey: ["groups"] });
      qc.invalidateQueries({ queryKey: ["visible-groups"] });
    },
  });
}

export function usePromoteMember() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ groupId, userId, role }: { groupId: string; userId: string; role: string }) =>
      api(`/collectives/${groupId}/members/${userId}/role`, {
        method: "PATCH",
        body: JSON.stringify({ role }),
      }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["group", vars.groupId] });
    },
  });
}

export function useRemoveGroupMember() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      groupId,
      userId,
      retract,
    }: {
      groupId: string;
      userId: string;
      retract?: boolean;
    }) => {
      const qs = retract ? "?retract=true" : "";
      return api(`/collectives/${groupId}/members/${userId}${qs}`, { method: "DELETE" });
    },
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["group", vars.groupId] });
      qc.invalidateQueries({ queryKey: ["groups"] });
      qc.invalidateQueries({ queryKey: ["visible-groups"] });
      qc.invalidateQueries({ queryKey: ["transcripts"] });
      qc.invalidateQueries({ queryKey: ["group-my-shares", vars.groupId] });
    },
  });
}

export function useSearchCollectives(query: string) {
  return useQuery({
    queryKey: ["collective-search", query],
    queryFn: () => api<CollectiveSearchResponse>(`/collectives/search?q=${encodeURIComponent(query)}`),
    enabled: query.trim().length > 0,
  });
}
