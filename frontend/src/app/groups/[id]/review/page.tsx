"use client";

import { use, useMemo, useState } from "react";
import Link from "next/link";
import { useGroup } from "@/lib/queries/groups";
import { usePendingShares, useBatchReview } from "@/lib/queries/groupReview";
import { buildReviewTree, toReviewRows } from "@/lib/review/tree";
import type { ReviewDecision } from "@/lib/review/types";
import { toggleNode, type Selection } from "@/lib/contribute/selection";
import { applyFilters, harnessCounts, type ContributeFilters } from "@/lib/contribute/filter";
import ContributeTree from "@/components/contribute/ContributeTree";
import TranscriptPreview from "@/components/contribute/TranscriptPreview";
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

  const [selection, setSelection] = useState<Selection>(new Set());
  const [previewId, setPreviewId] = useState<string | null>(null);
  const [filters, setFilters] = useState<ContributeFilters>({ search: "", harness: null });
  // Rows the server reported as already decided on the LAST action. They stay
  // on screen, marked and unselectable, until the refetched queue drops them:
  // a reviewer must be able to see which of their rows went stale, not just
  // watch a selection silently shrink.
  const [stale, setStale] = useState<ReadonlySet<string>>(new Set());

  const shares = useMemo(() => pending ?? [], [pending]);
  const rows = useMemo(() => toReviewRows(shares, stale), [shares, stale]);
  const filteredRows = useMemo(() => applyFilters(rows, filters), [rows, filters]);
  const tree = useMemo(() => buildReviewTree(filteredRows), [filteredRows]);
  const counts = useMemo(() => harnessCounts(rows, filters.search), [rows, filters.search]);

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
  const selectedCount = selected.size;

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
    setSelection((prev) => toggleNode(prev, node));
  }

  // Select-all / deselect-all acts on the leaves the tree currently SHOWS, so
  // a row hidden by the search or harness filter keeps whatever state it had
  // and a filtered view can never silently add a row to a decision.
  function handleToggleAll(ids: string[], selectAll: boolean) {
    setSelection((prev) => {
      const next = new Set(prev);
      for (const rowID of ids) {
        if (selectAll) next.add(rowID);
        else next.delete(rowID);
      }
      return next;
    });
  }

  async function decide(status: ReviewDecision) {
    if (selectedCount === 0) return;
    const ids = [...selected];
    const outcome = await review.mutateAsync({ transcript_ids: ids, status });
    // Every id the server answered about leaves the selection: a decided row
    // is done, and a stale row can never be decided from here — leaving it
    // ticked would resend it on every later action and make the count
    // disagree with the row, which is drawn disabled. The stale ids stay
    // VISIBLE and marked, so the reviewer can see what someone else already
    // decided instead of the rows simply vanishing.
    const answered = new Set([...outcome.decided, ...outcome.already_decided]);
    setStale(new Set(outcome.already_decided));
    setSelection((prev) => new Set([...prev].filter((sel) => !answered.has(sel))));
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

      {tree.length === 0 ? (
        <div className="border border-rule bg-surface px-5 py-12 text-center" data-testid="review-empty-queue">
          <p className="text-sm text-ink-3">nothing is waiting for review in this collective.</p>
        </div>
      ) : (
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
