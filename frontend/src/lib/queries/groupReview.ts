import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { BatchReviewRequest, BatchReviewResponse, PendingShare } from "../review/types";

/** The query key both the collective page and the review page read the pending
 *  queue under, so a decision on either surface refreshes the other. */
export function pendingSharesKey(groupId: string) {
  return ["group-pending", groupId] as const;
}

/**
 * `GET /groups/{id}/pending` — every submission awaiting a decision in this
 * collective, with the project, branch and parent identity the review tree
 * groups by. Owner-only on the server; `enabled` keeps a non-owner from asking
 * for a 403 they cannot act on.
 */
export function usePendingShares(groupId: string, enabled: boolean) {
  return useQuery({
    queryKey: pendingSharesKey(groupId),
    queryFn: () => api<PendingShare[]>(`/groups/${groupId}/pending`),
    enabled: enabled && !!groupId,
  });
}

/**
 * `PATCH /groups/{id}/shares` — ONE decision applied to a whole selection in a
 * single request. The server decides only still-open submissions and reports
 * the rest in `already_decided`, so this mutation is sent optimistically
 * against a queue that may already have moved under the reviewer.
 */
export function useBatchReview(groupId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: BatchReviewRequest) =>
      api<BatchReviewResponse>(`/groups/${groupId}/shares`, {
        method: "PATCH",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: pendingSharesKey(groupId) });
      qc.invalidateQueries({ queryKey: ["group", groupId] });
    },
  });
}
