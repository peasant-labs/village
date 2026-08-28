"use client";

import { use, useMemo, useState } from "react";
import Link from "next/link";
import { useGroup } from "@/lib/queries/groups";
import { useContributable, useContributeRun, partitionRunOutcome } from "@/lib/queries/groupShares";
import { buildContributeTree } from "@/lib/contribute/tree";
import { groupByProject, privateIds, toggleNode, type Selection } from "@/lib/contribute/selection";
import { applyFilters, harnessCounts, type ContributeFilters } from "@/lib/contribute/filter";
import ContributeTree from "@/components/contribute/ContributeTree";
import TranscriptPreview from "@/components/contribute/TranscriptPreview";
import ConfirmContributeDialog from "@/components/transcript/ConfirmContributeDialog";
import { Button } from "@/lib/ft-ui";

/**
 * Dedicated contribute route for a collective: `/groups/{id}/contribute`.
 *
 * Replaces the interim single-panel shell (village#64) with the project >
 * branch > session tree, a transcript preview column, and a sequential
 * one-POST-per-project batch-share run (village#66). Layout:
 * one column below 880px (container width, not viewport), two columns
 * (`minmax(20rem, 2fr) 3fr`) at and above it — the same breakpoint fairtrade's
 * `RailShell` uses for its own rail collapse.
 */
