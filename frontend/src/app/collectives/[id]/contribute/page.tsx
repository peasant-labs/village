"use client";

import { use, useCallback, useMemo, useState } from "react";
import Link from "next/link";
import { Lock } from "lucide-react";
import { useAuth } from "@/providers/AuthProvider";
import { loginHref } from "@/lib/signIn";
import { OnCrumb, OnEmpty, OnHead, OnPage } from "@/components/online/Page";
import type { VillageSessionListItem } from "@peasant-labs/schema";
import { useGroup } from "@/lib/queries/groups";
import { useContributable, useContributeRun, partitionRunOutcome } from "@/lib/queries/groupShares";
import { useGroupedContributable } from "@/lib/queries/groupedCollectives";
import { GROUPED_TOP_LEVEL_PAGE_SIZE } from "@/lib/queries/helperGroups";
import { buildContributeTree } from "@/lib/contribute/tree";
import { groupByProject, privateIds, sessionRows, toggleNode } from "@/lib/contribute/selection";
import { groupedContributionBatches, mergeContributionBatches } from "@/lib/contribute/groupedSelection";
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
import ConfirmContributeDialog from "@/components/transcript/ConfirmContributeDialog";
import { Button } from "@/lib/ft-ui";

/**
 * Dedicated contribute route for a collective: `/collectives/{id}/contribute`.
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
  const { isLoading: sessionLoading, isLoggedIn } = useAuth();
  const { data: contributable, isLoading: contributableLoading } = useContributable(id);
  const run = useContributeRun(id);

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
  const helper = useCollectiveIdentitySelection(helperDisabled);
  // The ONE identity set both surfaces read: a transcript the tree draws and a
  // helper disclosure nests is one selection, so it is counted once and both of
  // its checkboxes state the same thing.
  const selection = helper.selectedIds;
  const helperPrivateSelected = useMemo(
    () => [...helper.helperItems.values()].filter((item) => item.transcript?.session.visibility === "private"),
    [helper.helperItems],
  );
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

  function handleToggle(node: Parameters<typeof toggleNode>[1]) {
    helper.applyTreeSelection(toggleNode(selection, node));
  }

  // Select-all / deselect-all acts on the leaves the tree currently SHOWS: a
  // row hidden by the search or harness filter keeps whatever state it had, so
  // a filtered view can never silently drop (or silently add) a selection the
  // viewer cannot see.
  function handleToggleAll(ids: string[], selectAll: boolean) {
    const next = new Set(selection);
    for (const id of ids) {
      if (selectAll) next.add(id);
      else next.delete(id);
    }
    helper.applyTreeSelection(next);
  }

  async function startRun(visibilityConfirmed: boolean) {
    // Tree rows and individually ticked helper members are ONE set of explicit
    // transcript ids. The helper batches are built from the exact row each
    // member endpoint served, so nothing here sends a group id or infers a
    // sibling: the ordinary project/branch selection is the pre-existing
    // per-transcript selection, and a helper is only ever its own id.
    const batches = mergeContributionBatches(
      groupByProject(selection, tree),
      groupedContributionBatches([...helper.helperItems.values()]),
    );
    if (batches.size === 0) return;
    const results = await run.run(batches, visibilityConfirmed);
    const { clearedIds } = partitionRunOutcome(batches, results);
    helper.forget(clearedIds);
    setConfirmOpen(false);
  }

  function handleContributeClick() {
    if (selectedCount === 0) return;
    // The confirm list and the gate are both derived from UNIQUE ids: one
    // identity the tree and a helper disclosure both hold is one transcript to
    // confirm, not two.
    const privates = new Set([
      ...privateIds(selection, tree),
      ...helperPrivateSelected.map((item) => helperItemID(item)).filter((id): id is string => id != null),
    ]);
    if (privates.size > 0) {
      setConfirmOpen(true);
      return;
    }
    void startRun(false);
  }

  const privateSelectedItems = (() => {
    const byID = new Map<string, { id: string; title: string }>();
    for (const transcriptId of privateIds(selection, tree)) {
      const source = rows.find((row) => row.id === transcriptId);
      byID.set(transcriptId, { id: transcriptId, title: source?.title ?? transcriptId });
    }
    for (const item of helperPrivateSelected) {
      const id = helperItemID(item);
      if (id == null || byID.has(id)) continue;
      byID.set(id, { id, title: item.transcript?.session.title ?? id });
    }
    return [...byID.values()];
  })();

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

      {/* `contribute-member-panel` is the stable member-view testid this
          route has carried since village#64 (the interim single-panel
          shell); kept on the tree+preview composition that replaces that
          panel's body so a caller keyed on "the member sees SOME contribute
          UI" does not need to know which body variant is mounted. */}
      {tree.length === 0 && !hasGroupedExit ? (
        <div className="border border-rule bg-surface px-5 py-12 text-center" data-testid="contribute-member-panel">
          <p className="mx-auto text-sm text-ink-3">
            all your transcripts are already shared with this collective, or you have no
            published transcripts.
          </p>
        </div>
      ) : (
        <>
          {tree.length > 0 && (
            <div className="on-contribute-workspace" data-testid="contribute-member-panel">
              <div className="on-contribute-columns">
                <div className="on-contribute-browser">
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
                    helperGroupSlot={(session, owner) => {
                      const groups = helperGroups.get(session.id);
                      // A row with no saved helper group renders unchanged: no
                      // tree, and no marker claiming one.
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
                <div className="on-contribute-preview">
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

      <div className="on-contribute-footer">
        <div className="on-contribute-footer-inner">
        <div className="flex flex-col gap-1 min-w-0">
          <p className="text-[length:var(--fs-label)] font-mono text-ink-3 tabular-nums" data-testid="contribute-selection-count">{selectedCount} selected</p>
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
            <ul className="flex flex-col gap-1 text-[length:var(--fs-body)] text-ink-3" data-testid="contribute-run-receipt">
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

    </OnPage>

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
    </>
  );
}
