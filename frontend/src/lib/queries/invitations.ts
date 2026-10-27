import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  VillageCollectiveInvitationsSentResponse,
  VillageCollectiveStatusRoleResponse,
  VillageMyInvitationsResponse,
  VillageStatusResponse,
} from "@peasant-labs/schema";
import { api } from "../api";

/**
 * Collective invitations. An owner invites a person by username; the person
 * becomes a member only by accepting. The invite route answers the same way
 * whether or not an account holds the name, so the client never learns, and
 * never says, whether the person exists.
 */

export function collectiveInvitationsKey(groupId: string) {
  return ["collective-invitations", groupId] as const;
}

/** The invitations an owner has sent from one collective (`GET /collectives/{id}/invitations`). */
export function useCollectiveInvitations(groupId: string, enabled = true) {
  return useQuery({
    queryKey: collectiveInvitationsKey(groupId),
    queryFn: () => api<VillageCollectiveInvitationsSentResponse>(`/collectives/${groupId}/invitations`),
    enabled: enabled && !!groupId,
  });
}

/** Invites one username to a collective. Every outcome the server accepts reads "invited". */
export function useInviteToCollective() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ groupId, username }: { groupId: string; username: string }) =>
      api<VillageStatusResponse>(`/collectives/${groupId}/invitations`, {
        method: "POST",
        body: JSON.stringify({ username }),
      }),
    onSuccess: (_, vars) => {
      void qc.invalidateQueries({ queryKey: collectiveInvitationsKey(vars.groupId) });
    },
  });
}

/** Revokes one open invitation of a collective. */
export function useRevokeInvitation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ groupId, invitationId }: { groupId: string; invitationId: string }) =>
      api<VillageStatusResponse>(`/collectives/${groupId}/invitations/${invitationId}`, { method: "DELETE" }),
    onSettled: (_, __, vars) => {
      void qc.invalidateQueries({ queryKey: collectiveInvitationsKey(vars.groupId) });
    },
  });
}

export const MY_INVITATIONS_KEY = ["my-invitations"] as const;

/** The signed-in person's own pending invitations (`GET /users/me/invitations`). */
export function useMyInvitations(enabled: boolean) {
  return useQuery({
    queryKey: MY_INVITATIONS_KEY,
    queryFn: () => api<VillageMyInvitationsResponse>("/users/me/invitations"),
    enabled,
  });
}

/** Accepts one of the caller's invitations: they join the collective as a member. */
export function useAcceptInvitation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (invitationId: string) =>
      api<VillageCollectiveStatusRoleResponse>(`/users/me/invitations/${invitationId}/accept`, { method: "POST" }),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: MY_INVITATIONS_KEY });
      void qc.invalidateQueries({ queryKey: ["groups"] });
      void qc.invalidateQueries({ queryKey: ["visible-groups"] });
    },
  });
}

/** Declines one of the caller's invitations. The owners are not told. */
export function useDeclineInvitation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (invitationId: string) =>
      api<VillageStatusResponse>(`/users/me/invitations/${invitationId}/decline`, { method: "POST" }),
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: MY_INVITATIONS_KEY });
    },
  });
}
