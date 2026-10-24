/**
 * Session-list page request helpers for the mounted `/` Explore surface.
 *
 * The Explore pager owns the *requested* page intent (mirrored into
 * `ExploreFilters.page`). The discovery response carries a *confirmed* page for
 * the key that produced it. These helpers keep those two concepts separate:
 *
 *   • `buildTranscriptListParams` turns the current filters into the complete
 *     set of server-affecting request parameters (`sort`, `page`, `limit`, and
 *     the optional `q`/`provider`/`tags`). Every value that changes the response
 *     lives here so it can form a complete TanStack query key.
 * Response pagination is validated at the production query boundary before
 * TanStack Query can cache it as successful data; this module only builds the
 * complete request identity used by that boundary.
 */

/** Explicit Village page size for the commons discovery list. */
export const TRANSCRIPT_PAGE_SIZE = 24;

/** The `/api/v1/transcripts` discovery endpoint, named in the errors a failed
 *  request throws (for whoever reads the logs), never on the page. */
export const TRANSCRIPT_LIST_ENDPOINT = "/api/v1/transcripts";

/** Stable machine-readable category for discovery response trust failures. */
export enum TranscriptListQueryErrorCode {
  ResponsePaginationMismatch = "response_pagination_mismatch",
}

/**
 * Why a transcript-list request failed, in words for the page: the server's
 * own reason when it gave one, or a plain sentence for a response the page
 * refused. Never an internal path or a request detail; those stay in the
 * error itself, for whoever reads the logs.
 */
export function transcriptListFailureCause(error: unknown): string {
  if ((error as { code?: unknown } | null)?.code === TranscriptListQueryErrorCode.ResponsePaginationMismatch) {
    return "the server answered with a different page than the one asked for";
  }
  const message = error instanceof Error ? error.message.trim().replace(/[.\s]+$/, "") : "";
  if (!message || /\/api\/|https?:\/\//.test(message)) return "the request failed";
  return message;
}

/**
 * Component-owned Explore filter state. `page` is the user's requested
 * navigation intent; the other fields are the active discovery filters. The URL
 * is deliberately non-authoritative for this surface, so this is the sole
 * request-intent authority on the Village side.
 */
export type ExploreFilters = {
  query: string;
  provider: string;
  topics: string[];
  order: string;
  page: number;
};

/**
 * Build the complete set of server-affecting request parameters from the active
 * filters. Every returned value changes the discovery response, so the whole
 * record is safe to use as (part of) the TanStack query key.
 */
export function buildTranscriptListParams(
  filters: ExploreFilters,
): Record<string, string> {
  const params: Record<string, string> = {
    sort: filters.order,
    page: String(filters.page),
    limit: String(TRANSCRIPT_PAGE_SIZE),
  };
  const trimmedQuery = filters.query.trim();
  if (trimmedQuery) params.q = trimmedQuery;
  if (filters.provider && filters.provider !== "all") {
    params.provider = filters.provider;
  }
  if (filters.topics.length > 0) params.tags = filters.topics.join(",");
  return params;
}
