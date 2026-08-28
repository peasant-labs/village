import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, API_URL_BASE, getAuthHeaders } from "../api";
import type {
  ResolvedProject,
  TranscriptDetailResponse,
  TranscriptListResponse,
  UserProjectPageResponse,
} from "../types";
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
/**
 * @param options.enabled withhold the request until the caller's parameters are
 *   real. A filter value that is not yet known is dropped by the list handler
 *   rather than narrowing anything, so an unconditional request would answer a
 *   narrow question with the whole commons.
 */
export function useTranscripts(
  params?: Record<string, string>,
  options?: { enabled?: boolean },
) {
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
    enabled: options?.enabled ?? true,
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

/**
 * The project page's payload: `GET /users/{username}/projects/{projectHash}`
 * (AuthOptional).
 *
 * A 404 from this route is the profile-visibility boundary, not a transport
 * failure: it answers identically for a user who does not exist, a user who has
 * turned discoverability off, and a project neither of those owns. `retry` is
 * therefore off — re-requesting a deliberate refusal only delays the not-found
 * render — and callers branch on the {@link ApiError} status rather than
 * treating the rejection as a generic error.
 */
export function useUserProject(username: string, projectHash: string) {
  return useQuery({
    queryKey: ["user-project", username, projectHash],
    queryFn: () =>
      api<UserProjectPageResponse>(
        `/users/${encodeURIComponent(username)}/projects/${encodeURIComponent(projectHash)}`,
      ),
    retry: false,
    enabled: !!username && !!projectHash,
  });
}

/**
 * Sets the owner override for a project's display name, keyed on
 * `project_hash` — the project's IDENTITY, not a derived name. The route this
 * replaced, `PATCH /users/me/projects/rename` (keyed on `{from, to}` name
 * strings), is deleted with no shim: it matched zero rows whenever the client's
 * locally-derived name disagreed with what the server actually stored, which is
 * the exact defect this project-identity work exists to fix. Do not reintroduce
 * a name-keyed body shape here.
 *
 * The response is the project's newly {@link ResolvedProject resolved} identity,
 * so the control that issued the change re-renders from the server's answer
 * instead of echoing the value it just sent.
 */
export function useSetProjectDisplayName() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ projectHash, displayName }: { projectHash: string; displayName: string }) =>
      api<ResolvedProject>(`/users/me/projects/${encodeURIComponent(projectHash)}`, {
        method: "PATCH",
        body: JSON.stringify({ display_name: displayName }),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["transcripts"] });
      qc.invalidateQueries({ queryKey: ["user-project"] });
    },
  });
}

/**
 * Clears the owner override, reverting the project to its resolved default
 * (`DELETE /users/me/projects/{projectHash}/display-name`).
 *
 * The default is whatever the remaining evidence resolves to — a consented
 * name, a remote label, or the privacy-safe label — so the answer is read back
 * off the response rather than guessed at: after a clear, both the name AND the
 * tier it now comes from change, and a client that kept showing the cleared
 * value would be showing a name the server no longer holds.
 */
export function useClearProjectDisplayName() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ projectHash }: { projectHash: string }) =>
      api<ResolvedProject>(
        `/users/me/projects/${encodeURIComponent(projectHash)}/display-name`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["transcripts"] });
      qc.invalidateQueries({ queryKey: ["user-project"] });
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
