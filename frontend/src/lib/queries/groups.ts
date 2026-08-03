import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { Group, GroupMember, GroupContributor, GroupTranscript, GroupTranscriptStats, GroupModelBreakdown, CollectiveSearchResponse, UserGroupShare } from "../types";

export function useGroups() {
  return useQuery({
    queryKey: ["groups"],
    queryFn: () => api<Group[]>("/groups"),
  });
}

export function useGroup(id: string) {
  return useQuery({
    queryKey: ["group", id],
    queryFn: () =>
      api<{
        group: Group;
        members: GroupMember[];
        transcripts: GroupTranscript[];
        stats: GroupTranscriptStats;
        models: GroupModelBreakdown[];
        contributors: GroupContributor[];
        can_read: boolean;
        your_role: string;
        pending_members?: GroupMember[];
      }>(`/groups/${id}`),
    enabled: !!id,
  });
}

export function useGroupTranscripts(groupId: string, page: number, pageSize: number, enabled: boolean) {
  return useQuery({
    queryKey: ["group-transcripts", groupId, page, pageSize],
    queryFn: async () => {
      const res = await api<{
        transcripts: GroupTranscript[];
      }>(`/groups/${groupId}?limit=${pageSize}&offset=${page * pageSize}`);
      return res.transcripts ?? [];
    },
    enabled: enabled && !!groupId,
    placeholderData: (prev) => prev,
  });
}

export function useRemoveGroupTranscript() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ groupId, transcriptId }: { groupId: string; transcriptId: string }) =>
      api(`/groups/${groupId}/transcripts/${transcriptId}`, { method: "DELETE" }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["group", vars.groupId] });
      qc.invalidateQueries({ queryKey: ["group-transcripts", vars.groupId] });
    },
  });
}

export function useCreateGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { name: string; description?: string; acceptance_mode?: string; data_access?: string; linked_github_org?: string | null }) =>
      api<Group>("/groups", { method: "POST", body: JSON.stringify(data) }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["groups"] });
    },
  });
}

export function useUpdateGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      name,
      description,
      acceptance_mode,
      data_access,
      linked_github_org,
      display_members,
      transcript_deletion_policy,
    }: {
      id: string;
      name: string;
      description: string;
      acceptance_mode: string;
      data_access: string;
      linked_github_org?: string | null;
      display_members?: boolean;
      transcript_deletion_policy?: "user_choice" | "mandatory";
    }) =>
      api<Group>(`/groups/${id}`, {
        method: "PATCH",
        body: JSON.stringify({
          name,
          description,
          acceptance_mode,
          data_access,
          linked_github_org,
          display_members,
          transcript_deletion_policy,
        }),
      }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["group", vars.id] });
      qc.invalidateQueries({ queryKey: ["groups"] });
    },
  });
}

export function useMyGroupShares(groupId: string, enabled = true) {
  return useQuery({
    queryKey: ["group-my-shares", groupId],
    queryFn: () => api<UserGroupShare[]>(`/groups/${groupId}/my-shares`),
    enabled: enabled && !!groupId,
  });
}

export function useDeleteGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api(`/groups/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["groups"] });
    },
  });
}

export function useJoinGroup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (groupId: string) =>
      api(`/groups/${groupId}/join`, { method: "POST" }),
    onSuccess: (_, groupId) => {
      qc.invalidateQueries({ queryKey: ["group", groupId] });
      qc.invalidateQueries({ queryKey: ["groups"] });
    },
  });
}

export function usePromoteMember() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ groupId, userId, role }: { groupId: string; userId: string; role: string }) =>
      api(`/groups/${groupId}/members/${userId}/role`, {
        method: "PATCH",
        body: JSON.stringify({ role }),
      }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["group", vars.groupId] });
    },
  });
}

export function useAddGroupMember() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ groupId, username }: { groupId: string; username: string }) =>
      api(`/groups/${groupId}/members`, {
        method: "POST",
        body: JSON.stringify({ username }),
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
      return api(`/groups/${groupId}/members/${userId}${qs}`, { method: "DELETE" });
    },
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["group", vars.groupId] });
      qc.invalidateQueries({ queryKey: ["groups"] });
      qc.invalidateQueries({ queryKey: ["transcripts"] });
      qc.invalidateQueries({ queryKey: ["group-my-shares", vars.groupId] });
    },
  });
}

export function useSearchCollectives(query: string) {
  return useQuery({
    queryKey: ["collective-search", query],
    queryFn: () => api<CollectiveSearchResponse>(`/groups/search?q=${encodeURIComponent(query)}`),
    enabled: query.trim().length > 0,
  });
}
