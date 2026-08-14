import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, API_URL_BASE, getAuthHeaders } from "../api";
import type { TranscriptListResponse, TranscriptDetailResponse } from "../types";
import type {
  AnnotationSummary,
  ListAnnotationsResponse,
} from "../annotations";
import { TRANSCRIPT_LIST_ENDPOINT } from "../transcriptPageRequest";

/** Stable machine-readable category for discovery response trust failures. */
export enum TranscriptListQueryErrorCode {
  ResponsePaginationMismatch = "response_pagination_mismatch",
}

/**
 * The discovery endpoint described different explicit pagination values than
 * the request asked for. This error is thrown before TanStack Query can treat
 * the response as successful cache data.
 */
export class TranscriptListResponseMismatchError extends Error {
  readonly code = TranscriptListQueryErrorCode.ResponsePaginationMismatch;
  readonly endpoint = TRANSCRIPT_LIST_ENDPOINT;
  readonly operation = "loading the session list";

  constructor(
    readonly requestedPage: string | undefined,
    readonly requestedLimit: string | undefined,
    readonly receivedPage: number,
    readonly receivedLimit: number,
  ) {
    const requestedValues = [
      requestedPage === undefined ? null : `page ${requestedPage}`,
      requestedLimit === undefined ? null : `limit ${requestedLimit}`,
    ].filter((value): value is string => value !== null);
    const receivedValues = [
      requestedPage === undefined ? null : `page ${receivedPage}`,
      requestedLimit === undefined ? null : `limit ${receivedLimit}`,
    ].filter((value): value is string => value !== null);
    super(
      `Session list response pagination mismatch while loading ${TRANSCRIPT_LIST_ENDPOINT}: ` +
        `requested ${requestedValues.join(" and ")}, but received ${receivedValues.join(" and ")}. ` +
        `The response was rejected before it could become successful cached data. ` +
        `Retry the same session-list request. If the mismatch persists, verify the endpoint pagination response.`,
    );
    this.name = "TranscriptListResponseMismatchError";
  }
}

function explicitPaginationMatches(requested: string | undefined, received: number): boolean {
  if (requested === undefined) return true;
  const normalized = Number(requested);
  return Number.isSafeInteger(normalized) && normalized === received;
}

function assertTranscriptListResponseMatchesRequest(
  params: Record<string, string> | undefined,
  response: TranscriptListResponse,
): void {
  const requestedPage = params?.page;
  const requestedLimit = params?.limit;
  if (
    explicitPaginationMatches(requestedPage, response.page) &&
    explicitPaginationMatches(requestedLimit, response.limit)
  ) {
    return;
  }
  throw new TranscriptListResponseMismatchError(
    requestedPage,
    requestedLimit,
    response.page,
    response.limit,
  );
}

/**
 * Discovery list query for the `/` Explore surface.
 *
 * `params` already carries every server-affecting value (page, limit, sort,
 * search, provider, tags — see {@link buildTranscriptListParams}), so the whole
 * record is the query key: distinct page/filter intents never collide on one
 * cache entry. The fetch receives TanStack's per-request {@link AbortSignal} so a
 * superseded page request is cancelled instead of racing to commit. Previous
 * confirmed rows are retained via `placeholderData` while a new page loads.
 */
export function useTranscripts(params?: Record<string, string>) {
  const searchParams = new URLSearchParams(params);
  return useQuery({
    queryKey: ["transcripts", params],
    queryFn: async ({ signal }) => {
      const response = await api<TranscriptListResponse>(
        `/transcripts?${searchParams}`,
        { signal },
      );
      assertTranscriptListResponseMatchesRequest(params, response);
      return response;
    },
    placeholderData: (previousData) => previousData,
  });
}

export function useTranscript(id: string) {
  return useQuery({
    queryKey: ["transcript", id],
    queryFn: () => api<TranscriptDetailResponse>(`/transcripts/${id}`),
    enabled: !!id,
  });
}

