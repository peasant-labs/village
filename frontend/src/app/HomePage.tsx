"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { FolderOpen, Search } from "lucide-react";
import { useTranscriptPages } from "@/lib/queries/transcripts";
import { useGroupedTranscripts } from "@/lib/queries/helperGroups";
import { useGroups } from "@/lib/queries/groups";
import { useMyCollectiveContributions } from "@/lib/queries/collectives";
import { useMyStats } from "@/lib/queries/account";
import {
  ScopedContextContainerList,
  ScopedOwnerHelperGroups,
  helperGroupsByTranscript,
} from "@/components/transcript/ScopedHelperGroups";
import { useAuth } from "@/providers/AuthProvider";
import HomeTranscriptTable from "@/components/home/HomeTranscriptTable";
import HomeRail from "@/components/home/HomeRail";
import { Input, StatsStrip, TeachingEmptyState } from "@/lib/ft-ui";
import RequestFailureState from "@/components/RequestFailureState";
import MalformedProjectNotice from "@/components/MalformedProjectNotice";
import RetryButton from "@/components/RetryButton";
import { formatCompact, formatRecordedDuration, publishedAtDescending } from "@/lib/format";
import { childSessionsByParentID, groupChildSessions } from "@/lib/childSessions";
import { TRANSCRIPT_LIST_ENDPOINT } from "@/lib/transcriptPageRequest";
import type { TranscriptListItem } from "@/lib/types";

/**
 * The signed-in landing surface: the caller's own transcripts in a table, with
 * their totals above it and their collectives beside it.
 *
 * Every read is the caller's own and already served: the owner-scoped
 * transcript list (a page at a time, with search), `GET /users/me/stats` for
 * the totals, `GET /groups` for the collectives they belong to, and
 * `GET /users/me/collectives/contributions` for what is still waiting for a
 * collective's approval.
 */

/**
 * Whether the viewer's handle is known, still being chosen, or recorded as
 * chosen while blank. The page owes each a different answer, so they are a
 * closed set rather than a pair of booleans combined at each branch.
 */
type HandleState = "known" | "choosing" | "missing";

/** How many transcripts one page of the list asks for, and `load more` adds. */
export const HOME_PAGE_SIZE = 20;

/** How long typing has to pause before the list is asked again. */
const SEARCH_SETTLE_MS = 250;

/**
 * The rows a list of parents is shown in, most recently ACTIVE first.
 *
 * A row's own `published_at` is not what "recent" means once a row can hold the
 * sessions it started. A person's newest session is often one their last run
 * spawned, and that row is inside its parent's chip; ranking the parents by
 * their own timestamps alone would push that whole group down the list, where
 * the newest thing the person did would sit inside a chip far below where it
 * belongs.
 *
 * So a group is as recent as the newest row in it: the parent, or any session
 * it started. A row that started nothing is unaffected, and ranks exactly as it
 * did before.
 *
 * The comparison goes through the shared `publishedAtDescending`, the one
 * recency rule this page uses, so a value that does not parse sorts last here
 * for the same reason it does everywhere else.
 */
export function mostRecentGroupFirst(
  rootItems: TranscriptListItem[],
  childSessions: Map<string, TranscriptListItem[]>,
): TranscriptListItem[] {
  const groupPublishedAt = (item: TranscriptListItem): string => {
    let newest = item.transcript.published_at;
    for (const started of childSessions.get(item.transcript.id) ?? []) {
      if (publishedAtDescending(started.transcript.published_at, newest) < 0) {
        newest = started.transcript.published_at;
      }
    }
    return newest;
  };
  return [...rootItems].sort((a, b) =>
    publishedAtDescending(groupPublishedAt(a), groupPublishedAt(b)),
  );
}

/**
 * Most recently published first, through the one shared comparator.
 *
 * The server already answers newest first; the rows are ordered here as well so
 * the pages a person has loaded read as one list, and so a value that does not
 * parse sorts last instead of scrambling the rows around it.
 */
export function mostRecentFirst(items: TranscriptListItem[]): TranscriptListItem[] {
  return [...items].sort((a, b) =>
    publishedAtDescending(a.transcript.published_at, b.transcript.published_at),
  );
}

function counted(n: number, one: string, many: string): { label: string; value: number } {
  return { label: n === 1 ? one : many, value: n };
}

