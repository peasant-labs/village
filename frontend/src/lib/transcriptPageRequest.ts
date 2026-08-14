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
 *   • `validateSettledTranscriptPage` checks an already-settled response against
 *     the key that requested it. Intentional placeholder data (the prior
 *     confirmed page shown while a new page loads) is NOT settled data and must
 *     never be passed here.
 *
 * Neither helper performs I/O; they are pure so the page transition logic can be
 * unit-tested in isolation from the network and from TanStack Query.
 */

/** Explicit Village page size for the commons discovery list. */
export const TRANSCRIPT_PAGE_SIZE = 24;

/** The `/api/v1/transcripts` discovery endpoint, used in actionable messages. */
export const TRANSCRIPT_LIST_ENDPOINT = "/api/v1/transcripts";

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

/** Result of validating a settled discovery response against its request key. */
export type SettledPageValidation =
  | { ok: true }
  | {
      ok: false;
      message: string;
      requestedPage: number;
      requestedLimit: number;
      receivedPage: number;
      receivedLimit: number;
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

/**
 * Validate a *settled* discovery response before it is presented.
 *
 * A trustworthy response describes exactly the page and page size that its
 * request key asked for. A mismatch means the settled data does not belong to
 * the current intent, so it must not replace the confirmed rows already shown.
 *
 * @returns `{ ok: true }` when the response matches the request key, otherwise
 *   an actionable descriptor naming what was requested, what was received, and
 *   how the caller should recover.
 */
export function validateSettledTranscriptPage(input: {
  requestedPage: number;
  requestedLimit: number;
  responsePage: number;
  responseLimit: number;
}): SettledPageValidation {
  const { requestedPage, requestedLimit, responsePage, responseLimit } = input;
  if (responsePage === requestedPage && responseLimit === requestedLimit) {
    return { ok: true };
  }
  return {
    ok: false,
    requestedPage,
    requestedLimit,
    receivedPage: responsePage,
    receivedLimit: responseLimit,
    message:
      `Session list showed the wrong page. Requested page ${requestedPage} at ` +
      `${requestedLimit} per page from ${TRANSCRIPT_LIST_ENDPOINT} during the ` +
      `page transition, but the settled response described page ${responsePage} ` +
      `at ${responseLimit} per page. The previously confirmed rows are kept and ` +
      `were not replaced by this response. Retry the same page to reload it.`,
  };
}