export function useTranscriptContent(id: string) {
  return useQuery({
    queryKey: ["transcript-content", id],
    queryFn: async () => {
      const res = await fetch(`${API_URL_BASE}/transcripts/${id}/content`, {
        headers: getAuthHeaders(),
      });
      if (!res.ok) {
        throw new Error(`API error: ${res.status}`);
      }
      const text = await res.text();
      // Try parsing as JSON first (single object or array)
      try {
        return JSON.parse(text);
      } catch {
        // Fall back to JSONL (newline-delimited JSON)
        const lines = text.split("\n").filter((line) => line.trim());
        return lines.map((line) => JSON.parse(line));
      }
    },
    enabled: !!id,
  });
}

/**
 * Manual per-turn labels for a transcript.
 *
 * GET /api/v1/transcripts/{id}/annotations (AuthOptional). Returns every stored
 * annotation (session- and entry-level); the viewer adapter narrows entry-level
 * ones into `savedLabelsByEntry`. Visibility follows the transcript's own rules.
 */
export function useTranscriptAnnotations(id: string, enabled = true) {
  return useQuery({
    queryKey: ["transcript-annotations", id],
    queryFn: () =>
      api<ListAnnotationsResponse>(`/transcripts/${id}/annotations`),
    enabled: !!id && enabled,
  });
}

/** Body for POST .../annotations — a single entry-level (per-turn) label. */
export interface CreateAnnotationInput {
  transcriptId: string;
  typeId: string;
  value: string;
  entryIndex: number;
}

/**
 * Persist a manual per-turn label.
 *
 * POST /api/v1/transcripts/{id}/annotations (AuthRequired). The 201 response is
 * the created `AnnotationSummary`. On success the cached annotation list is
 * invalidated so the new chip renders.
 */
export function useCreateTranscriptAnnotation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ transcriptId, typeId, value, entryIndex }: CreateAnnotationInput) =>
      api<AnnotationSummary>(`/transcripts/${transcriptId}/annotations`, {
        method: "POST",
        body: JSON.stringify({ typeId, value, entryIndex }),
      }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({
        queryKey: ["transcript-annotations", vars.transcriptId],
      });
    },
  });
}

export function useUpdateTranscript() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...data }: { id: string; title?: string; description?: string; visibility?: string; tags?: string[] }) =>
      api(`/transcripts/${id}`, { method: "PATCH", body: JSON.stringify(data) }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["transcript", vars.id] });
      qc.invalidateQueries({ queryKey: ["transcripts"] });
    },
  });
}

export function useDeleteTranscript() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      api(`/transcripts/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["transcripts"] });
    },
  });
}

export function useRenameUserProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ from, to }: { from: string; to: string }) =>
      api(`/users/me/projects/rename`, {
        method: "PATCH",
        body: JSON.stringify({ from, to }),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["transcripts"] });
    },
  });
}

export function useUnshareTranscript() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ transcriptId, groupId }: { transcriptId: string; groupId: string }) =>
      api(`/transcripts/${transcriptId}/share/${groupId}`, { method: "DELETE" }),
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["transcripts"] });
      qc.invalidateQueries({ queryKey: ["group", vars.groupId] });
      qc.invalidateQueries({ queryKey: ["group-my-shares", vars.groupId] });
    },
  });
}

export function useBulkShareTranscripts() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ transcriptIds, groupId }: { transcriptIds: string[]; groupId: string }) => {
      const results = await Promise.allSettled(
        transcriptIds.map((tid) =>
          api(`/transcripts/${tid}/share`, {
            method: "POST",
            body: JSON.stringify({ group_ids: [groupId] }),
          })
        )
      );
      return results;
    },
    onSuccess: (_, vars) => {
      qc.invalidateQueries({ queryKey: ["transcripts"] });
      qc.invalidateQueries({ queryKey: ["group", vars.groupId] });
      qc.invalidateQueries({ queryKey: ["groups-public"] });
    },
  });
}