export default function HomePage() {
  const { user } = useAuth();
  const username = user?.github_username ?? "";
  // A blank owner filter is DROPPED by the list handler, which would answer
  // with the whole commons under a heading that says "your", so nothing is
  // asked until the viewer's handle is known.
  //
  // The three states are named rather than derived from two booleans at each
  // use, because they are answered differently and only one of them is a
  // waiting state. `choosing` is on its way to `/welcome` — the handle gate
  // reads `username_chosen`, so this page only has to hold still. `missing` is
  // an account that records having chosen a handle while carrying none: a
  // server contract violation nothing will resolve on its own.
  // A null user is not an account with a bad handle, it is no answer yet, and
  // it must never reach the accusation below. `RootPage` does not mount this
  // page without one, but the guard belongs with the state it describes rather
  // than with the only caller that happens to hold the line today.
  const handleState: HandleState =
    username.trim() !== "" ? "known" : user?.username_chosen === true ? "missing" : "choosing";

  if (handleState === "choosing") {
    return (
      <div className="iu-page animate-fade-up">
        <div className="h-8 w-64 animate-shimmer" />
        <div className="h-48 animate-shimmer" />
        <div className="h-48 animate-shimmer" />
      </div>
    );
  }

  // Nothing is coming to fix this one: the redirect that rescues an unchosen
  // handle does not fire for an account that records having chosen one.
  if (handleState === "missing") {
    return (
      <div className="iu-page" data-testid="home-page-no-handle">
        <div
          role="alert"
          className="border border-danger/40 bg-danger-soft px-4 py-3 text-danger"
        >
          <p className="font-medium">your account has no handle</p>
          <p className="mt-1">
            your transcripts are stored under your handle, and this account is recorded as having
            chosen one while carrying none, so they cannot be looked up. nothing has been lost.
            sign out and back in; if the page still says this, the account needs a maintainer.
          </p>
        </div>
      </div>
    );
  }

  // One mount per person: a different handle is a different home, and nothing
  // one person typed, loaded or saw fail may carry over to the next.
  return <HomeBody key={username} username={username} />;
}

