"use client";

import { use, useCallback, useMemo, useState } from "react";
import Link from "next/link";
import type { VillageSessionListItem } from "@peasant-labs/schema";
import { useGroup } from "@/lib/queries/groups";
import { usePendingShares, useBatchReview } from "@/lib/queries/groupReview";
import { useGroupedPendingShares } from "@/lib/queries/groupedCollectives";
import { GROUPED_TOP_LEVEL_PAGE_SIZE } from "@/lib/queries/helperGroups";
import { buildReviewTree, toReviewRows } from "@/lib/review/tree";
import type { ReviewDecision } from "@/lib/review/types";
import { sessionRows, toggleNode } from "@/lib/contribute/selection";
import { groupedReviewSelection } from "@/lib/contribute/groupedSelection";
import { helperItemID, useCollectiveIdentitySelection } from "@/lib/contribute/helperSelection";
import { applyFilters, harnessCounts, type ContributeFilters } from "@/lib/contribute/filter";
import ContributeTree from "@/components/contribute/ContributeTree";
import TranscriptPreview from "@/components/contribute/TranscriptPreview";
import {
  ScopedOwnerHelperTree,
  ScopedUnownedHelperGroups,
  helperGroupsByTranscript,
  unownedGroupedItems,
} from "@/components/transcript/ScopedHelperGroups";
import ScopedGroupedContinuation from "@/components/transcript/ScopedGroupedContinuation";
import { Button } from "@/lib/ft-ui";

/**
 * The owner's review route for a collective: `/groups/{id}/review`.
 *
 * The collective page's queue block decides ONE submission per click, which
 * makes a queue of any size a long sequence of single decisions and shows a
 * reviewer nothing of the work they are deciding on. This page reads the same
 * pending queue through the contribute page's tree and preview composition:
 * project > branch > session on the left with checkboxes, the focused
 * submission's transcript on the right, and one bottom bar that applies a
 * single decision to the whole selection in ONE request.
 *
 * Owner-only. A maintainer role that could also review is deliberately not
 * modelled here — village's role set is owner | member | contributor | pending,
 * and adding a reviewer role is its own change to the role model.
 */
