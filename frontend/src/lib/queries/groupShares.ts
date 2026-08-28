import { useCallback, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import type { BatchShareRequest, BatchShareResponse, ContributableResponse } from "../contribute/types";

/**
 * `GET /groups/{id}/contributable` — every transcript the caller owns that
 * could be contributed to this collective, including ones already shared
 * (flagged `already_shared`, never omitted, so the tree can show and disable
 * them instead of the row silently disappearing between visits).
 */
export function useContributable(groupId: string) {
  return useQuery({
    queryKey: ["group-contributable", groupId],
    queryFn: () => api<ContributableResponse>(`/groups/${groupId}/contributable`),
    enabled: !!groupId,
  });
}

/**
 * `POST /groups/{id}/shares` — ONE project's batch. The caller
 * (`useContributeRun`) is responsible for splitting a multi-project
 * selection into one call per project; this mutation never accepts more
 * than one `project_hash` per call, by construction of {@link BatchShareRequest}.
 */
export function useBatchShareProject(groupId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: BatchShareRequest) =>
      api<BatchShareResponse>(`/groups/${groupId}/shares`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["group-contributable", groupId] });
      qc.invalidateQueries({ queryKey: ["group", groupId] });
      qc.invalidateQueries({ queryKey: ["groups-public"] });
    },
  });
}

/** One project's outcome after a run: the server's response, or the
 *  {@link ApiError} it refused with. Never anything else — a thrown
 *  non-`ApiError` (a network failure, an unexpected exception) is wrapped
 *  into one so every result reads the same way. */
export type ContributeRunOutcome = BatchShareResponse | ApiError;

export interface ContributeRunState {
  running: boolean;
  total: number;
  done: number;
  results: Map<string, ContributeRunOutcome>;
}

const INITIAL_STATE: ContributeRunState = {
  running: false,
  total: 0,
  done: 0,
  results: new Map(),
};

/**
 * Drives the sequential one-POST-per-project batch-share run: iterates
 * the selected projects IN ORDER, awaiting each `POST /groups/{id}/shares`
 * before starting the next, and never stops early on a failure — every
 * project named in `batches` gets its own attempt, and `state.results`
 * reports each one's outcome (success or the server's own {@link ApiError})
 * once the run settles. `state` updates after every project completes, so a
 * progress bar can render `done / total` live.
 */
export function useContributeRun(groupId: string) {
  const batchShare = useBatchShareProject(groupId);
  const [state, setState] = useState<ContributeRunState>(INITIAL_STATE);

  const run = useCallback(
    async (
      batches: Map<string, string[]>,
      visibilityConfirmed: boolean,
    ): Promise<Map<string, ContributeRunOutcome>> => {
      const projectHashes = [...batches.keys()];
      const results = new Map<string, ContributeRunOutcome>();
      setState({ running: true, total: projectHashes.length, done: 0, results: new Map() });

      for (const projectHash of projectHashes) {
        const transcriptIds = batches.get(projectHash) ?? [];
        try {
          const response = await batchShare.mutateAsync({
            project_hash: projectHash,
            transcript_ids: transcriptIds,
            visibility_confirmed: visibilityConfirmed,
          });
          results.set(projectHash, response);
        } catch (err) {
          // Never stop the run: record this project's refusal and continue
          // to the next one. A non-ApiError throw (network failure) is
          // wrapped so every entry in `results` is the same shape.
          const failure =
            err instanceof ApiError
              ? err
              : new ApiError(0, err instanceof Error ? err.message : "the request failed unexpectedly");
          results.set(projectHash, failure);
        }
        setState((prev) => ({ ...prev, done: prev.done + 1, results: new Map(results) }));
      }

      setState((prev) => ({ ...prev, running: false }));
      return results;
    },
    [batchShare],
  );

  return { state, run, reset: () => setState(INITIAL_STATE) };
}

/**
 * Splits a just-finished run's outcome into ids to clear from the selection
 * (every id under a project whose POST succeeded) and ids that must stay
 * selected (every id under a project that failed, or was never attempted —
 * "unfinished" — so a retry is one click). A project's outcome is all-or-
 * nothing: a run does not partially clear a project's ids by per-transcript
 * status, because the server's own `shared`/`already_shared` split already
 * describes disposition WITHIN a successful project.
 */
export function partitionRunOutcome(
  batches: Map<string, string[]>,
  results: Map<string, ContributeRunOutcome>,
): { clearedIds: string[]; staySelectedIds: string[] } {
  const clearedIds: string[] = [];
  const staySelectedIds: string[] = [];
  for (const [projectHash, ids] of batches) {
    const outcome = results.get(projectHash);
    if (outcome && !(outcome instanceof ApiError)) {
      clearedIds.push(...ids);
    } else {
      staySelectedIds.push(...ids);
    }
  }
  return { clearedIds, staySelectedIds };
}
