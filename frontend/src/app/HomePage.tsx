"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { FileText, Search, Terminal } from "lucide-react";
import { useTranscripts } from "@/lib/queries/transcripts";
import { transcriptListFailureNote } from "@/lib/transcriptPageRequest";
import { useGroupedTranscriptsPaged } from "@/lib/queries/helperGroups";
import { useGroups } from "@/lib/queries/groups";
import { useMyCollectiveContributions } from "@/lib/queries/collectives";
import { useMyStats } from "@/lib/queries/account";
import {
  ScopedContextContainerList,
  ScopedOwnerHelperGroups,
  helperGroupsByTranscript,
} from "@/components/transcript/ScopedHelperGroups";
import { useAuth } from "@/providers/AuthProvider";
import AgentSessionGroup from "@/components/transcript/AgentSessionGroup";
import HomeWaiting from "@/components/home/HomeWaiting";
import DocsLink from "@/components/online/DocsLink";
import { DOCS_PUBLISH_URL } from "@/lib/appLinks";
import { OnEmpty, OnHead, OnPage } from "@/components/online/Page";
import { CheckRow, ViewMenu } from "@/components/online/ViewMenu";
import RequestFailureState from "@/components/RequestFailureState";
import MalformedProjectNotice from "@/components/MalformedProjectNotice";
import RetryButton from "@/components/RetryButton";
import { formatRelativeTime, publishedAtDescending } from "@/lib/format";
import {
  ColumnAlign,
  ProviderName,
  TranscriptLibraryTable,
  formatCount,
  formatInteger,
  groupKeysFor,
  type SavedView,
  type TranscriptLibraryColumn,
} from "@/lib/ft-ui";
import { isHarness } from "@/lib/harness";
import ChildSessionDisclosure from "@/components/transcript/ChildSessionDisclosure";
import type { TranscriptRowFact } from "@/components/transcript/TranscriptList";
import PullRequestCell from "@/components/home/PullRequestCell";
import {
  ALL_GROUP_BYS,
  ColumnId,
  GroupBy,
  NO_READERS,
  agentLabel,
  decodeLibraryView,
  groupKeysOf,
  groupLabelOf,
  loadHomeView,
  projectKey,
  projectLabel,
  projectOptions,
  readersOf,
  saveHomeView,
  writeLibraryViewToUrl,
  type ColumnId as ColumnIdType,
  type GroupBy as GroupByType,
  type HomeView,
  type LibraryUrlView,
} from "@/lib/homeView";
import { childSessionsByParentID, groupChildSessions } from "@/lib/childSessions";
import type { TranscriptListItem, TranscriptRow } from "@/lib/types";
import { transcriptHref } from "@/lib/transcriptContext";

/**
 * The signed-in landing surface: the caller's own transcripts, their totals,
 * the newest few as cards, and the shared transcript library table.
 *
 * Every read is the caller's own and already served: the owner-scoped
 * transcript list (one numbered page at a time, with search), `GET /users/me/stats`
 * for the totals, `GET /collectives` for the collectives they belong to, and
 * `GET /users/me/collectives/contributions` for what is still waiting for a
 * collective's approval. The collectives themselves are listed in the sidebar.
 *
 * The table is controlled. The URL owns the page, the page size, and the
 * grouping; this device owns the visible columns and the closed groups. The
 * host composes one saved view from both halves and reports changes back. No
 * host re-derives grouping or paging: the table owns group layout, column
 * visibility, and pager chrome.
 */

/**
 * The facts a session another session started states when its chip is opened.
 * No provider: the home list names what a session was about and where, not
 * which tool recorded it.
 */
export const HOME_STARTED_SESSION_FACTS: readonly TranscriptRowFact[] = ["branch", "date", "turns"];

/** How long typing has to pause before the list is asked again. */
const SEARCH_SETTLE_MS = 250;

