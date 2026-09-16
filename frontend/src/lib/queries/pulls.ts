import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type {
  VillagePullRequestAttachmentResponse,
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
export function useUserPromptSettings(enabled: boolean) {
  return useQuery({
    queryKey: ["user-prompt-settings"],
    queryFn: () => api<VillageUserSettings>("/users/me/settings"),
    retry: false,
    enabled,
  });
}

export function useUpdateUserPromptSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (previewBeforeAttach: boolean) =>
      api<VillageUserSettings>("/users/me/settings", {
        method: "PATCH",
        body: JSON.stringify({ preview_before_attach: previewBeforeAttach }),
      }),
    onSuccess: (updated) => {
      qc.setQueryData(["user-prompt-settings"], updated);
    },
  });
}
