"use client";

import { use, useCallback, useMemo, useState } from "react";
import Link from "next/link";
import type { VillageSessionListItem } from "@peasant-labs/schema";
import { useGroup } from "@/lib/queries/groups";
import { useContributable, useContributeRun, partitionRunOutcome } from "@/lib/queries/groupShares";
import { useGroupedContributable } from "@/lib/queries/groupedCollectives";
import { GROUPED_TOP_LEVEL_PAGE_SIZE } from "@/lib/queries/helperGroups";
import { buildContributeTree } from "@/lib/contribute/tree";
import { groupByProject, privateIds, sessionRows, toggleNode, type Selection } from "@/lib/contribute/selection";
import { groupedContributionBatches, mergeContributionBatches } from "@/lib/contribute/groupedSelection";
import { helperItemID, useExplicitHelperSelection } from "@/lib/contribute/helperSelection";
import { applyFilters, harnessCounts, type ContributeFilters } from "@/lib/contribute/filter";
import ContributeTree from "@/components/contribute/ContributeTree";
import TranscriptPreview from "@/components/contribute/TranscriptPreview";
import {
  ScopedOwnerHelperGroups,
  ScopedUnownedHelperGroups,
  helperGroupsByTranscript,
  unownedGroupedItems,
} from "@/components/transcript/ScopedHelperGroups";
import ScopedGroupedContinuation from "@/components/transcript/ScopedGroupedContinuation";
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
  // The grouped read of the SAME route. It supplements the flat tree with the
  // saved helper threads the server grouped under each owner row; it is not a
  // second list. A failed or still-loading grouped read removes nothing -- the
  // flat tree above stays the authority for its own rows and pages. The read
  // pages, so a later grouped owner or helper-only context container still has
  // its grouped exit; the server's own total, never a flat offset, decides the
  // next page.
  const grouped = useGroupedContributable(id, { limit: GROUPED_TOP_LEVEL_PAGE_SIZE });
  const groupedItems = useMemo(() => grouped.items, [grouped.items]);
  const helperGroups = useMemo(() => helperGroupsByTranscript(groupedItems), [groupedItems]);
  // Every transcript the flat tree actually draws, including the sessions folded
  // under another row. A grouped owner named here is ALREADY represented, so the
  // grouped exit below skips it instead of mounting a second owner row.
  const flatOwnerIds = useMemo(
    () => new Set(tree.flatMap((project) => sessionRows(project).map((row) => row.id))),
    [tree],
  );
  const groupedFallback = useMemo(
    () => unownedGroupedItems(groupedItems, flatOwnerIds),
    [groupedItems, flatOwnerIds],
  );
  // Grouped content the flat tree did not carry, plus the way to any grouped
  // page not read yet. Either one is enough that the page is not empty.
  const hasGroupedExit = groupedFallback.length > 0 || grouped.remainingItems > 0;
  // A member is selectable only while the row it was served with is still a
  // live contributable submission: an already-shared row stays visible in the
  // group but can never re-enter a contribution batch.
  const helperDisabled = useCallback((item: VillageSessionListItem) => {
    const row = item.transcript;
    const contribution = row?.contributable;
    return !row || !contribution || contribution.already_shared || contribution.id !== row.session.id;
  }, []);
  const helper = useExplicitHelperSelection(helperDisabled);
  const helperPrivateSelected = useMemo(
    () => [...helper.selected.values()].filter((item) => item.transcript?.session.visibility === "private"),
    [helper.selected],
  );
  // The receipt list is keyed by `project_hash` (the run's grouping key), but
  // a viewer never sees a raw hash elsewhere on this page -- resolve it back
  // to the same `project_display_name` the tree renders.
  const projectLabelByHash = useMemo(
    () => new Map(rows.map((row) => [row.project_hash, row.project_display_name])),
    [rows],
  );
  const selectedCount = selection.size + helper.selectedIds.size;

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
    // Tree rows and individually ticked helper members are ONE set of explicit
    // transcript ids. The helper batches are built from the exact row each
    // member endpoint served, so nothing here sends a group id or infers a
    // sibling: the ordinary project/branch selection is the pre-existing
    // per-transcript selection, and a helper is only ever its own id.
    const batches = mergeContributionBatches(
      groupByProject(selection, tree),
      groupedContributionBatches([...helper.selected.values()]),
    );
    if (batches.size === 0) return;
    const results = await run.run(batches, visibilityConfirmed);
    const { clearedIds } = partitionRunOutcome(batches, results);
    const cleared = new Set(clearedIds);
    setSelection((prev) => new Set([...prev].filter((sel) => !cleared.has(sel))));
    helper.forget(cleared);
    setConfirmOpen(false);
  }

  function handleContributeClick() {
    if (selectedCount === 0) return;
    const privates = [
      ...privateIds(selection, tree),
      ...helperPrivateSelected.map((item) => helperItemID(item)).filter((id): id is string => id != null),
    ];
    if (privates.length > 0) {
      setConfirmOpen(true);
      return;
    }
    void startRun(false);
  }

  const privateSelectedItems = [
    ...privateIds(selection, tree).map((transcriptId) => {
      const source = rows.find((row) => row.id === transcriptId);
      return { id: transcriptId, title: source?.title ?? transcriptId };
    }),
    ...helperPrivateSelected.map((item) => ({
      id: helperItemID(item) ?? "",
      title: item.transcript?.session.title ?? helperItemID(item) ?? "",
    })),
  ];

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
      {tree.length === 0 && !hasGroupedExit ? (
        <div className="border border-rule bg-surface px-5 py-12 text-center" data-testid="contribute-member-panel">
          <p className="text-sm text-ink-3">
            all your transcripts are already shared with this collective, or you have no
            published transcripts.
          </p>
        </div>
      ) : (
        <>
          {tree.length > 0 && (
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
                    helperGroupSlot={(session) => (
                      <ScopedOwnerHelperGroups
                        groups={helperGroups.get(session.id)}
                        onRefreshOrigin={grouped.refreshOrigin}
                        selection={helper.contract}
                      />
                    )}
                  />
                </div>
                <div className="min-h-[20rem] @[880px]:min-h-[32rem]">
                  <TranscriptPreview transcriptId={previewId} />
                </div>
              </div>
            </div>
          )}
          {/* The grouped exits the flat tree above did not draw: the context
              containers it has no row for, and each owner row it does not
              carry. Mounted OUTSIDE the tree branch, so an empty or partial
              flat result still reaches the saved helper threads the server
              grouped -- and an owner the tree already draws is skipped, never
              mounted twice. */}
          {hasGroupedExit && (
            <div className="border border-rule bg-surface" data-testid="grouped-helper-fallback">
              <ScopedUnownedHelperGroups
                items={groupedItems}
                representedOwnerIds={flatOwnerIds}
                onRefreshOrigin={grouped.refreshOrigin}
                selection={helper.contract}
              />
              <ScopedGroupedContinuation
                remaining={grouped.remainingItems}
                busy={grouped.isFetchingNextPage}
                onLoadMore={() => void grouped.fetchNextPage()}
              />
            </div>
          )}
        </>
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
