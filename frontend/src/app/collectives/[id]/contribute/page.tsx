"use client";

import { use, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { FileText, Lock } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/providers/AuthProvider";
import { loginHref } from "@/lib/signIn";
import { OnCrumb, OnEmpty, OnHead, OnPage } from "@/components/online/Page";
import { useGroup } from "@/lib/queries/groups";
import { useContributionRun } from "@/lib/queries/contributionSubmit";
import { isRetryableFailure, privateRecords, type PrivateApproval } from "@/lib/contribute/chunkedSubmit";
import { useContributionProjects, type ContributionFilters } from "@/lib/queries/contributionReads";
import {
  deselectIds,
  EMPTY_SELECTION,
  withoutLive,
  type FrozenSelection,
  type LoadedRow,
  type SelectedRecord,
} from "@/lib/contribute/frozenSelection";
import PagedContributeTree, { type SelectionUpdate } from "@/components/contribute/PagedContributeTree";
import TranscriptPreview from "@/components/contribute/TranscriptPreview";
import ContributionReceipt from "@/components/contribute/ContributionReceipt";
import ConfirmContributeDialog from "@/components/transcript/ConfirmContributeDialog";
import { Button, Input, Select } from "@/lib/ft-ui";
import RequestFailureState from "@/components/RequestFailureState";

/**
 * The consent step. Before a run it lists every private session of the
 * selection; during a run it lists the sessions that turned private after
 * they were selected, and its answer settles the run's question.
 */
type ConsentStep =
  | { kind: "before_run"; selection: FrozenSelection; records: SelectedRecord[] }
  | { kind: "during_run"; records: SelectedRecord[] };

/** How long the search waits after the last keystroke before it reads. */
const SEARCH_DEBOUNCE_MS = 300;

const plural = (count: number, noun: string) => `${count} ${noun}${count === 1 ? "" : "s"}`;

/**
 * Dedicated contribute route for a collective: `/collectives/{id}/contribute`.
 *
 * The library is read by project: the project list states every project's
 * counts without loading its sessions, and opening a project reads its
 * sessions a page at a time. A project's "select all" reads the exact set of
 * its matching sessions, so a selection never depends on what happens to be
 * loaded. The selection is a set of records, frozen when it is made: a later
 * search never adds to it or silently drops from it. Layout: one column below
 * 880px (container width, not viewport), two at and above it.
 */
export default function GroupContributePage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const queryClient = useQueryClient();
  const { data: group, isLoading: groupLoading } = useGroup(id);
  const { isLoading: sessionLoading, isLoggedIn } = useAuth();
  const run = useContributionRun(id);
  const running = run.state.phase === "running";

  const [searchText, setSearchText] = useState("");
  const [query, setQuery] = useState("");
  const [provider, setProvider] = useState("");
  const filters = useMemo<ContributionFilters>(
    () => ({ ...(query ? { q: query } : {}), ...(provider ? { provider } : {}) }),
    [query, provider],
  );
  const filtersActive = query !== "" || provider !== "";
  useEffect(() => {
    const timer = setTimeout(() => setQuery(searchText.trim()), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [searchText]);

  const picker = useContributionProjects(id, filters);
  const [rawSelection, setSelection] = useState<FrozenSelection>(EMPTY_SELECTION);
  const updateSelection = useCallback<SelectionUpdate>((update) => setSelection(update), []);
  const [previewId, setPreviewId] = useState<string | null>(null);
  const [consent, setConsent] = useState<ConsentStep | null>(null);
  // The run's open question about sessions that turned private, answered by
  // the consent step.
  const pendingApproval = useRef<((answer: PrivateApproval) => void) | null>(null);

  // Every row an open project has loaded, by project. A row loaded once stays
  // known after its section closes, so its project can still be named.
  const [loaded, setLoaded] = useState<ReadonlyMap<string, LoadedRow[]>>(new Map());
  const handleLoadedRows = useCallback((projectHash: string, rows: LoadedRow[]) => {
    setLoaded((before) => (before.get(projectHash) === rows ? before : new Map(before).set(projectHash, rows)));
  }, []);
  const loadedFlat = useMemo(() => [...loaded.values()].flat(), [loaded]);

  // A refreshed page can show a selected session as contributed meanwhile,
  // elsewhere or by a run still in flight. It no longer counts as selected,
  // and the page says so until the person dismisses the notice.
  const { selection, becameLive } = useMemo(() => withoutLive(rawSelection, loadedFlat), [rawSelection, loadedFlat]);
  const contributedMeanwhile = running ? 0 : becameLive.length;

  const yourRole = group?.your_role;
  const isMember = yourRole === "contributor" || yourRole === "member" || yourRole === "owner";

  const projectLabelByHash = useMemo(() => {
    const labels = new Map<string, string>();
    for (const row of loadedFlat) labels.set(row.transcript.project_hash, row.transcript.project_display_name);
    for (const project of picker.projects) labels.set(project.project_hash, project.project_display_name);
    return labels;
  }, [loadedFlat, picker.projects]);

  // Selected sessions whose project this search does not list. They stay in
  // the selection, so the page names them and offers to let them go.
  const listedProjects = useMemo(() => new Set<string>(picker.projects.map((p) => p.project_hash)), [picker.projects]);
  const unlistedIds = useMemo(
    () => [...selection.values()].filter((record) => !listedProjects.has(record.family_project_hash)).map((record) => record.id),
    [listedProjects, selection],
  );

  const selectedCount = selection.size;

  if (groupLoading) {
    return (
      <OnPage tight>
        <div className="h-4 w-40 bg-surface-hover animate-shimmer" />
        <div className="h-16 w-72 bg-surface-hover animate-shimmer" />
        <div className="h-64 w-full bg-surface-hover animate-shimmer" />
      </OnPage>
    );
  }

  if (!group || !isMember) {
    // Somebody who is not signed in is offered the one thing that could change
    // the answer, and returns to this page afterwards.
    const signedOut = !sessionLoading && !isLoggedIn;
    return (
      <OnPage tight>
        {group && (
          <OnCrumb
            items={[
              { label: "collectives", href: "/collectives" },
              { label: <span className="normal-case">{group.group.name}</span>, href: `/collectives/${id}` },
              { label: "contribute" },
            ]}
          />
        )}
        <OnEmpty
          headingLevel={1}
          icon={<Lock aria-hidden="true" />}
          title={
            signedOut
              ? "sign in to contribute to this collective"
              : group
                ? "you must be a member of this collective to contribute"
                : "collective not found"
          }
          testId="contribute-non-member-notice"
          actions={
            <>
              {signedOut && (
                <Link href={loginHref(`/collectives/${id}/contribute`)} className="btn btn-primary">
                  sign in
                </Link>
              )}
              <Link href={group ? `/collectives/${id}` : "/collectives"} className="btn btn-secondary">
                back to <span className="normal-case">{group ? group.group.name : "collectives"}</span>
              </Link>
            </>
          }
        />
      </OnPage>
    );
  }

  const approvePrivate = (records: SelectedRecord[]) =>
    new Promise<PrivateApproval>((resolve) => {
      pendingApproval.current = resolve;
      setConsent({ kind: "during_run", records });
    });

  function answerApproval(answer: PrivateApproval) {
    pendingApproval.current?.(answer);
    pendingApproval.current = null;
    setConsent(null);
  }

  async function startRun(runSelection: FrozenSelection, approved: ReadonlySet<string>) {
    setConsent(null);
    const receipt = await run.start({ selection: runSelection, approved, approvePrivate });
    if (!receipt) return;
    // What the run settled leaves the selection. A failure a retry can fix
    // stays, and so does every session the run never reached.
    const settled = [
      ...receipt.contributed.map(({ record }) => record.id),
      ...receipt.already.map((record) => record.id),
      ...receipt.failed.filter((failure) => !isRetryableFailure(failure.reason)).map((failure) => failure.record.id),
    ];
    setSelection((previous) => deselectIds(previous, settled));
  }

  /** Consent is asked once per run, up front, for every private session the
   *  run would make visible, including ones the page never loaded. */
  function contribute(runSelection: FrozenSelection) {
    if (runSelection.size === 0 || running) return;
    const records = privateRecords(runSelection);
    if (records.length > 0) {
      setConsent({ kind: "before_run", selection: runSelection, records });
      return;
    }
    void startRun(runSelection, new Set());
  }

  function retry(records: SelectedRecord[]) {
    contribute(new Map(records.map((record) => [record.id, record])));
  }

  const pickerFailure = picker.failure && picker.failure.kind !== "cancelled" ? picker.failure : null;
  const nothingToContribute =
    !filtersActive && !picker.isLoading && !pickerFailure && picker.projects.length === 0 && selectedCount === 0;

  return (
    <>
    <OnPage tight>
      <OnCrumb
        items={[
          { label: "collectives", href: "/collectives" },
          { label: <span className="normal-case">{group.group.name}</span>, href: `/collectives/${id}` },
          { label: "contribute" },
        ]}
      />
      {/* The collective's name is USER CONTENT, so it keeps its case inside
          the lowercase heading. */}
      <OnHead
        title={
          <>
            contribute to <span className="normal-case">{group.group.name}</span>
          </>
        }
      />

      {nothingToContribute ? (
        <OnEmpty
          icon={<FileText aria-hidden="true" />}
          title="no transcripts to contribute"
          body="all your transcripts are already shared with this collective, or you have no published transcripts."
          testId="contribute-member-panel"
        />
      ) : (
        <div className="on-contribute-workspace" data-testid="contribute-member-panel">
          <div className="on-contribute-columns">
            <div className="on-contribute-browser">
              <div className="on-contribute-tree">
                <div className="on-contribute-filters">
                  <Input
                    value={searchText}
                    onChange={(e) => setSearchText(e.target.value)}
                    placeholder="search sessions"
                    aria-label="search sessions"
                  />
                  <Select value={provider} onChange={(e) => setProvider(e.target.value)} aria-label="filter by harness">
                    <option value="">every harness</option>
                    {picker.providers.map((facet) => (
                      <option key={facet.provider} value={facet.provider}>
                        {facet.provider} ({facet.count})
                      </option>
                    ))}
                  </Select>
                </div>
                <div className="on-contribute-selection">
                  <p className="min-w-0 font-mono text-sm tabular-nums text-ink-2" data-testid="contribute-selection-tally">
                    {selectedCount} selected · {plural(picker.selectableTotal, "selectable session")}
                    {picker.matchingTotal > picker.selectableTotal &&
                      ` · ${picker.matchingTotal - picker.selectableTotal} already contributed`}
                  </p>
                  <div className="flex items-center gap-2">
                    {selectedCount > 0 && (
                      <Button size="sm" variant="ghost" onClick={() => setSelection(EMPTY_SELECTION)}>
                        clear selection
                      </Button>
                    )}
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={picker.isFetching}
                      onClick={() => void queryClient.invalidateQueries({ queryKey: ["group-contributable", id] })}
                    >
                      refresh
                    </Button>
                  </div>
                </div>
                {(unlistedIds.length > 0 || contributedMeanwhile > 0) && (
                  <div className="flex flex-col gap-2 border-b border-rule px-4 py-3 text-sm text-ink-2" role="status">
                    {unlistedIds.length > 0 && (
                      <div className="flex flex-wrap items-center justify-between gap-2" data-testid="contribute-unlisted-notice">
                        <p>
                          {plural(unlistedIds.length, "selected session")} {unlistedIds.length === 1 ? "is" : "are"} in
                          projects this search does not list. {unlistedIds.length === 1 ? "it stays" : "they stay"} selected.
                        </p>
                        <Button size="sm" variant="ghost" onClick={() => setSelection((previous) => deselectIds(previous, unlistedIds))}>
                          deselect {unlistedIds.length === 1 ? "it" : "them"}
                        </Button>
                      </div>
                    )}
                    {contributedMeanwhile > 0 && (
                      <div className="flex flex-wrap items-center justify-between gap-2" data-testid="contribute-meanwhile-notice">
                        <p>
                          {plural(contributedMeanwhile, "selected session")} {contributedMeanwhile === 1 ? "was" : "were"}{" "}
                          contributed meanwhile and left the selection.
                        </p>
                        <Button size="sm" variant="ghost" onClick={() => setSelection((previous) => deselectIds(previous, becameLive))}>
                          dismiss
                        </Button>
                      </div>
                    )}
                  </div>
                )}
                <div className="on-contribute-rows">
                  {pickerFailure ? (
                    <RequestFailureState
                      title="your transcripts could not load"
                      message={
                        pickerFailure.kind === "deployment"
                          ? pickerFailure.message
                          : "your available transcripts could not be read. retry to find out which transcripts you can contribute."
                      }
                      retryLabel="retry your transcripts"
                      retryDisabled={picker.isFetching}
                      onRetry={() => void picker.refresh()}
                    />
                  ) : picker.isLoading ? (
                    <div className="flex flex-col gap-2 p-4" aria-busy="true" data-testid="contribute-projects-loading">
                      <span className="sr-only">loading your projects</span>
                      <div className="h-12 w-full bg-surface-hover animate-shimmer" />
                      <div className="h-12 w-full bg-surface-hover animate-shimmer" />
                      <div className="h-12 w-full bg-surface-hover animate-shimmer" />
                    </div>
                  ) : picker.projects.length === 0 ? (
                    <p className="px-4 py-8 text-center text-sm text-ink-3">no contributable sessions match this search.</p>
                  ) : (
                    <>
                      <PagedContributeTree
                        groupId={id}
                        filters={filters}
                        projects={picker.projects}
                        selection={selection}
                        onSelectionChange={updateSelection}
                        previewId={previewId}
                        onPreview={setPreviewId}
                        onLoadedRows={handleLoadedRows}
                      />
                      {picker.hasNextPage && (
                        <div className="px-4 py-3">
                          <Button
                            size="sm"
                            variant="secondary"
                            loading={picker.isFetchingNextPage}
                            disabled={picker.isFetchingNextPage}
                            onClick={() => void picker.fetchNextPage()}
                          >
                            load more projects
                          </Button>
                        </div>
                      )}
                    </>
                  )}
                </div>
              </div>
            </div>
            <div className="on-contribute-preview">
              <TranscriptPreview transcriptId={previewId} />
            </div>
          </div>
        </div>
      )}

      <div className="on-contribute-footer">
        <div className="on-contribute-footer-inner">
        {/* The status side takes the free width, so an open receipt grows
            downward and the contribute button holds its place. */}
        <div className="flex min-w-0 flex-[1_1_20rem] flex-col gap-1">
          <p className="text-[length:var(--fs-label)] font-mono text-ink-3 tabular-nums" data-testid="contribute-selection-count">{selectedCount} selected</p>
          {run.state.phase === "running" && (
            <div className="flex items-center gap-3">
              <div
                className="w-64 max-w-full h-1 bg-rule overflow-hidden"
                role="progressbar"
                aria-label="contribution progress"
                aria-valuemin={0}
                aria-valuemax={run.state.progress?.total ?? selectedCount}
                aria-valuenow={run.state.progress?.settled ?? 0}
                data-testid="contribute-run-progress"
              >
                <div
                  className="h-full bg-mark transition-[width]"
                  style={{
                    width: `${run.state.progress && run.state.progress.total > 0 ? (run.state.progress.settled / run.state.progress.total) * 100 : 0}%`,
                  }}
                />
              </div>
              {run.state.progress && (
                <span className="text-[length:var(--fs-label)] font-mono text-ink-3 tabular-nums">
                  {run.state.progress.settled} of {run.state.progress.total}
                </span>
              )}
              <Button size="sm" variant="ghost" onClick={run.cancel}>
                stop
              </Button>
            </div>
          )}
          {run.state.phase === "done" && (
            <ContributionReceipt
              receipt={run.state.receipt}
              projectLabel={(hash) => projectLabelByHash.get(hash)}
              onRetry={retry}
            />
          )}
        </div>
        <Button
          variant="primary"
          loading={running}
          disabled={selectedCount === 0 || running}
          onClick={() => contribute(selection)}
        >
          {running ? "contributing…" : `contribute ${plural(selectedCount, "transcript")}`}
        </Button>
        </div>
      </div>

    </OnPage>

      <ConfirmContributeDialog
        open={consent !== null}
        onClose={() => (consent?.kind === "during_run" ? answerApproval("decline") : setConsent(null))}
        onConfirm={() => {
          if (consent?.kind === "during_run") answerApproval("approve");
          else if (consent) void startRun(consent.selection, new Set(consent.records.map((record) => record.id)));
        }}
        transcripts={(consent?.records ?? []).map((record) => ({ id: record.id, title: record.title ?? "untitled session" }))}
        collectives={[
          {
            id: group.group.id,
            name: group.group.name,
            memberCount: group.members.length,
          },
        ]}
      />
    </>
  );
}
