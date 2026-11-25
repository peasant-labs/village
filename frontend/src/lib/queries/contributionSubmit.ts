import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  zVillageBatchShareResponse,
  type VillageBatchShareResponse,
  type VillageBatchShareSelectionRequest,
} from "@peasant-labs/schema";
import { api, ApiError } from "../api";
import {
  runContribution,
  type ContributionProgress,
  type ContributionReceipt,
  type PrivateApproval,
} from "../contribute/chunkedSubmit";
import type { FrozenSelection, SelectedRecord } from "../contribute/frozenSelection";

/**
 * The contribute page's write: one chunk of the frozen selection to
 * `POST /collectives/{id}/shares/selection`, and the hook that runs the whole
 * selection through it.
 */

export function contributionSelectionPath(groupId: string): string {
  return `/collectives/${encodeURIComponent(groupId)}/shares/selection`;
}

/**
 * Checks a 200 against the contract and against the request it answers. A
 * body that fails either check is treated as a server failure: the chunk is
 * reported failed and stays selected, and resending it is safe, because a
 * session the write already took comes back as already shared.
 */
export function decodeSelectionResponse(raw: unknown, request: VillageBatchShareSelectionRequest): VillageBatchShareResponse {
  const parsed = zVillageBatchShareResponse.safeParse(raw);
  if (!parsed.success) {
    throw new ApiError(502, "the server's answer to this contribution did not match the contract");
  }
  if (parsed.data.project_hash !== request.project_hash) {
    throw new ApiError(502, "the server answered this contribution for a different project");
  }
  return parsed.data;
}

export async function submitSelection(
  groupId: string,
  request: VillageBatchShareSelectionRequest,
  signal?: AbortSignal,
): Promise<VillageBatchShareResponse> {
  const raw = await api<unknown>(contributionSelectionPath(groupId), {
    method: "POST",
    body: JSON.stringify(request),
    signal,
  });
  return decodeSelectionResponse(raw, request);
}

/**
 * Where a contribution run is. The phases discriminate
 * {@link ContributionRunState}, so they are plain strings: a discriminant
 * narrows only on a literal type.
 */
export const ContributionRunPhase = Object.freeze({
  Idle: "idle",
  Running: "running",
  Done: "done",
} as const);
export type ContributionRunPhase = (typeof ContributionRunPhase)[keyof typeof ContributionRunPhase];

export type ContributionRunState =
  | { phase: typeof ContributionRunPhase.Idle }
  | { phase: typeof ContributionRunPhase.Running; progress: ContributionProgress | null }
  | { phase: typeof ContributionRunPhase.Done; receipt: ContributionReceipt };

export interface StartContribution {
  selection: FrozenSelection;
  /** The private ids the person approved in the consent step. */
  approved: ReadonlySet<string>;
  /** Re-asks about sessions that became private after they were selected. */
  approvePrivate: (records: SelectedRecord[]) => Promise<PrivateApproval>;
}

/**
 * Runs one submission at a time and keeps its progress and receipt. When the
 * run ends, however it ends, every contribution read of the collective is
 * refreshed, so a contributed row shows as locked.
 */
export function useContributionRun(groupId: string) {
  const client = useQueryClient();
  const [state, setState] = useState<ContributionRunState>({ phase: ContributionRunPhase.Idle });
  const controller = useRef<AbortController | null>(null);

  useEffect(() => () => controller.current?.abort(), []);

  const start = useCallback(
    async ({ selection, approved, approvePrivate }: StartContribution): Promise<ContributionReceipt | null> => {
      if (controller.current) return null;
      const abort = new AbortController();
      controller.current = abort;
      setState({ phase: ContributionRunPhase.Running, progress: null });
      try {
        const receipt = await runContribution({
          selection,
          approved,
          approvePrivate,
          signal: abort.signal,
          submit: (request) => submitSelection(groupId, request, abort.signal),
          onProgress: (progress) => setState({ phase: ContributionRunPhase.Running, progress }),
        });
        setState({ phase: ContributionRunPhase.Done, receipt });
        return receipt;
      } finally {
        controller.current = null;
        void client.invalidateQueries({ queryKey: ["group-contributable", groupId] });
        void client.invalidateQueries({ queryKey: ["group", groupId] });
      }
    },
    [client, groupId],
  );

  const cancel = useCallback(() => controller.current?.abort(), []);
  const reset = useCallback(() => {
    if (!controller.current) setState({ phase: ContributionRunPhase.Idle });
  }, []);

  return { state, start, cancel, reset };
}