export default function GroupReviewPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  const { data: group, isLoading: groupLoading } = useGroup(id);
  const isOwner = group?.your_role === "owner";
  const { data: pending, isLoading: pendingLoading } = usePendingShares(id, isOwner);
  const review = useBatchReview(id);

  const [previewId, setPreviewId] = useState<string | null>(null);
  const [filters, setFilters] = useState<ContributeFilters>({ search: "", harness: null });
  // Rows the server reported as already decided on the LAST action. They stay
  // on screen, marked and unselectable, until the refetched queue drops them:
  // a reviewer must be able to see which of their rows went stale, not just
  // watch a selection silently shrink.
  const [stale, setStale] = useState<ReadonlySet<string>>(new Set());

  // The grouped read of the SAME queue: it supplements the flat review tree
  // with the saved helper threads the server grouped under each submission.
  // A failed or still-loading grouped read removes nothing -- the flat tree
  // above stays the authority for its own rows. The read pages, so a later
  // grouped owner or helper-only context container still has its grouped exit;
  // the server's own total decides the next page.
  const grouped = useGroupedPendingShares(
    id,
    { limit: GROUPED_TOP_LEVEL_PAGE_SIZE },
    isOwner,
  );
  const groupedItems = useMemo(() => grouped.items, [grouped.items]);
  const helperGroups = useMemo(() => helperGroupsByTranscript(groupedItems), [groupedItems]);
  // A helper is decidable only while the row it was served with is still a
  // live pending submission for this collective, and not already answered by
  // another reviewer on this page's last action.
  const helperDisabled = useCallback(
    (item: VillageSessionListItem) => {
      const row = item.transcript;
      const pendingRow = row?.pending;
      const itemID = helperItemID(item);
      return (
        itemID == null ||
        !row ||
        !pendingRow ||
        pendingRow.transcript_id !== row.session.id ||
        stale.has(itemID)
      );
    },
    [stale],
  );
  const helper = useCollectiveIdentitySelection(helperDisabled);
  // The ONE identity set both surfaces read: a submission the flat queue draws
  // and a helper disclosure nests is one selection, so it is counted once and
  // both of its checkboxes state the same thing.
  const selection = helper.selectedIds;

  const shares = useMemo(() => pending ?? [], [pending]);
  const rows = useMemo(() => toReviewRows(shares, stale), [shares, stale]);
  const filteredRows = useMemo(() => applyFilters(rows, filters), [rows, filters]);
  const tree = useMemo(() => buildReviewTree(filteredRows), [filteredRows]);
  const counts = useMemo(() => harnessCounts(rows, filters.search), [rows, filters.search]);
  // Every transcript the flat tree actually draws, folded rows included. A
  // grouped owner named here is ALREADY represented, so the grouped exit below
  // skips it instead of mounting a second owner row.
  const flatOwnerIds = useMemo(
    () => new Set(tree.flatMap((project) => sessionRows(project).map((row) => row.id))),
    [tree],
  );
  const groupedFallback = useMemo(
    () => unownedGroupedItems(groupedItems, flatOwnerIds),
    [groupedItems, flatOwnerIds],
  );
  // Grouped content the flat queue did not carry, plus the way to any grouped
  // page not read yet. Either one is enough that the queue is not empty.
  const hasGroupedExit = groupedFallback.length > 0 || grouped.remainingItems > 0;

  /**
   * The selection, narrowed to the rows the QUEUE still holds.
   *
   * A selection is a set of ids and the queue is refetched, so a row a
   * reviewer ticked can disappear from under them - another owner decided it -
   * and leave its id behind. Counting that id would state a number no ticked
   * row on screen accounts for, and send it on the next decision. Reconciling
   * here rather than pruning in an effect means the count is DERIVED from the
   * queue and cannot drift from it.
   *
   * It narrows against every fetched row, NOT against the filtered tree: a row
   * hidden by the search or harness filter has not left the queue and
   * deliberately keeps its selection, which is the same rule select-all
   * follows.
   */
  const queuedIds = useMemo(() => new Set(rows.map((row) => row.id)), [rows]);
  const selected = useMemo(
    () => new Set([...selection].filter((id) => queuedIds.has(id))),
    [selection, queuedIds],
  );
  // The helper rows the queue still holds: one that left it since it was ticked
  // must not be counted or resent. Their ids are already part of `selected` (the
  // shared identity set), so the union below counts each identity once.
  const queuedHelperItems = useMemo(
    () =>
      [...helper.helperItems.values()].filter((item) => {
        const id = helperItemID(item);
        return id != null && queuedIds.has(id);
      }),
    [helper.helperItems, queuedIds],
  );
  const queuedHelperIds = useMemo(
    () => new Set(queuedHelperItems.map((item) => helperItemID(item)).filter((id): id is string => id != null)),
    [queuedHelperItems],
  );
  const selectedCount = new Set([...selected, ...queuedHelperIds]).size;

  if (groupLoading || (isOwner && pendingLoading)) {
    return (
      <div className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up">
        <div className="h-4 w-40 bg-surface-hover animate-shimmer" />
        <div className="h-16 w-72 bg-surface-hover animate-shimmer" />
        <div className="h-64 w-full bg-surface-hover animate-shimmer" />
      </div>
    );
  }

  if (!group || !isOwner) {
    return (
      <div
        className="max-w-[1600px] mx-auto px-6 pt-6 pb-12 flex flex-col gap-6 animate-fade-up"
        data-testid="review-non-owner-notice"
      >
        <div className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center">
          <p className="text-sm font-medium text-ink">
            {group
              ? "only an owner of this collective can review contributions"
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
    helper.applyTreeSelection(toggleNode(selection, node));
  }

  // Select-all / deselect-all acts on the leaves the tree currently SHOWS, so
  // a row hidden by the search or harness filter keeps whatever state it had
  // and a filtered view can never silently add a row to a decision.
  function handleToggleAll(ids: string[], selectAll: boolean) {
    const next = new Set(selection);
    for (const rowID of ids) {
      if (selectAll) next.add(rowID);
      else next.delete(rowID);
    }
    helper.applyTreeSelection(next);
  }

  async function decide(status: ReviewDecision) {
    if (selectedCount === 0) return;
    // Tree rows and individually ticked helper members are ONE set of explicit
    // transcript ids: the decision names submissions, never a group id and
    // never a parent/sibling a helper happened to be grouped under.
    const ids = [...new Set([...selected, ...groupedReviewSelection(queuedHelperItems)])];
    const outcome = await review.mutateAsync({ transcript_ids: ids, status });
    // Every id the server answered about leaves the selection: a decided row
    // is done, and a stale row can never be decided from here — leaving it
    // ticked would resend it on every later action and make the count
    // disagree with the row, which is drawn disabled. The stale ids stay
    // VISIBLE and marked, so the reviewer can see what someone else already
    // decided instead of the rows simply vanishing.
    const answered = new Set([...outcome.decided, ...outcome.already_decided]);
    setStale(new Set(outcome.already_decided));
    helper.forget(answered);
  }

  const deciding = review.isPending;

  return (
    <div className="cmg-root max-w-[1600px] mx-auto px-6 pt-6 pb-24 flex flex-col gap-6 animate-fade-up">
      <div className="flex flex-col gap-2">
        <div className="flex items-start justify-between gap-4">
          {/* `normal-case` is load-bearing: the design system lowercases
              h1/h2/h3 as UI chrome, and the collective's name is USER
              CONTENT, which is never lowercased. */}
          <h1 className="text-xl font-semibold text-ink normal-case">
            review contributions to {group.group.name}
          </h1>
          <Link
            href={`/groups/${id}`}
            className="shrink-0 text-sm text-ink-3 hover:text-ink transition-colors focus-mono cursor-pointer"
          >
            back to {group.group.name}
          </Link>
        </div>
        <p className="text-sm text-ink-3">
          choose the contributions to approve or reject, then decide them together.
        </p>
      </div>

      {tree.length === 0 && !hasGroupedExit ? (
        <div className="border border-rule bg-surface px-5 py-12 text-center" data-testid="review-empty-queue">
          <p className="text-sm text-ink-3">nothing is waiting for review in this collective.</p>
        </div>
      ) : (
        <>
          {tree.length > 0 && (
            <div className="@container" data-testid="review-panel">
              <div className="grid grid-cols-1 @[880px]:grid-cols-[minmax(20rem,2fr)_3fr] gap-4 border border-rule bg-surface min-h-[32rem]">
                <div className="border-b @[880px]:border-b-0 @[880px]:border-r border-rule min-h-[20rem] @[880px]:min-h-[32rem]">
                  <ContributeTree
                    tree={tree}
                    selection={selected}
                    onToggleNode={handleToggle}
                    onToggleAll={handleToggleAll}
                    onPreview={setPreviewId}
                    previewId={previewId}
                    filters={filters}
                    onFiltersChange={setFilters}
                    harnessCounts={counts}
                    countNoun="contribution"
                    emptyLabel="no pending contributions match this filter."
                    helperGroupSlot={(session, owner) => {
                      const groups = helperGroups.get(session.id);
                      // A submission with no saved helper group renders
                      // unchanged: no tree, and no marker claiming one.
                      if (groups == null || groups.length === 0) return undefined;
                      return (
                        <ScopedOwnerHelperTree
                          owner={owner}
                          groups={groups}
                          onRefreshOrigin={grouped.refreshOrigin}
                          selection={helper.contract}
                        />
                      );
                    }}
                  />
                </div>
                <div className="min-h-[20rem] @[880px]:min-h-[32rem]">
                  <TranscriptPreview transcriptId={previewId} />
                </div>
              </div>
            </div>
          )}
          {/* The grouped exits the flat queue above did not draw: the context
              containers it has no row for, and each submission it does not
              carry. Mounted OUTSIDE the tree branch, so an empty or partial
              queue still reaches the saved helper threads the server grouped --
              and a submission the tree already draws is skipped, never mounted
              twice. */}
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
            <p className="text-xs font-mono text-ink-3 tabular-nums" data-testid="review-selection-count">
              {selectedCount} selected
            </p>
            {stale.size > 0 && (
              <p className="text-xs font-mono text-ink-3 tabular-nums" data-testid="review-stale-notice">
                {stale.size} already decided by someone else
              </p>
            )}
            {review.isError && (
              <p className="text-xs text-danger" data-testid="review-error">
                {review.error instanceof Error ? review.error.message : "the decision could not be applied."}
              </p>
            )}
          </div>
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              disabled={selectedCount === 0 || deciding}
              onClick={() => void decide("rejected")}
            >
              reject selected
            </Button>
            <Button
              variant="primary"
              loading={deciding}
              disabled={selectedCount === 0 || deciding}
              onClick={() => void decide("approved")}
            >
              approve selected
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
