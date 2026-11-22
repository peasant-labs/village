"use client";

import { use, useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { FileText, Lock } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useAuth } from "@/providers/AuthProvider";
import { loginHref } from "@/lib/signIn";
import { OnCrumb, OnEmpty, OnHead, OnPage } from "@/components/online/Page";
import { useGroup } from "@/lib/queries/groups";
import { useContributeRun, partitionRunOutcome } from "@/lib/queries/groupShares";
import { useContributionProjects, type ContributionFilters } from "@/lib/queries/contributionReads";
import {
  bySubmissionProject,
  deselectIds,
  EMPTY_SELECTION,
  withoutLive,
  type FrozenSelection,
  type LoadedRow,
} from "@/lib/contribute/frozenSelection";
import PagedContributeTree, { type SelectionUpdate } from "@/components/contribute/PagedContributeTree";
import TranscriptPreview from "@/components/contribute/TranscriptPreview";
import ConfirmContributeDialog from "@/components/transcript/ConfirmContributeDialog";
import { Button, Input, Select } from "@/lib/ft-ui";
import RequestFailureState from "@/components/RequestFailureState";

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
  const run = useContributeRun(id);

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
  const [confirmOpen, setConfirmOpen] = useState(false);

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
  const contributedMeanwhile = run.state.running ? 0 : becameLive.length;

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

  async function startRun(visibilityConfirmed: boolean) {
    // One submission per project the sessions are filed under, each naming
    // its sessions explicitly.
    const batches = new Map(
      [...bySubmissionProject(selection)].map(([projectHash, records]) => [projectHash, records.map((record) => record.id)]),
    );
    if (batches.size === 0) return;
    const results = await run.run(batches, visibilityConfirmed);
    const { clearedIds } = partitionRunOutcome(batches, results);
    setSelection((previous) => deselectIds(previous, clearedIds));
    setConfirmOpen(false);
  }

  const privateSelected = [...selection.values()]
    .filter((record) => record.visibility === "private")
    .map((record) => ({ id: record.id, title: record.title ?? "untitled session" }));

  function handleContributeClick() {
    if (selectedCount === 0) return;
    if (privateSelected.length > 0) {
      setConfirmOpen(true);
      return;
    }
    void startRun(false);
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
        <div className="flex flex-col gap-1 min-w-0">
          <p className="text-[length:var(--fs-label)] font-mono text-ink-3 tabular-nums" data-testid="contribute-selection-count">{selectedCount} selected</p>
          {run.state.running && (
            <div
              className="w-64 max-w-full h-1 bg-rule overflow-hidden"
              role="progressbar"
              aria-label="contribution progress"
              aria-valuemin={0}
              aria-valuemax={run.state.total}
              aria-valuenow={run.state.done}
              data-testid="contribute-run-progress"
            >
              <div
                className="h-full bg-mark transition-[width]"
                style={{ width: `${run.state.total === 0 ? 0 : (run.state.done / run.state.total) * 100}%` }}
              />
            </div>
          )}
          {!run.state.running && run.state.results.size > 0 && (
            <ul className="flex flex-col gap-1 text-[length:var(--fs-body)] text-ink-3" data-testid="contribute-run-receipt">
              {[...run.state.results.entries()].map(([projectHash, outcome]) => (
                <li key={projectHash} className="tabular-nums">
                  <span className="normal-case">{projectLabelByHash.get(projectHash) ?? "a project"}</span>:{" "}
                  {"shared" in outcome
                    ? `${outcome.shared.length} shared, ${outcome.already_shared.length} already shared`
                    : outcome.message}
                </li>
              ))}
            </ul>
          )}
        </div>
        <Button
          variant="primary"
          loading={run.state.running}
          disabled={selectedCount === 0 || run.state.running}
          onClick={handleContributeClick}
        >
          {run.state.running ? "sharing…" : `contribute ${plural(selectedCount, "transcript")}`}
        </Button>
        </div>
      </div>

    </OnPage>

      <ConfirmContributeDialog
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        onConfirm={() => startRun(true)}
        transcripts={privateSelected}
        collectives={[
          {
            id: group.group.id,
            name: group.group.name,
            memberCount: group.members.length,
          },
        ]}
        isSubmitting={run.state.running}
      />
    </>
  );
}