/**
 * Whether the viewer's handle is known, still being chosen, or recorded as
 * chosen while blank. The page owes each a different answer, so they are a
 * closed set rather than a pair of booleans combined at each branch.
 */
type HandleState = "known" | "choosing" | "missing";

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

function counted(n: number, one: string, many: string): { label: string; value: number } {
  return { label: n === 1 ? one : many, value: n };
}

/** The last page `total` rows divide into at `perPage`, never below one. The one
 *  division both the clamp and the pager agree on. */
function lastPageIndex(total: number, perPage: number): number {
  return Math.max(1, Math.ceil(total / perPage));
}

export default function HomePage() {
  const { user } = useAuth();
  const username = user?.github_username ?? "";
  // A blank owner filter is DROPPED by the list handler, which would answer
  // with the whole commons under a heading that says "your", so nothing is
  // asked until the viewer's handle is known.
  const handleState: HandleState =
    username.trim() !== "" ? "known" : user?.username_chosen === true ? "missing" : "choosing";

  if (handleState === "choosing") {
    return (
      <OnPage>
        <div className="h-10 w-72 animate-shimmer" />
        <div className="h-48 animate-shimmer" />
        <div className="h-48 animate-shimmer" />
      </OnPage>
    );
  }

  // Nothing is coming to fix this one: the redirect that rescues an unchosen
  // handle does not fire for an account that records having chosen one.
  if (handleState === "missing") {
    return (
      <OnPage testId="home-page-no-handle">
        <OnHead title="your transcripts" />
        <div role="alert" className="on-notice" data-tone="danger">
          <p>
            <strong>your account has no handle.</strong> your transcripts are stored under your
            handle, and this account is recorded as having chosen one while carrying none, so they
            cannot be looked up. nothing has been lost. sign out and back in; if the page still says
            this, the account needs a maintainer.
          </p>
        </div>
      </OnPage>
    );
  }

  // One mount per person: a different handle is a different home, and nothing
  // one person typed, loaded or saw fail may carry over to the next.
  return <HomeBody key={username} username={username} />;
}