function HomeBody({ username }: { username: string }) {
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState("");
  useEffect(() => {
    const settled = draft.trim();
    if (settled === query) return;
    const timer = setTimeout(() => setQuery(settled), SEARCH_SETTLE_MS);
    return () => clearTimeout(timer);
  }, [draft, query]);

  const listParams: Record<string, string> =
    query === "" ? { owner: username } : { owner: username, q: query };
  const list = useTranscriptPages(listParams, HOME_PAGE_SIZE);

  // The grouped response is the server's own fold of saved helper threads onto
  // their owner rows. It is a second, independent read of the same route: the
  // paged list above still owns the rows, the ordinary child chip and the
  // paging, and the helper groups ride on whichever of those rows the server
  // placed them on. A grouped read that fails contributes no helper groups; the
  // rows render exactly as they would without it.
  const grouped = useGroupedTranscripts({ owner: username, limit: "100" });
  const groupedItems = grouped.data?.items ?? [];
  const helperGroups = helperGroupsByTranscript(groupedItems);
  const refreshGrouped = grouped.refreshOrigin;

  const stats = useMyStats(username, true);
  const collectives = useGroups();
  const contributions = useMyCollectiveContributions(true);

  // Rows of the list's OWN answer. While a new search loads, the previous
  // search's rows stay on screen as a placeholder; they describe a different
  // question, so no failure or count below may be read from them.
  const own = list.isPlaceholderData ? undefined : list.data;
  const listKey = `${username}\u0000${query}`;

  // The failure has to OUTLIVE its own retry. With no rows to fall back on, a
  // refetch puts the query back into its pending state, so `isError` goes false
  // while the retry is in flight: reading it directly would drop the panel for
  // a loading state the moment the button was pressed, taking the alert, the
  // focus and the retry with it. The cause is therefore remembered until a
  // request actually succeeds, together with the question it answered, so a
  // failure recorded for one search never describes another.
  //
  // A failed `load more` is not a failed list: the rows already shown are
  // still right, and that failure is said beside the button instead.
  const listFailed = list.isError && !list.isFetchNextPageError;
  const reportedCause = listFailed
    ? list.error instanceof Error
      ? list.error.message
      : "an unknown error"
    : null;
  const [remembered, setRemembered] = useState<{ key: string; cause: string } | null>(null);
  if (reportedCause !== null && (reportedCause !== remembered?.cause || listKey !== remembered?.key)) {
    setRemembered({ key: listKey, cause: reportedCause });
  }
  // Cleared only by a request that actually answered.
  if (!list.isError && own != null && remembered !== null) {
    setRemembered(null);
  }
  const failureCause = remembered?.key === listKey ? remembered.cause : null;
  const failed = failureCause !== null;

  // A retry that fails again produces the SAME alert text, which a screen
  // reader will not announce a second time and a sighted reader cannot
  // distinguish from a button that did nothing. So the request being in flight
  // is its own visible state on the control, and its own polite announcement.
  const retrying = failed && list.isFetching && !list.isFetchingNextPage;
  const retryText = retrying ? "retrying" : "retry";

  const pages = list.data?.pages ?? [];
  const loaded = pages.flatMap((page) => page.transcripts);
  const total = pages[0]?.total ?? 0;
  const searching = list.isPlaceholderData;

  // Grouped BEFORE ranking, so a row is a session the viewer ran rather than a
  // session one of their runs started. A session started from inside another
  // one is published as its own transcript; without the fold a single busy run
  // would fill the list with its own offspring. A row whose parent is not
  // loaded keeps its ordinary place, so nothing a person published falls out.
  const grouping = groupChildSessions(mostRecentFirst(loaded));
  const childSessions = childSessionsByParentID(grouping);
  const rows = mostRecentGroupFirst(grouping.rootItems, childSessions);
  const malformed = loaded.filter((item) => !item.transcript.project_hash).length;

  const [now] = useState(() => Date.now());

  // Mounted on every branch below, not inside one of them: a polite region has
  // to exist BEFORE its content changes for the change to be announced.
  const statusText = retrying
    ? "reloading your transcripts"
    : searching
      ? "searching your transcripts"
      : list.isFetchingNextPage
        ? "loading more transcripts"
        : "";

  const statItems =
    stats.data == null
      ? []
      : [
          counted(stats.data.total_transcripts, "transcript", "transcripts"),
          ...(collectives.data != null
            ? [counted(collectives.data.length, "collective", "collectives")]
            : []),
          counted(stats.data.pull_request_count, "pull request", "pull requests"),
          { label: "tokens", value: formatCompact(stats.data.total_tokens) },
          { label: "recorded", value: formatRecordedDuration(stats.data.total_duration_ms) },
        ];
  const libraryEmpty = own != null && total === 0 && query === "";
  const showStats = statItems.length > 0 && stats.data!.total_transcripts > 0;

  let listRegion: React.ReactNode;
  if (failed && own == null && !searching) {
    // A failed request is NOT an empty library. The teaching empty state here
    // would tell somebody with a shelf full of transcripts that they have
    // published nothing. The failure panel says what failed and offers the same
    // request again.
    listRegion = (
      <div data-testid="home-page-error">
        <RequestFailureState
          title="your transcripts could not be loaded"
          message={
            `your own published transcripts could not be loaded from ` +
            `${TRANSCRIPT_LIST_ENDPOINT}. a failed request is not an empty ` +
            `library, and nothing has been deleted. retry to load it again. ` +
            `the request reported: ${failureCause}.`
          }
          onRetry={() => list.refetch()}
          retryLabel={retryText}
          retryDisabled={retrying}
        />
      </div>
    );
  } else if (list.data == null) {
    listRegion = (
      <div className="flex flex-col gap-[var(--sp-2)]" data-testid="home-list-loading" aria-hidden="true">
        <div className="h-10 animate-shimmer" />
        <div className="h-14 animate-shimmer" />
        <div className="h-14 animate-shimmer" />
      </div>
    );
  } else if (libraryEmpty) {
    listRegion = (
      <div data-testid="home-empty-state" className="border border-rule bg-surface">
        <TeachingEmptyState
          icon={FolderOpen}
          title="nothing published yet"
          body="sessions you publish from peasant appear here, with who can read each one. sign peasant in to village on your computer, then publish a session from it."
          command="peasant village login"
          privacy={null}
          style={{ border: "none", background: "transparent" }}
        />
        <div className="px-6 pb-6">
          <Link href="/publish" className="cmg-pr-link">
            how to publish from peasant
          </Link>
        </div>
      </div>
    );
  } else if (rows.length === 0) {
    listRegion = (
      <div data-testid="home-no-match" className="flex flex-col items-start gap-[var(--sp-2)] border border-rule bg-surface p-[var(--sp-4)]">
        <p className="m-0">no transcript you published matches &ldquo;{query}&rdquo;.</p>
        <button type="button" className="btn btn-secondary btn-sm" onClick={() => setDraft("")}>
          clear search
        </button>
      </div>
    );
  } else {
    const more = Math.min(HOME_PAGE_SIZE, Math.max(total - loaded.length, 0));
    listRegion = (
      <div className="flex flex-col gap-[var(--sp-3)]" data-testid="home-transcripts" aria-busy={searching || undefined}>
        {failed && (
          // The rows below are the last ones the server confirmed. They are
          // kept deliberately: a failed refresh is not news that the library
          // shrank.
          <div
            data-testid="home-stale-notice"
            className="border border-rule bg-surface px-4 py-3 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3"
          >
            {/* Only the sentence is the alert. The control sits outside it
                because its label changes while a retry is in flight, and an
                atomic alert re-announces the whole notice on that change. */}
            <p role="alert" className="m-0 text-ink-2">
              these transcripts could not be refreshed, so they are the ones last loaded and may be
              out of date. the request reported: {failureCause}.
            </p>
            <RetryButton
              label={retryText}
              busy={retrying}
              onRetry={() => list.refetch()}
              testId="home-stale-retry"
            />
          </div>
        )}
        <MalformedProjectNotice
          count={malformed}
          testId="home-malformed-notice"
          consequence="They are still listed below, without a link to a project page."
        />
        <div className={searching ? "opacity-60 transition-opacity" : undefined}>
          <HomeTranscriptTable
            rows={rows}
            childSessions={childSessions}
            viewerUsername={username}
            now={now}
            helperGroupSlot={(item) => (
              <ScopedOwnerHelperGroups
                groups={helperGroups.get(item.transcript.id)}
                onRefreshOrigin={refreshGrouped}
              />
            )}
          />
          <ScopedContextContainerList items={groupedItems} onRefreshOrigin={refreshGrouped} />
        </div>
        <div className="iu-page-foot">
          <span className="iu-page-count" data-testid="home-count">
            <span className="tnum">{loaded.length.toLocaleString("en-US")}</span> of{" "}
            <span className="tnum">{total.toLocaleString("en-US")}</span>{" "}
            {total === 1 ? "transcript" : "transcripts"}
          </span>
          {list.hasNextPage && !searching && (
            <span className="inline-flex items-center gap-[var(--sp-2)]">
              {list.isFetchNextPageError && (
                <span role="alert" className="cmg-note" data-testid="home-load-more-failed">
                  the next transcripts could not be loaded:{" "}
                  {list.error instanceof Error ? list.error.message : "an unknown error"}.
                </span>
              )}
              <RetryButton
                label={list.isFetchingNextPage ? "loading more" : `show ${more} more`}
                busy={list.isFetchingNextPage}
                onRetry={() => list.fetchNextPage()}
                testId="home-load-more"
              />
            </span>
          )}
        </div>
      </div>
    );
  }

  return (
    <div className="iu-page animate-fade-up" data-testid="home-page">
      <p role="status" aria-live="polite" className="sr-only" data-testid="home-status">
        {statusText}
      </p>

      <header className="iu-page-head">
        <h1 className="iu-page-title">your transcripts</h1>
        <p className="iu-page-sub">sessions you published from peasant, and who can read each one.</p>
      </header>

      {showStats ? (
        <div data-testid="home-stats">
          <StatsStrip items={statItems} label="your transcripts in numbers" />
        </div>
      ) : stats.isError ? (
        <p className="cmg-note" data-testid="home-stats-failed">
          your totals could not be loaded.
        </p>
      ) : null}

      <div className="cmg-home">
        <div className="cmg-home-main">
          {!libraryEmpty && (
            <div className="iu-page-search" role="search">
              <Input
                label="search your transcripts"
                type="search"
                iconLeft={Search}
                placeholder="search your transcripts"
                value={draft}
                onChange={(event) => setDraft(event.target.value)}
              />
            </div>
          )}
          {listRegion}
        </div>
        <HomeRail
          collectives={{
            data: collectives.data,
            isError: collectives.isError,
            isFetching: collectives.isFetching,
            refetch: collectives.refetch,
          }}
          contributions={{
            data: contributions.data,
            isError: contributions.isError,
            isFetching: contributions.isFetching,
            refetch: contributions.refetch,
          }}
        />
      </div>
    </div>
  );
}
