import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  VillagePullRequestAttachmentResponse,
  VillageTranscriptPullRequest,
  VillageTranscriptPullRequestsResponse,
  VillageUserSettings,
} from "@peasant-labs/schema";
import { api } from "../api";

/**
 * The pull request attachment routes and the caller's own attachment settings.
 *
 * A confirm or a detach returns the whole attachment response, so a mutation
 * writes the answer straight into the same key the page reads: no refetch, and
 * the page can never show a state the server has already moved past.
 */

/** The one key for one pull request's attachment. */
export function pullRequestAttachmentKey(owner: string, name: string, number: number) {
  return ["pull-request-attachment", owner.toLowerCase(), name.toLowerCase(), number] as const;
}

function pullPath(owner: string, name: string, number: number): string {
  return `/pulls/${encodeURIComponent(owner)}/${encodeURIComponent(name)}/${number}`;
}

export function usePullRequestAttachment(owner: string, name: string, number: number) {
  return useQuery({
    queryKey: pullRequestAttachmentKey(owner, name, number),
    queryFn: () => api<VillagePullRequestAttachmentResponse>(pullPath(owner, name, number)),
    retry: false,
    enabled: !!owner && !!name && Number.isInteger(number) && number > 0,
  });
}

/**
 * The pull requests one transcript is bound to, attached or detached, that THIS
 * viewer may read (`GET /transcripts/{id}/pulls`). The server applies the
 * transcript's own read check and each pull request's reader rule, so the list
 * is already narrowed to the viewer; the page renders it as served.
 */
export function transcriptPullRequestsKey(transcriptId: string) {
  return ["transcript-pull-requests", transcriptId] as const;
}

export function useTranscriptPullRequests(transcriptId: string, enabled = true) {
  return useQuery({
    queryKey: transcriptPullRequestsKey(transcriptId),
    queryFn: async (): Promise<VillageTranscriptPullRequest[]> => {
      const res = await api<VillageTranscriptPullRequestsResponse>(
        `/transcripts/${encodeURIComponent(transcriptId)}/pulls`,
      );
      return res.pull_requests ?? [];
    },
    enabled: enabled && !!transcriptId,
  });
}

/** Confirm a preview into attached. Author-only, and refused with 409 outside a preview. */
export function useConfirmPullRequestAttachment(owner: string, name: string, number: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      api<VillagePullRequestAttachmentResponse>(`${pullPath(owner, name, number)}/confirm`, {
        method: "POST",
      }),
    onSuccess: (updated) => {
      qc.setQueryData(pullRequestAttachmentKey(owner, name, number), updated);
    },
  });
}

/** Detach: delete the comment, reset the check, restore every bound transcript. */
export function useDetachPullRequestAttachment(owner: string, name: string, number: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      api<VillagePullRequestAttachmentResponse>(pullPath(owner, name, number), {
        method: "DELETE",
      }),
    onSuccess: (updated) => {
      qc.setQueryData(pullRequestAttachmentKey(owner, name, number), updated);
    },
  });
}

/**
 * Whether this caller's own attachments stop at a preview they confirm. Served
 * by `/users/me/settings`, which is separate from `/auth/me`: the profile
 * payload carries no such field, and this setting belongs to the attachment
 * lifecycle rather than to the account.
 */
// The key carries the viewer it belongs to: the route is always "me", but a
// sign-out followed by a different sign-in must not serve the previous
// account's value from the cache.
export function userPromptSettingsKey(username: string) {
  return ["user-prompt-settings", username.toLowerCase()] as const;
}

export function useUserPromptSettings(enabled: boolean, username: string) {
  return useQuery({
    queryKey: userPromptSettingsKey(username),
    queryFn: () => api<VillageUserSettings>("/users/me/settings"),
    retry: false,
    enabled: enabled && !!username,
  });
}

export function useUpdateUserPromptSettings(username: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (previewBeforeAttach: boolean) =>
      api<VillageUserSettings>("/users/me/settings", {
        method: "PATCH",
        body: JSON.stringify({ preview_before_attach: previewBeforeAttach }),
      }),
    onSuccess: (updated) => {
      qc.setQueryData(userPromptSettingsKey(username), updated);
    },
  });
}

/**
 * Turns automatic pull request linking on or off for the signed-in person
 * (`auto_attach_pull_requests` on `PATCH /users/me/settings`). Only this one
 * field is sent, so the other setting on the route is left as it is.
 */
export function useUpdateAutoAttachPullRequests(username: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (autoAttach: boolean) =>
      api<VillageUserSettings>("/users/me/settings", {
        method: "PATCH",
        body: JSON.stringify({ auto_attach_pull_requests: autoAttach }),
      }),
    onSuccess: (updated) => {
      qc.setQueryData(userPromptSettingsKey(username), updated);
    },
  });
}