export default function GroupContributePage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const { data: group, isLoading: groupLoading } = useGroup(id);
  const { data: contributable, isLoading: contributableLoading } = useContributable(id);
  const run = useContributeRun(id);

  const [selection, setSelection] = useState<Selection>(new Set());
  const [previewId, setPreviewId] = useState<string | null>(null);
  const [filters, setFilters] = useState<ContributeFilters>({ search: "", harness: null });
  const [confirmOpen, setConfirmOpen] = useState(false);

  const yourRole = group?.your_role;
  const isMember = yourRole === "contributor" || yourRole === "member" || yourRole === "owner";

  const rows = useMemo(() => contributable?.transcripts ?? [], [contributable]);
  const filteredRows = useMemo(() => applyFilters(rows, filters), [rows, filters]);
  const tree = useMemo(() => buildContributeTree(filteredRows), [filteredRows]);
  const counts = useMemo(() => harnessCounts(rows, filters.search), [rows, filters.search]);
  // The receipt list is keyed by `project_hash` (the run's grouping key), but
  // a viewer never sees a raw hash elsewhere on this page -- resolve it back
  // to the same `project_display_name` the tree renders.
  const projectLabelByHash = useMemo(
    () => new Map(rows.map((row) => [row.project_hash, row.project_display_name])),
    [rows],
  );
  const selectedCount = selection.size;

  if (groupLoading || contributableLoading) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="h-4 w-40 bg-surface-hover animate-shimmer" />
        <div className="h-16 w-72 bg-surface-hover animate-shimmer" />
        <div className="h-64 w-full bg-surface-hover animate-shimmer" />
      </div>
    );
  }

  if (!group || !isMember) {
    return (
      <div
        className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up"
        data-testid="contribute-non-member-notice"
      >
        <div className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center">
          <p className="text-sm font-medium text-ink">
            {group
              ? "you must be a member of this collective to contribute"
              : "collective not found"}
          </p>
          <Link
            href={group ? `/groups/${id}` : "/groups"}
            className="text-sm text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
          >
            back to {group ? group.group.name : "collectives"}
          </Link>
        </div>
      </div>
    );
  }

  function handleToggle(node: Parameters<typeof toggleNode>[1]) {
    setSelection((prev) => toggleNode(prev, node));
  }

  // Select-all / deselect-all acts on the leaves the tree currently SHOWS: a
  // row hidden by the search or harness filter keeps whatever state it had, so
  // a filtered view can never silently drop (or silently add) a selection the
  // viewer cannot see.
  function handleToggleAll(ids: string[], selectAll: boolean) {
    setSelection((prev) => {
      const next = new Set(prev);
      for (const id of ids) {
        if (selectAll) next.add(id);
        else next.delete(id);
      }
      return next;
    });
  }

  async function startRun(visibilityConfirmed: boolean) {
    const batches = groupByProject(selection, tree);
    if (batches.size === 0) return;
    const results = await run.run(batches, visibilityConfirmed);
    const { clearedIds } = partitionRunOutcome(batches, results);
    const cleared = new Set(clearedIds);
    setSelection((prev) => new Set([...prev].filter((sel) => !cleared.has(sel))));
    setConfirmOpen(false);
  }

  function handleContributeClick() {
    if (selectedCount === 0) return;
    const privates = privateIds(selection, tree);
    if (privates.length > 0) {
      setConfirmOpen(true);
      return;
    }
    void startRun(false);
  }

  const privateSelectedItems = privateIds(selection, tree).map((transcriptId) => {
    const source = rows.find((row) => row.id === transcriptId);
    return { id: transcriptId, title: source?.title ?? transcriptId };
  });

  return (
    <div className="cmg-root max-w-[1600px] mx-auto px-6 pt-6 pb-24 flex flex-col gap-6 animate-fade-up">
      {/* Title, then one line saying what this page is for, matching the
          heading rhythm every other village surface uses. */}
      <div className="flex flex-col gap-2">
        <div className="flex items-start justify-between gap-4">
          {/* `normal-case` is load-bearing: the design system lowercases
              h1/h2/h3 as UI chrome, and the collective's name is USER
              CONTENT, which is never lowercased. */}
          <h1 className="text-xl font-semibold text-ink normal-case">
            contribute to {group.group.name}
          </h1>
          <Link
            href={`/groups/${id}`}
            className="shrink-0 text-sm text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
          >
            back to {group.group.name}
          </Link>
        </div>
        <p className="text-sm text-ink-3">
          choose the sessions to share with this collective.
        </p>
      </div>

      {/* `contribute-member-panel` is the stable member-view testid this
          route has carried since village#64 (the interim single-panel
          shell); kept on the tree+preview composition that replaces that
          panel's body so a caller keyed on "the member sees SOME contribute
          UI" does not need to know which body variant is mounted. */}
      {tree.length === 0 ? (
        <div className="border border-rule bg-surface px-5 py-12 text-center" data-testid="contribute-member-panel">
          <p className="text-sm text-ink-3">
            all your transcripts are already shared with this collective, or you have no
            published transcripts.
          </p>
        </div>
      ) : (
        <div className="@container" data-testid="contribute-member-panel">
          <div className="grid grid-cols-1 @[880px]:grid-cols-[minmax(20rem,2fr)_3fr] gap-4 border border-rule bg-surface min-h-[32rem]">
            <div className="border-b @[880px]:border-b-0 @[880px]:border-r border-rule min-h-[20rem] @[880px]:min-h-[32rem]">
              <ContributeTree
                tree={tree}
                selection={selection}
                onToggleNode={handleToggle}
                onToggleAll={handleToggleAll}
                onPreview={setPreviewId}
                previewId={previewId}
                filters={filters}
                onFiltersChange={setFilters}
                harnessCounts={counts}
              />
            </div>
            <div className="min-h-[20rem] @[880px]:min-h-[32rem]">
              <TranscriptPreview transcriptId={previewId} />
            </div>
          </div>
        </div>
      )}

      <div className="fixed bottom-0 left-0 right-0 border-t border-rule bg-surface z-10">
        <div className="max-w-[1600px] mx-auto px-6 py-3 flex items-center justify-between gap-4">
        <div className="flex flex-col gap-1 min-w-0">
          <p className="text-xs font-mono text-ink-3 tabular-nums">{selectedCount} selected</p>
          {run.state.running && (
            <div
              className="w-64 max-w-full h-1 bg-rule overflow-hidden"
              role="progressbar"
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
            <ul className="flex flex-col gap-1 text-xs text-ink-3" data-testid="contribute-run-receipt">
              {[...run.state.results.entries()].map(([projectHash, outcome]) => (
                <li key={projectHash} className="tabular-nums">
                  {projectLabelByHash.get(projectHash) ?? projectHash}:{" "}
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
          {run.state.running ? "sharing…" : `contribute ${selectedCount} transcript${selectedCount !== 1 ? "s" : ""}`}
        </Button>
        </div>
      </div>

      <ConfirmContributeDialog
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        onConfirm={() => startRun(true)}
        transcripts={privateSelectedItems}
        collectives={[
          {
            id: group.group.id,
            name: group.group.name,
            memberCount: group.members.length,
          },
        ]}
        isSubmitting={run.state.running}
      />
    </div>
  );
}
