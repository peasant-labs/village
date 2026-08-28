"use client";

import { useMemo, useState, type ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { useRouter } from "next/navigation";
import { useTranscripts } from "@/lib/queries/transcripts";
import { useSearchCollectives } from "@/lib/queries/groups";
import { usePopularTags } from "@/lib/queries/tags";
import { adaptExplore } from "@/lib/adapters/explore";
import {
  TRANSCRIPT_LIST_ENDPOINT,
  TRANSCRIPT_PAGE_SIZE,
  buildTranscriptListParams,
  type ExploreFilters,
} from "@/lib/transcriptPageRequest";
import type { TranscriptListResponse } from "@/lib/types";
import { Explore } from "@peasant-labs/fairtrade/commons";
import AgentSessionGroup from "@/components/transcript/AgentSessionGroup";
import RequestFailureState from "@/components/RequestFailureState";
import RetryButton from "@/components/RetryButton";

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

  // `filters.page` is the requested navigation intent. The query function is the
  // response trust boundary: data reaches TanStack's successful cache only after
  // every explicit page/limit value matches its request key. Placeholder data is
  // therefore always previously validated successful data.
  const requestedPage = filters.page;

  // Retain the last successful non-placeholder response for initial/transition
  // failures. This guarded render-phase update converges on the latest validated
  // object without an effect or a ref read during render.
  const [lastConfirmed, setLastConfirmed] = useState<TranscriptListResponse | null>(null);
  if (data != null && !isPlaceholderData && !isError && data !== lastConfirmed) {
    setLastConfirmed(data);
  }

  const displayData: TranscriptListResponse | null = data ?? lastConfirmed;

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
  const agentTotal = displayData?.agent_total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / TRANSCRIPT_PAGE_SIZE));

  // aria-busy reflects active work only. On a terminal error, or on a settled
  // rejection that is now awaiting a user retry, no request is in flight, so this
  // is false and the region is not announced as loading forever.
  const busy = isFetching;

  // One surface owns failure announcement. `failureMessage` is that surface's
  // text; it is never duplicated into the polite status region below.
  //
  // The failure has to OUTLIVE its own retry. With no rows to fall back on, a
  // refetch puts the query back into its pending state, so `isError` goes false
  // while the retry is in flight: reading it directly would drop this surface
  // for a loading skeleton the moment the button was pressed, taking the alert,
  // the focus and the control with it. The message is therefore remembered
  // until a request actually succeeds.
  const reportedFailure: string | null = isError
    ? `Failed to load page ${requestedPage} of the session list from ` +
      `${TRANSCRIPT_LIST_ENDPOINT}. ` +
      `${displayData != null ? "The previously confirmed rows are kept. " : ""}` +
      `${error instanceof Error ? error.message : "Unknown error."} ` +
      `Retry the same page to reload it.`
    : null;
  // Remembered WITH the request it belongs to. Asking for a different page is
  // not a retry of the one that failed, so the old message must not follow the
  // reader onto it: that request gets its own loading state and its own answer.
  const requestKey = JSON.stringify(params);
  const [remembered, setRemembered] = useState<{ key: string; message: string } | null>(null);
  if (reportedFailure !== null && reportedFailure !== remembered?.message) {
    setRemembered({ key: requestKey, message: reportedFailure });
  }
  if (!isError && data != null && remembered !== null) {
    setRemembered(null);
  }
  const failureMessage = remembered?.key === requestKey ? remembered.message : null;

  // While a retry is in flight the control says so and refuses further presses.
  // A retry that fails again renders the same words, so without this it cannot
  // be told from a button that did nothing.
  const retrying = failureMessage != null && isFetching;

  // The concise transient loading text, shared by the sr-only announcer and the
  // visible cue so both stay identical. When prior rows remain visible during a
  // page change it names both the requested page and the page still on screen.
  const busyMessage =
    confirmedPage != null && confirmedPage !== requestedPage
      ? `loading page ${requestedPage}; showing page ${confirmedPage} until it arrives`
      : `loading page ${requestedPage}`;

  // The polite live region carries only transient loading/loaded status; failure
  // is owned by the alert surface, so a failure is announced exactly once.
  let statusMessage: string;
  if (retrying) {
    statusMessage = `retrying page ${requestedPage}`;
  } else if (isLoading) {
    statusMessage = "loading session list";
  } else if (busy) {
    statusMessage = busyMessage;
  } else if (failureMessage != null) {
    statusMessage = retrying ? `retrying page ${requestedPage}` : "";
  } else if (confirmedPage != null) {
    statusMessage = `page ${confirmedPage} of ${totalPages} loaded`;
  } else {
    statusMessage = "";
  }

  // One label for both ways the same failure is offered: the full surface and
  // the notice above retained rows. Written out in two places, it would drift
  // the moment one of them is reworded.
  const retryLabel = retrying
    ? `retrying page ${requestedPage}`
    : `retry page ${requestedPage}`;

  const retryButton = (
    <RetryButton label={retryLabel} busy={retrying} onRetry={() => refetch()} />
  );

  let content: ReactNode;
  if (failureMessage != null && payload == null) {
    // A failure with no rows to retain (initial-load error, or a first-response
    // mismatch): the full error surface owns the announcement and offers an
    // exact-key retry instead of stalling on an endless skeleton. It is the
    // SHARED panel, so the home page cannot describe the same failure in
    // different words.
    content = (
      <RequestFailureState
        title="Failed to load transcripts"
        message={failureMessage}
        onRetry={() => refetch()}
        retryLabel={retryLabel}
        retryDisabled={retrying}
      />
    );
  } else if ((isLoading && failureMessage == null) || !payload) {
    content = (
      <div className="flex flex-col gap-6 animate-fade-up">
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
  } else {
    content = (
      <>
        {failureMessage != null && (
          <div
            className="border border-rule bg-surface px-4 py-3 mb-4 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3"
          >
            {/* Only the sentence is the alert. The control sits outside it
                because its label changes while a retry is in flight, and an
                alert is atomic: wrapping the button would re-announce the whole
                failure assertively on every press. */}
            <p role="alert" className="text-[13px] text-ink-3">{failureMessage}</p>
            {retryButton}
          </div>
        )}
        {busy && (
          // A concise visible loading cue so sighted users see that the newer page
          // is loading while the prior page stays on screen (the pager already
          // reflects the requested page). Icon plus text carry the meaning, never
          // colour alone; the spin is reduced-motion safe. It is aria-hidden so
          // the single sr-only polite region above remains the sole announcer and
          // the message is not read twice. It sits above the unchanged results, so
          // no row is remounted and focus is not moved.
          <div
            aria-hidden="true"
            data-testid="session-list-loading"
            className="flex items-center gap-2 px-4 py-2 mb-4 border border-rule bg-surface text-[13px] text-ink-3"
          >
            <Loader2 size={14} className="text-ink-3 motion-safe:animate-spin" aria-hidden="true" />
            <span className="mono tnum">{busyMessage}</span>
          </div>
        )}
        <div aria-busy={busy} data-testid="session-list-results">
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
          {/* Agent-driven sessions are excluded from the rows above by the
              server, which reports how many the same filters matched. The
              group sits at the end of the list so the browse results stay the
              sessions people wrote, with the rest one click away. */}
          {/* The group reuses the Explore surface's own body grid and results
              column rather than a copied width, so it lines up with the cards
              above it at every breakpoint and follows that layout if it ever
              changes. The first cell is the empty facet rail. */}
          <div className="cex-explore-body mt-4">
            <div aria-hidden="true" />
            <div className="cex-results">
              <AgentSessionGroup agentTotal={agentTotal} baseParams={params} />
            </div>
          </div>
        </div>
      </>
    );
  }

  // The polite status region is mounted persistently across every render branch
  // (loading, error, and results) so a live region always exists before its
  // content changes, which reliable announcement requires.
  return (
    <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12">
      <p
        role="status"
        aria-live="polite"
        className="sr-only"
        data-testid="session-list-status"
      >
        {statusMessage}
      </p>
      {content}
    </div>
  );
}