function HomeBody({ username }: { username: string }) {
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState("");

  // The URL half of the view. Read once on mount (the body only mounts in the
  // browser) and rewritten on every change, so a copied URL reproduces the data
  // view and the grouping. The device half is read from storage beside it.
  const [urlView, setUrlView] = useState<LibraryUrlView>(() =>
    decodeLibraryView(new URLSearchParams(window.location.search)),
  );
  const { page, perPage, groupBy } = urlView;

  const [view, setView] = useState<HomeView>(loadHomeView);

  const applyUrlView = (next: LibraryUrlView) => {
    setUrlView(next);
    writeLibraryViewToUrl(next);
  };

  // Every settled page's rows, keyed by its page number. A continuation row is
  // derived from the page immediately before the one on screen, so a return to
  // an earlier page cannot inherit the page it came back from.
  const [pageRows, setPageRows] = useState<ReadonlyMap<number, { page: number; rows: TranscriptListItem[] }>>(
    () => new Map(),
  );
  const [recordedData, setRecordedData] = useState<NonNullable<typeof list.data> | null>(null);
  const clearPageHistory = () => setPageRows(new Map());

  // Typing settles into the query the list is asked. A changed question resets
  // the traversal to page one and forgets the page history: groups keyed to a
  // different result set cannot continue this one.
  useEffect(() => {
    const settled = draft.trim();
    if (settled === query) return;
    const timer = setTimeout(() => {
      setQuery(settled);
      setUrlView((current) => {
        if (current.page === 1) return current;
        const next = { ...current, page: 1 };
        writeLibraryViewToUrl(next);
        return next;
      });
      clearPageHistory();
    }, SEARCH_SETTLE_MS);
    return () => clearTimeout(timer);
  }, [draft, query]);

  // The projects this visit hides. Not remembered: a project hidden last week
  // must never make a newly published session look missing.
  const [hiddenProjects, setHiddenProjects] = useState<ReadonlySet<string>>(() => new Set());

  const listParams: Record<string, string> = {
    owner: username,
    page: String(page),
    per_page: String(perPage),
    ...(query === "" ? {} : { q: query }),
  };
  const list = useTranscripts(listParams);

  // Two-step client clamp. The server echoes the requested page and answers an
  // out-of-range one with no rows; it never clamps. A cold URL therefore asks
  // for the URL page first, reads the echoed total, and asks for the last page
  // in a second request.
  //
  // The adjustment happens during render, guarded by the condition that will
  // stop being true once the request intent moves: React re-renders immediately
  // with the last page, and the next request carries it. An effect would render
  // the empty page once before correcting it, and a synchronous setState in an
  // effect is the cascading render the hook rules forbid.
  const observedLast = lastPageIndex(list.data?.total ?? 0, perPage);
  if (list.data != null && list.data.page > observedLast && page !== observedLast) {
    setUrlView({ page: observedLast, perPage, groupBy });
  }

  // Once a reachable page is in hand, the address names it. This is an external
  // system write (the browser's URL), not state: a copied address reproduces a
  // page the server served, including the one a clamp settled on. An
  // out-of-range response is skipped because the render above is already moving
  // the request intent to the last page.
  useEffect(() => {
    // Placeholder data is the PREVIOUS page's object, not this page's answer.
    // Writing it back after a page-size change (which resets to page one) would
    // put the old page number back in the address.
    if (list.data == null || list.isPlaceholderData) return;
    const last = lastPageIndex(list.data.total, perPage);
    if (list.data.page > last) return;
    writeLibraryViewToUrl({ page: list.data.page, perPage, groupBy });
  }, [list.data, list.isPlaceholderData, perPage, groupBy]);

  // The grouped response is the server's own fold of saved helper threads onto
  // their owner rows. It is a second, independent read of the same route: the
  // paged list above still owns the rows and the paging, and the helper groups
  // ride on whichever of those rows the server placed them on. A grouped read
  // that fails contributes no helper groups; the rows render exactly as they
  // would without it. The named explicit continuation stays on the collective
  // and user pages; the Library has no continuation beyond its numbered pages.
  const grouped = useGroupedTranscriptsPaged({ owner: username, limit: "100" });
  const groupedItems = grouped.items;
  const helperGroups = helperGroupsByTranscript(groupedItems);
  const refreshGrouped = grouped.refreshOrigin;

  const stats = useMyStats(username, true);
  const collectives = useGroups();
  const contributions = useMyCollectiveContributions(true);

  // Rows of the list's OWN answer. While a new search or page loads, the
  // previous response stays on screen as a placeholder; it describes a
  // different question, so no failure or count below may be read from it.
  const own = list.isPlaceholderData ? undefined : list.data;
  const listKey = `${username}\u0000${query}\u0000${page}`;

  // The failure has to OUTLIVE its own retry. With no rows to fall back on, a
  // refetch puts the query back into its pending state, so `isError` goes false
  // while the retry is in flight: reading it directly would drop the panel for
  // a loading state the moment the button was pressed, taking the alert, the
  // focus and the retry with it. The cause is therefore remembered until a
  // request actually succeeds, together with the question it answered, so a
  // failure recorded for one question never describes another.
  const listFailed = list.isError;
  const reportedCause = listFailed ? (transcriptListFailureNote(list.error) ?? "") : null;
  const [remembered, setRemembered] = useState<{ key: string; cause: string } | null>(null);
  if (reportedCause !== null && (remembered?.key !== listKey || remembered?.cause !== reportedCause)) {
    setRemembered({ key: listKey, cause: reportedCause });
  }
  if (!list.isError && own != null && remembered !== null) {
    setRemembered(null);
  }
  const failureCause = remembered?.key === listKey ? remembered.cause : null;
  const failed = failureCause !== null;

  const retrying = failed && list.isFetching;
  const retryText = retrying ? "retrying" : "retry";

  const data = list.data;
  const loaded = data?.transcripts ?? [];
  const nextItems = data?.next_items ?? [];
  const total = data?.total ?? 0;
  const agentTotal = data?.agent_total ?? 0;
  const lastPage = lastPageIndex(total, perPage);
  // The first response to an out-of-range cold URL has no rows to show; the
  // clamp effect is already asking for the last page. Render neither an empty
  // page nor a "no transcripts" answer while that second request is in flight.
  const awaitingClamp = data != null && data.page > lastPage;
  const searching = list.isPlaceholderData;

  // Grouped BEFORE ranking, so a row is a session the viewer ran rather than a
  // session one of their runs started.
  const grouping = groupChildSessions(loaded);
  const childSessions = childSessionsByParentID(grouping);
  const rows = mostRecentGroupFirst(grouping.rootItems, childSessions);
  const malformed = loaded.filter((item) => !item.transcript.project_hash).length;

  const projects = projectOptions(rows);
  const visibleRows =
    hiddenProjects.size === 0 ? rows : rows.filter((item) => !hiddenProjects.has(projectKey(item)));

  // The following page's group keys: a group with rows there is marked at the
  // end of its block. `nextItems` is the server's own following page, so no host
  // derives a group the server did not serve.
  const nextGroupKeys =
    nextItems.length > 0 ? groupKeysFor(nextItems, (row) => groupKeysOf(row, groupBy)) : null;
  // A group that began on the page immediately before this one opens with one
  // compact continuation row. A cold URL, or a return to an earlier page, has
  // no adjacent predecessor held, so its groups keep full headers.
  const settledPage = data != null && !list.isPlaceholderData ? data.page : null;
  const priorPage = settledPage == null ? undefined : pageRows.get(settledPage - 1);
  const continuedRows =
    priorPage !== undefined && settledPage !== null && priorPage.page === settledPage - 1
      ? priorPage.rows
      : null;
  const continuedGroupKeys =
    continuedRows !== null && continuedRows.length > 0
      ? groupKeysFor(continuedRows, (row) => groupKeysOf(row, groupBy))
      : null;

  // The label map resolves a project hash (or a harness id) to what the row
  // shows. It is built once for the page and its following page, so a group
  // header and the row's own cell never disagree.
  const groupLabels = new Map<string, string>();
  for (const row of [...visibleRows, ...nextItems]) {
    for (const key of groupKeysOf(row, groupBy)) {
      const label =
        groupBy === GroupBy.Project
          ? projectLabel(row)
          : groupBy === GroupBy.Agent
            ? agentLabel(row)
            : key;
      groupLabels.set(`${groupBy.wire}:${key}`, label);
    }
  }

  const [now] = useState(() => Date.now());

  // Record each settled page once its answer is in hand. Placeholder data is
  // the previous page's object, not an answer, so it is not recorded. The
  // guarded render update is React's "adjust state when a response changes"
  // pattern; an effect would have to set state synchronously, which the hook
  // rules forbid.
  if (data != null && !list.isPlaceholderData && data !== recordedData) {
    setRecordedData(data);
    setPageRows((current) => new Map(current).set(data.page, { page: data.page, rows: data.transcripts }));
  }

  // Mounted on every branch below, not inside one of them: a polite region has
  // to exist BEFORE its content changes for the change to be announced.
  const statusText = retrying
    ? "reloading your transcripts"
    : searching
      ? "searching your transcripts"
      : "";

  const statItems =
    stats.data == null
      ? []
      : [
          counted(stats.data.total_transcripts, "transcript", "transcripts"),
          ...(collectives.data != null
            ? [counted(collectives.data.length, "collective", "collectives")]
            : []),
          counted(stats.data.pull_request_count, "pull request linked", "pull requests linked"),
          { label: "tokens recorded", value: formatCount(stats.data.total_tokens) },
        ];
  const libraryEmpty = own != null && total === 0 && agentTotal === 0 && query === "";
  const showStats = statItems.length > 0 && stats.data!.total_transcripts > 0;

  const toggleProject = (key: string) => {
    const next = new Set(hiddenProjects);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    setHiddenProjects(next);
  };
  const shownProjects = projects.filter((p) => !hiddenProjects.has(p.key)).length;
  const projectsLabel =
    shownProjects === projects.length ? "all" : shownProjects === 0 ? "none" : `${shownProjects} of ${projects.length}`;

  const goToPage = (index: number) => {
    const target = Math.min(Math.max(1, Math.floor(index)), lastPage);
    if (target === page) return;
    applyUrlView({ page: target, perPage, groupBy });
  };

  const changePageSize = (size: LibraryUrlView["perPage"]) => {
    // Page numbers name different row sets at a different size, so no page the
    // client held is adjacent to anything on the new size.
    clearPageHistory();
    applyUrlView({ page: 1, perPage: size, groupBy });
  };

  const changeView = (next: SavedView<GroupByType, ColumnIdType>) => {
    const groupingChanged = next.groupBy !== groupBy;
    const nextPage = groupingChanged ? 1 : page;
    // A closed id belongs to the grouping that named it: the table reports the
    // set unchanged, and the host clears it when the grouping changes, so a
    // group closed under projects cannot close an unrelated agent group.
    const deviceView: HomeView = {
      columns: next.visibleColumns,
      collapsed: groupingChanged ? [] : [...next.collapsed],
    };
    setView(deviceView);
    saveHomeView(deviceView);
    // The view is a new arrangement; a continuation from the old one is not
    // evidence about this one.
    clearPageHistory();
    if (groupingChanged || nextPage !== page) {
      applyUrlView({ page: nextPage, perPage, groupBy: next.groupBy });
    }
  };

  const columns: readonly TranscriptLibraryColumn<TranscriptListItem, ColumnIdType>[] = [
    {
      id: ColumnId.Transcript,
      label: "transcript",
      align: ColumnAlign.Start,
      render: (item) => {
        const t = item.transcript;
        const started = childSessions.get(t.id);
        return (
          <div className="on-cell-title" data-testid="home-transcript-row" data-transcript-id={t.id}>
            <Link href={transcriptHref(t.id, { kind: "home" })}>{t.title || "untitled"}</Link>
            {helperGroupSlot(item)}
            {started !== undefined && started.length > 0 && (
              <ChildSessionDisclosure
                parentTranscriptID={t.id}
                childSessions={started}
                hideOwner
                facts={HOME_STARTED_SESSION_FACTS}
                helperGroupSlot={helperGroupSlot}
                context={{ kind: "home" }}
              />
            )}
          </div>
        );
      },
    },
    {
      id: ColumnId.Project,
      label: ColumnId.Project.label,
      align: ColumnAlign.Start,
      render: (item) => {
        const t = item.transcript;
        const branch = t.git_branch?.trim() ?? "";
        // A project is routed on its hash. A row that arrived without one is a
        // server contract violation: it is still listed, and its project is
        // named but not linked, never folded into a made-up project.
        return (
          <>
            {t.project_hash ? (
              <Link
                href={`/users/${encodeURIComponent(username)}/projects/${t.project_hash}`}
                className="on-cell-link"
              >
                {t.project_display_name}
              </Link>
            ) : (
              <span>{t.project_display_name}</span>
            )}
            {branch !== "" && <span className="on-sub">{branch}</span>}
          </>
        );
      },
    },
    {
      id: ColumnId.Readers,
      label: ColumnId.Readers.label,
      align: ColumnAlign.Start,
      render: (item) => {
        const readers = readersOf(item);
        return readers.length > 0 ? (
          <span className="on-readers">
            {readers.map((name) => (
              <span key={name}>{name}</span>
            ))}
          </span>
        ) : (
          <span className="on-none">{NO_READERS}</span>
        );
      },
    },
    {
      id: ColumnId.Agent,
      label: ColumnId.Agent.label,
      align: ColumnAlign.Start,
      render: (item) =>
        isHarness(item.transcript.model_provider) ? (
          <ProviderName harness={item.transcript.model_provider} className="on-agent" />
        ) : (
          <span className="on-agent">{agentLabel(item)}</span>
        ),
    },
    {
      id: ColumnId.PullRequests,
      label: ColumnId.PullRequests.label,
      align: ColumnAlign.Start,
      render: (item) => (
        <PullRequestCell transcriptID={item.transcript.id} summary={item.pull_requests} />
      ),
    },
    {
      id: ColumnId.Turns,
      label: ColumnId.Turns.label,
      align: ColumnAlign.End,
      numeric: true,
      render: (item) =>
        item.transcript.turn_count == null ? (
          <span className="on-none">not counted</span>
        ) : (
          item.transcript.turn_count.toLocaleString("en-US")
        ),
    },
    {
      id: ColumnId.When,
      label: ColumnId.When.label,
      align: ColumnAlign.End,
      numeric: true,
      render: (item) => (
        <time dateTime={item.transcript.published_at}>
          {formatRelativeTime(item.transcript.published_at, now)}
        </time>
      ),
    },
  ];

  function helperGroupSlot(item: TranscriptRow) {
    return (
      <ScopedOwnerHelperGroups
        groups={helperGroups.get(item.transcript.id)}
        onRefreshOrigin={refreshGrouped}
      />
    );
  }

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
            `${failureCause ? `${failureCause}. ` : ""}nothing has been deleted. retry to load them again.`
          }
          onRetry={() => list.refetch()}
          retryLabel={retryText}
          retryDisabled={retrying}
        />
      </div>
    );
  } else if (list.data == null || awaitingClamp) {
    listRegion = (
      <div className="flex flex-col gap-[var(--sp-2)]" data-testid="home-list-loading" aria-hidden="true">
        <div className="h-11 w-full max-w-[40rem] animate-shimmer" />
        <div className="h-14 animate-shimmer" />
        <div className="h-14 animate-shimmer" />
        <div className="h-14 animate-shimmer" />
      </div>
    );
  } else if (total === 0 && agentTotal > 0 && query === "") {
    listRegion = (
      <div className="on-notice on-agent-library-note" data-testid="home-agent-only">
        <p>your published agent sessions are grouped below.</p>
        <p>sessions with your own prompts appear in the main library.</p>
      </div>
    );
  } else if (libraryEmpty) {
    listRegion = (
      <div className="on-section" data-testid="home-empty-state">
        <OnEmpty
          icon={<Terminal />}
          title="publish the session behind your next change"
          body="open a session in peasant, review what will leave your machine, then choose the collectives that can read it."
          actions={<DocsLink href={DOCS_PUBLISH_URL}>how to publish</DocsLink>}
        />
        {collectives.data != null && collectives.data.length === 0 && (
          <div className="on-row" data-testid="home-no-collective">
            <span className="on-muted">not in a collective yet?</span>
            <Link href="/collectives" className="btn btn-primary">
              new collective
            </Link>
            <Link href="/discover" className="btn btn-secondary">
              discover
            </Link>
          </div>
        )}
      </div>
    );
  } else if (rows.length === 0) {
    listRegion = (
      <div data-testid="home-no-match" className="on-nomatch">
        <b>no transcript you published matches &ldquo;{query}&rdquo;</b>
        <button type="button" className="btn btn-secondary" onClick={() => setDraft("")}>
          clear search
        </button>
      </div>
    );
  } else {
    listRegion = (
      <div className="on-section" data-testid="home-transcripts" aria-busy={searching || undefined}>
        {failed && (
          // The rows below are the last ones the server confirmed. They are
          // kept deliberately: a failed refresh is not news that the library
          // shrank.
          <div data-testid="home-stale-notice" className="on-notice">
            <p role="alert">
              these transcripts could not be refreshed and may be out of date.
              {failureCause ? ` ${failureCause}.` : ""}
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
          {visibleRows.length === 0 ? (
            <div className="on-nomatch" data-testid="home-no-project-match">
              <b>no transcripts match these projects</b>
              <button type="button" className="btn btn-secondary" onClick={() => setHiddenProjects(new Set())}>
                show all projects
              </button>
            </div>
          ) : (
            <TranscriptLibraryTable
              caption="your transcripts"
              rows={visibleRows}
              rowKey={(row) => row.transcript.id}
              columns={columns}
              groupBy={groupBy}
              groupByOptions={ALL_GROUP_BYS}
              groupKeys={(row) => groupKeysOf(row, groupBy)}
              groupLabel={(id) => groupLabelOf(id, groupBy, groupLabels)}
              nextGroupKeys={nextGroupKeys}
              continuedGroupKeys={continuedGroupKeys}
              page={{ index: list.data!.page, size: perPage, total: list.data!.total }}
              viewState={{ groupBy, visibleColumns: view.columns, collapsed: view.collapsed }}
              onViewStateChange={changeView}
              onPageChange={goToPage}
              onPageSizeChange={changePageSize}
            />
          )}
          <ScopedContextContainerList items={groupedItems} onRefreshOrigin={refreshGrouped} />
        </div>
      </div>
    );
  }

  const showTools = !libraryEmpty && list.data != null && !(failed && own == null && !searching);

  return (
    <OnPage testId="home-page">
      <p role="status" aria-live="polite" className="sr-only" data-testid="home-status">
        {statusText}
      </p>

      <OnHead title="your transcripts" />

      {showStats ? (
        <ul className="on-stats" data-testid="home-stats" aria-label="your transcripts in numbers">
          {statItems.map((item) => (
            <li key={item.label} className="on-stat">
              <b className="tabular-nums">{typeof item.value === "number" ? formatInteger(item.value) : item.value}</b> <span>{item.label}</span>
            </li>
          ))}
        </ul>
      ) : stats.isError ? (
        <p className="on-muted" data-testid="home-stats-failed">
          your totals could not be loaded.
        </p>
      ) : null}

      <HomeWaiting
        contributions={{
          data: contributions.data,
          isError: contributions.isError,
          isFetching: contributions.isFetching,
          refetch: contributions.refetch,
        }}
      />

      <section className="on-home-library" aria-labelledby="home-library-title">
      <h2 id="home-library-title" className="on-h2">library</h2>
      {showTools && (
        <div className="on-tools" role="toolbar" aria-label="view">
          <div className="on-search" role="search">
            <Search aria-hidden="true" />
            <input
              type="search"
              aria-label="search your transcripts"
              placeholder="search transcripts"
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
            />
          </div>
          {projects.length > 1 && (
            <ViewMenu icon={<FileText aria-hidden="true" />} label="projects" value={projectsLabel} title="projects" testId="home-projects">
              {() => (
                <>
                  <div className="on-menu-acts">
                    <button type="button" className="btn btn-ghost btn-sm" onClick={() => setHiddenProjects(new Set())}>
                      select all
                    </button>
                    <button
                      type="button"
                      className="btn btn-ghost btn-sm"
                      onClick={() => setHiddenProjects(new Set(projects.map((p) => p.key)))}
                    >
                      clear
                    </button>
                  </div>
                  {projects.map((p) => (
                    <CheckRow key={p.key} checked={!hiddenProjects.has(p.key)} onToggle={() => toggleProject(p.key)} count={p.count}>
                      {p.label}
                    </CheckRow>
                  ))}
                </>
              )}
            </ViewMenu>
          )}
        </div>
      )}

      {listRegion}
      {list.data != null && (
        <AgentSessionGroup agentTotal={agentTotal} baseParams={listParams} showOwnerActions />
      )}
      </section>
    </OnPage>
  );
}
