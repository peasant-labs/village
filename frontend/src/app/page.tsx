"use client";

import { useMemo, useState } from "react";
import { SearchX } from "lucide-react";
import { useRouter } from "next/navigation";
import { useTranscripts } from "@/lib/queries/transcripts";
import { useSearchCollectives } from "@/lib/queries/groups";
import { usePopularTags } from "@/lib/queries/tags";
import { adaptExplore } from "@/lib/adapters/explore";
import {
  TRANSCRIPT_LIST_ENDPOINT,
  TRANSCRIPT_PAGE_SIZE,
  buildTranscriptListParams,
  validateSettledTranscriptPage,
  type ExploreFilters,
} from "@/lib/transcriptPageRequest";
import type { TranscriptListResponse } from "@/lib/types";
import { Explore } from "@peasant-labs/fairtrade/commons";

const DEFAULT_FILTERS: ExploreFilters = {
  query: "",
  provider: "all",
  topics: [],
  order: "recent",
  page: 1,
};

export default function ExplorePage() {
  const router = useRouter();
  const [filters, setFilters] = useState<ExploreFilters>(DEFAULT_FILTERS);

  const params = useMemo(() => buildTranscriptListParams(filters), [filters]);

  const {
    data,
    isLoading,
    isError,
    error,
    isPlaceholderData,
    isFetching,
    refetch,
  } = useTranscripts(params);
  const { data: collData } = useSearchCollectives(filters.query);
  const { data: popularTags } = usePopularTags(15);

  // `filters.page` is the requested navigation intent. Intentional placeholder
  // data is the prior confirmed page shown while a new page loads; it is exempt
  // from settled-response validation. Only genuinely settled data (not a
  // placeholder, no fetch in flight) is validated against the requested key.
  const requestedPage = filters.page;
  const settled = data != null && !isPlaceholderData && !isFetching && !isError;

  const mismatch = useMemo(() => {
    if (!settled || data == null) return null;
    const result = validateSettledTranscriptPage({
      requestedPage,
      requestedLimit: TRANSCRIPT_PAGE_SIZE,
      responsePage: data.page,
      responseLimit: data.limit,
    });
    return result.ok ? null : result;
  }, [settled, data, requestedPage]);

  // Remember the last confirmed (settled + validated) response so a dishonest
  // settled response can retain honest prior rows instead of showing mismatched
  // content or silently falling back to page 1. Storing derived state during
  // render (guarded so it converges) is the React-recommended alternative to an
  // effect here: it keeps the retained rows in sync without a cascading render.
  const [lastConfirmed, setLastConfirmed] = useState<TranscriptListResponse | null>(null);
  if (settled && data != null && mismatch == null && data !== lastConfirmed) {
    setLastConfirmed(data);
  }

  // On a validation mismatch, present the retained confirmed rows rather than the
  // untrustworthy settled response. Otherwise present the current query data
  // (which, during a page-only request, is the retained placeholder page).
  const displayData: TranscriptListResponse | null =
    mismatch != null
      ? lastConfirmed
      : data ?? lastConfirmed;

  const payload = useMemo(() => {
    if (!displayData) return null;
    return adaptExplore(
      displayData,
      collData ?? { collectives: [] },
      popularTags ?? []
    );
  }, [collData, displayData, popularTags]);

  const confirmedPage = displayData?.page ?? null;
  const total = displayData?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / TRANSCRIPT_PAGE_SIZE));
  // Results are busy whenever a superseding request is in flight (including while
  // prior rows are shown as placeholder data).
  const busy = isFetching || isPlaceholderData;

  // A page transition that fails (exhausted retry) or returns a dishonest page is
  // reported without discarding prior rows; abort/supersession is never an error.
  const pageTransitionError: string | null =
    mismatch != null
      ? mismatch.message
      : isError && displayData != null
        ? `Failed to load page ${requestedPage} of the session list from ` +
          `${TRANSCRIPT_LIST_ENDPOINT}. The previously confirmed rows are kept. ` +
          `${error instanceof Error ? error.message : "Unknown error."} ` +
          `Retry the same page to reload it.`
        : null;

  let statusMessage: string;
  if (isLoading) {
    statusMessage = "loading session list";
  } else if (pageTransitionError != null) {
    statusMessage = pageTransitionError;
  } else if (busy && confirmedPage != null && confirmedPage !== requestedPage) {
    statusMessage = `loading page ${requestedPage}; showing page ${confirmedPage} until it arrives`;
  } else if (busy) {
    statusMessage = `loading page ${requestedPage}`;
  } else if (confirmedPage != null) {
    statusMessage = `page ${confirmedPage} of ${totalPages} loaded`;
  } else {
    statusMessage = "";
  }

  // Initial load failure with no prior rows to retain: full error surface.
  if (isError && displayData == null && !isLoading) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center">
          <SearchX size={28} className="text-ink-4" />
          <p className="text-sm font-medium text-ink">Failed to load transcripts</p>
          <p className="text-[13px] text-ink-3 max-w-sm">
            {error instanceof Error ? error.message : "The commons browse surface could not load."}
          </p>
          <button
            type="button"
            className="btn btn-secondary btn-sm"
            onClick={() => refetch()}
          >
            retry
          </button>
        </div>
      </div>
    );
  }

  if (isLoading || !payload) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="flex flex-col gap-1">
          <div className="h-8 w-72 animate-shimmer" />
          <div className="h-4 w-96 animate-shimmer" />
        </div>
        <div className="grid grid-cols-1 lg:grid-cols-[16rem_minmax(0,1fr)] gap-6">
          <div className="h-[420px] animate-shimmer" />
          <div className="flex flex-col gap-4">
            <div className="h-24 animate-shimmer" />
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="h-48 animate-shimmer" />
              <div className="h-48 animate-shimmer" />
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12">
      <p role="status" aria-live="polite" className="sr-only">
        {statusMessage}
      </p>
      {pageTransitionError != null && (
        <div
          role="alert"
          className="border border-rule bg-surface px-4 py-3 mb-4 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3"
        >
          <p className="text-[13px] text-ink-3">{pageTransitionError}</p>
          <button
            type="button"
            className="btn btn-secondary btn-sm shrink-0"
            onClick={() => refetch()}
          >
            retry page {requestedPage}
          </button>
        </div>
      )}
      <div aria-busy={busy}>
        <Explore
          data={payload}
          onFiltersChange={setFilters}
          transcriptHref={(transcript) => `/transcripts/${transcript.id}`}
          profileHref={(owner) => `/users/${owner.githubUsername}`}
          collectiveHref={(collective) => `/groups/${collective.id}`}
          onOpenTranscript={(transcript) => router.push(`/transcripts/${transcript.id}`)}
          onOpenProfile={(owner) => router.push(`/users/${owner.githubUsername}`)}
          onOpenCollective={(collective) => router.push(`/groups/${collective.id}`)}
        />
      </div>
    </div>
  );
}
