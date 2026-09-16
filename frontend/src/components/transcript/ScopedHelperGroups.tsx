"use client";

import { useCallback, useState, type ReactNode } from "react";
import { RefreshCw } from "lucide-react";
import {
  HelperGroup,
  HelperGroupListItem,
  HelperThreadRow,
} from "@peasant-labs/fairtrade/ui";
import type {
  HelperGroupSummary,
  VillageSessionListItem,
} from "@peasant-labs/schema";
import { useHelperGroupMembers } from "@/lib/queries/helperGroups";

/**
 * The Village host for Fairtrade's canonical helper-group primitives.
 *
 * A grouped list response carries, on each admitted ordinary transcript, the
 * helper groups that hang off it and, for a helper whose owner is not in the
 * result, a context container with the owner's status. This module renders
 * those with the published `HelperGroupListItem` / `HelperGroup` /
 * `HelperThreadRow` primitives; it derives nothing itself.
 *
 * What the host owns, because the primitives deliberately do not:
 *
 *  • One independent expansion AND member-page state per group. A group is
 *    keyed by its own `groupId`+`memberScope` at the call site, so a response
 *    that mints a new scope for the same group resets that group alone; two
 *    open groups never share a page and one closing never moves the other.
 *  • The member request. Members come only from the server summary's opaque
 *    scope, so no caller can widen a query by adding filters; the request is
 *    sent only while the group is open.
 *  • An honest member-load state. While a page is in flight, and when the
 *    request fails for any reason other than a 409, the group states that — it
 *    never falls through to the primitive's "no saved helpers match" notice,
 *    which would describe a failed load as an empty result. The recovery is a
 *    retry of the SAME scope and page, never a broader request.
 *  • Fail-closed scope expiry. An expired/invalid scope is a 409, and the
 *    primitive shows only a refresh of the ORIGINATING list. This host passes
 *    that refresh straight through and never substitutes an all-members view.
 *  • Explicit per-member selection. Selection is a set of transcript ids and a
 *    toggle; it never infers siblings, the owner, or a whole group from one
 *    pick, so a member the viewer did not check is never submitted.
 */

/** How many members one disclosure loads per page. */
export const HELPER_MEMBER_PAGE_SIZE = 20;

/**
 * The member state a group states while it holds no members of its own.
 *
 * The published primitive draws a member list or, when the list is empty, its
 * own "no saved helpers match the current query and access" notice. That copy
 * is TRUE for a successful empty page and FALSE for a request still in flight
 * or one that failed, so the host supplies this one non-member row instead of
 * letting an empty array claim a successful result. It is never a member: it
 * carries no identity, no link and no checkbox.
 */
type MemberLoadState = "loading" | "failed";

const MEMBER_LOAD_ROWS: Record<MemberLoadState, { memberLoadState: MemberLoadState }> = {
  loading: { memberLoadState: "loading" },
  failed: { memberLoadState: "failed" },
};

/** The host's own member state row, or null for an ordinary member item. */
function memberLoadState(raw: unknown): MemberLoadState | null {
  if (raw == null || typeof raw !== "object") return null;
  const value = (raw as { memberLoadState?: unknown }).memberLoadState;
  return value === "loading" || value === "failed" ? value : null;
}

/** The same scope and page restated, so a reader knows what a retry asks for. */
function MemberLoadFailed({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="helper-group-notice" role="alert" data-testid="helper-group-load-failed">
      <p>
        these saved helpers could not be loaded, so no members are shown. the request stayed
        scoped to this group and nothing broader was loaded. retry to ask for the same scope
        again, or refresh the originating list.
      </p>
      <button type="button" className="helper-group-action" onClick={onRetry}>
        <RefreshCw aria-hidden="true" /> retry
      </button>
    </div>
  );
}

function MemberLoadPending() {
  return (
    <p className="helper-group-notice" role="status" data-testid="helper-group-loading">
      loading this group&apos;s saved helpers.
    </p>
  );
}

/**
 * The host's per-member selection contract. The component reports the exact
 * display item that was toggled — the same item the member endpoint served,
 * carrying the route arm (`contributable`/`pending`) a collective action needs —
 * and holds no state of its own, so the surface that acts on a selection owns
 * it. A surface that only needs the id reads `item.transcript?.session.id`
 * rather than receiving a second, partially-populated callback.
 */
export interface ScopedHelperSelection {
  selectedIds: ReadonlySet<string>;
  onToggle: (item: VillageSessionListItem, selected: boolean) => void;
  /** Items the surface must not let the viewer pick (e.g. already shared). */
  isDisabled?: (item: VillageSessionListItem) => boolean;
}

/** The transcript a display item carries, or undefined for a context container. */
export function itemSession(item: VillageSessionListItem | undefined) {
  return item?.transcript?.session;
}

/**
 * The helper groups each top-level transcript carries, indexed by that
 * transcript's id — the key the row list already has in hand at the row it is
 * drawing. Helper-group membership is never inferred from a parent id.
 */
export function helperGroupsByTranscript(
  items: readonly VillageSessionListItem[],
): Map<string, HelperGroupSummary[]> {
  const byTranscript = new Map<string, HelperGroupSummary[]>();
  for (const item of items) {
    const id = itemSession(item)?.id;
    if (id != null && item.helperGroups != null && item.helperGroups.length > 0) {
      byTranscript.set(id, item.helperGroups);
    }
  }
  return byTranscript;
}

/** The helper-only context containers, in server order. */
export function contextContainers(
  items: readonly VillageSessionListItem[],
): VillageSessionListItem[] {
  return items.filter((item) => item.kind === "context_container" && item.context != null);
}

/**
 * One owner-anchored helper group and its own independent disclosure page.
 *
 * The caller keys this component by `groupId`+`memberScope`; a new scope is a
 * new disclosure, which is what makes expiry fail closed instead of reusing a
 * closed group's stale page.
 */
export function ScopedHelperGroup({
  group,
  onRefreshOrigin,
  selection,
}: {
  group: HelperGroupSummary;
  onRefreshOrigin: () => void;
  selection?: ScopedHelperSelection;
}) {
  const [expanded, setExpanded] = useState(false);
  const [page, setPage] = useState(1);
  const members = useHelperGroupMembers(group, {
    expanded,
    page,
    limit: HELPER_MEMBER_PAGE_SIZE,
  });
  const payload = members.data;
  const rows = payload?.members ?? [];
  const refetchMembers = members.refetch;
  // While a page is in flight the server's own total is not yet on screen; the
  // summary's saved-identity count is the honest stand-in, never a review or
  // turn total.
  const total = payload?.total ?? group.helperThreadCount;
  const totalPages = Math.max(1, Math.ceil(total / HELPER_MEMBER_PAGE_SIZE));

  // A 409 is the primitive's own fail-closed expiry state; every other failure
  // is the host's to state. `members.isPending` covers the first page, a page
  // change and an expanded group whose request has not answered yet — all of
  // which would otherwise render as a successful empty result.
  const loadState: MemberLoadState | null =
    members.isError && !members.refreshRequired
      ? "failed"
      : members.isPending
        ? "loading"
        : null;
  // A single host-owned row stands in for the member list while there is none
  // to draw. It is never a member: it has no transcript identity and renders
  // only the state it reports.
  const displayedRows: unknown[] = loadState == null ? rows : [MEMBER_LOAD_ROWS[loadState]];

  const handleExpandedChange = useCallback((open: boolean) => {
    setExpanded(open);
  }, []);

  const isMemberSelected = selection
    ? (member: VillageSessionListItem) => {
        const id = itemSession(member)?.id;
        return id != null && selection.selectedIds.has(id);
      }
    : undefined;

  const renderMember = useCallback(
    (raw: unknown): ReactNode => {
      const state = memberLoadState(raw);
      if (state === "loading") return <MemberLoadPending />;
      if (state === "failed") return <MemberLoadFailed onRetry={() => void refetchMembers()} />;
      const member = raw as VillageSessionListItem;
      const session = itemSession(member);
      if (session == null) {
        // Member pages are transcript-only; a context container here would be a
        // contract violation and is never fabricated into a selectable row.
        return null;
      }
      const selected = selection?.selectedIds.has(session.id) ?? false;
      const nested = member.helperGroups;
      return (
        <HelperThreadRow
          id={session.id}
          title={session.title ?? "untitled transcript"}
          provider={session.model_provider}
          inputSubmissionCount={session.input_submission_count ?? undefined}
          turnCount={session.turn_count ?? undefined}
          href={`/transcripts/${encodeURIComponent(session.id)}`}
          selected={selection == null ? undefined : selected}
          selectionDisabled={selection?.isDisabled?.(member) ?? false}
          onSelect={
            selection == null
              ? undefined
              : (_id: string, nextSelected: boolean) => selection.onToggle(member, nextSelected)
          }
        >
          {nested?.map((child) => (
            <ScopedHelperGroup
              key={`${child.groupId}:${child.memberScope}`}
              group={child}
              onRefreshOrigin={onRefreshOrigin}
              selection={selection}
            />
          ))}
        </HelperThreadRow>
      );
    },
    [refetchMembers, onRefreshOrigin, selection],
  );

  // Paging belongs to a loaded page. While the members are loading or have
  // failed there is no page to page from, so the controls are withheld rather
  // than offering a second request against the same unanswered scope.
  const memberFooter =
    loadState == null && totalPages > 1 ? (
      <div className="flex items-center gap-3" data-testid="helper-group-pager">
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          disabled={page <= 1}
          onClick={() => setPage((current) => Math.max(1, current - 1))}
        >
          previous
        </button>
        <span className="font-mono text-xs text-ink-3 tabular-nums">
          page {page} of {totalPages}
        </span>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          disabled={page >= totalPages}
          onClick={() => setPage((current) => Math.min(totalPages, current + 1))}
        >
          next
        </button>
      </div>
    ) : null;

  return (
    <HelperGroup
      groupId={group.groupId}
      memberScope={group.memberScope}
      helperThreadCount={group.helperThreadCount}
      members={displayedRows}
      expanded={expanded}
      onExpandedChange={handleExpandedChange}
      scopeExpired={members.refreshRequired}
      onRefreshList={onRefreshOrigin}
      isMemberSelected={isMemberSelected}
      getMemberKey={(raw: unknown) => {
        const state = memberLoadState(raw);
        if (state != null) return `member-load-${state}`;
        const member = raw as VillageSessionListItem;
        return itemSession(member)?.id ?? member.context?.groupId ?? group.groupId;
      }}
      renderMember={renderMember}
      memberFooter={memberFooter}
    />
  );
}

/**
 * A helper whose owner is not in the result. The primitive renders the
 * authorized owner status and never a fabricated row, title, or link.
 */
export function ScopedContextHelperGroups({
  item,
  onRefreshOrigin,
  selection,
}: {
  item: VillageSessionListItem;
  onRefreshOrigin: () => void;
  selection?: ScopedHelperSelection;
}) {
  const context = item.context;
  if (context == null) return null;
  return (
    <HelperGroupListItem ownerStatus={context.ownerStatus}>
      {(item.helperGroups ?? []).map((group) => (
        <ScopedHelperGroup
          key={`${group.groupId}:${group.memberScope}`}
          group={group}
          onRefreshOrigin={onRefreshOrigin}
          selection={selection}
        />
      ))}
    </HelperGroupListItem>
  );
}

/**
 * The helper groups attached to one admitted owner row, rendered directly
 * beneath that row. The caller obtains them from the grouped list response and
 * passes them down; this component never infers a group from a row.
 *
 * A caller that does NOT keep its own ordinary row (its list draws the row
 * elsewhere, or draws none at all) mounts this one: the groups render as one
 * disclosure block and, because it mounts per-member checkboxes only when it is
 * handed a selection, a display-only mount draws no connector. A caller that
 * keeps its own ordinary row mounts {@link ScopedOwnerHelperTree} instead, so
 * the tree has that row as its owner.
 */
export function ScopedOwnerHelperGroups({
  groups,
  onRefreshOrigin,
  selection,
}: {
  groups: readonly HelperGroupSummary[] | undefined;
  onRefreshOrigin: () => void;
  selection?: ScopedHelperSelection;
}) {
  if (groups == null || groups.length === 0) return null;
  return (
    <div className="pl-5" data-testid="owner-helper-groups">
      {groups.map((group) => (
        <ScopedHelperGroup
          key={`${group.groupId}:${group.memberScope}`}
          group={group}
          onRefreshOrigin={onRefreshOrigin}
          selection={selection}
        />
      ))}
    </div>
  );
}

/**
 * The same helper groups, mounted as Fairtrade's canonical helper tree around
 * the ordinary owner row the caller already draws.
 *
 * The row is passed as `owner`, so the tree measures a single connector through
 * the checkboxes of the rows it holds: the per-member checkboxes the members
 * mount. The owner row stays the caller's own component - its own checkbox, its
 * own selection behavior, its own hierarchy connector - which is the
 * composition the design system documents for a host row it retains, and the
 * caller mounts this only for a row that carries at least one saved helper
 * group (a row without one renders unchanged, with no tree around it).
 */
export function ScopedOwnerHelperTree({
  owner,
  groups,
  onRefreshOrigin,
  selection,
}: {
  /** The ordinary owner row the caller draws, already mounted as `owner`. */
  owner: ReactNode;
  groups: readonly HelperGroupSummary[];
  onRefreshOrigin: () => void;
  selection?: ScopedHelperSelection;
}) {
  return (
    <HelperGroupListItem owner={owner}>
      {groups.map((group) => (
        <ScopedHelperGroup
          key={`${group.groupId}:${group.memberScope}`}
          group={group}
          onRefreshOrigin={onRefreshOrigin}
          selection={selection}
        />
      ))}
    </HelperGroupListItem>
  );
}

/**
 * The helper-only context containers of one grouped page, in server order.
 * These are read context, never rows: they carry no owner action and cannot be
 * selected.
 */
export function ScopedContextContainerList({
  items,
  onRefreshOrigin,
  selection,
}: {
  items: readonly VillageSessionListItem[];
  onRefreshOrigin: () => void;
  selection?: ScopedHelperSelection;
}) {
  const containers = contextContainers(items);
  if (containers.length === 0) return null;
  return (
    <div className="divide-y divide-rule" data-testid="grouped-context-containers">
      {containers.map((item) => (
        <ScopedContextHelperGroups
          key={item.context?.groupId ?? ""}
          item={item}
          onRefreshOrigin={onRefreshOrigin}
          selection={selection}
        />
      ))}
    </div>
  );
}

/**
 * One owner row and its helper groups, for a surface that is not already
 * drawing the row itself (e.g. a discovery grid). The owner row is the same
 * explicitly linked transcript row the member pages use; the immediate helper
 * groups hang beneath it with their own disclosure state.
 */
export function ScopedHelperGroupedRow({
  item,
  onRefreshOrigin,
  selection,
  selectOwner = true,
}: {
  item: VillageSessionListItem;
  onRefreshOrigin: () => void;
  selection?: ScopedHelperSelection;
  /**
   * Whether the owner row itself is selectable. A surface whose ordinary list
   * is the authority for its own owner rows mounts a fallback owner read-only
   * and selects only inside the disclosures, so one transcript identity never
   * gets two selection controls.
   */
  selectOwner?: boolean;
}) {
  const session = itemSession(item);
  const groups = item.helperGroups ?? [];
  if (session == null) {
    return (
      <ScopedContextHelperGroups
        item={item}
        onRefreshOrigin={onRefreshOrigin}
        selection={selection}
      />
    );
  }
  const ownerSelection = selectOwner ? selection : undefined;
  return (
    <HelperGroupListItem
      owner={
        <HelperThreadRow
          id={session.id}
          title={session.title ?? "untitled transcript"}
          provider={session.model_provider}
          inputSubmissionCount={session.input_submission_count ?? undefined}
          turnCount={session.turn_count ?? undefined}
          href={`/transcripts/${encodeURIComponent(session.id)}`}
          selected={ownerSelection?.selectedIds.has(session.id)}
          selectionDisabled={ownerSelection?.isDisabled?.(item) ?? false}
          onSelect={
            ownerSelection == null
              ? undefined
              : (_id: string, nextSelected: boolean) => ownerSelection.onToggle(item, nextSelected)
          }
        />
      }
    >
      {groups.map((group) => (
        <ScopedHelperGroup
          key={`${group.groupId}:${group.memberScope}`}
          group={group}
          onRefreshOrigin={onRefreshOrigin}
          selection={selection}
        />
      ))}
    </HelperGroupListItem>
  );
}

/**
 * The grouped rows of one page, in server order: an owner row for each admitted
 * ordinary transcript (with its helper groups), then the helper-only context
 * containers. Every item on the page renders exactly once — a helper-only
 * context is drawn here, not here AND again by a second renderer. A page whose
 * items carry neither a helper group nor a context container renders nothing.
 */
export function ScopedGroupedHelperRows({
  items,
  onRefreshOrigin,
  selection,
  selectOwner,
}: {
  items: readonly VillageSessionListItem[];
  onRefreshOrigin: () => void;
  selection?: ScopedHelperSelection;
  /** Forwarded to each owner row; see {@link ScopedHelperGroupedRow}. */
  selectOwner?: boolean;
}) {
  // A context container is read context whether or not it currently carries a
  // group summary, so it is kept in the list either way; an ordinary row with no
  // saved helpers has nothing to disclose and is skipped.
  const disclosed = items.filter(
    (item) => item.kind === "context_container" || (item.helperGroups ?? []).length > 0,
  );
  if (disclosed.length === 0) return null;
  return (
    <div className="divide-y divide-rule" data-testid="grouped-helper-rows">
      {disclosed.map((item) => (
        <ScopedHelperGroupedRow
          key={itemSession(item)?.id ?? item.context?.groupId ?? ""}
          item={item}
          onRefreshOrigin={onRefreshOrigin}
          selection={selection}
          selectOwner={selectOwner}
        />
      ))}
    </div>
  );
}

/**
 * The grouped items the flat rendering of THIS surface does not already draw:
 * every helper-only context container, and each owner row whose transcript is
 * not among `representedOwnerIds` and that carries at least one saved helper
 * group.
 *
 * A flat surface consumes the grouped page through its row slots, so a grouped
 * owner that the flat result does not carry has no slot to hang its group under
 * — and a context container has no owner row at all. This derives the exit for
 * exactly those items, from the admitted grouped page, so the grouped content is
 * reachable whether the flat result is empty, partial, or in an alternate view.
 * An owner the flat rendering DOES draw is named in `representedOwnerIds` and is
 * skipped here, so no owner row and no group is ever mounted twice.
 */
export function unownedGroupedItems(
  items: readonly VillageSessionListItem[],
  representedOwnerIds: ReadonlySet<string>,
): VillageSessionListItem[] {
  return items.filter((item) => {
    if (item.kind === "context_container") return true;
    const id = itemSession(item)?.id;
    return id != null && !representedOwnerIds.has(id) && (item.helperGroups ?? []).length > 0;
  });
}

/**
 * The grouped items a flat surface has not drawn, mounted below it. The owner
 * row is drawn read-only — the ordinary list stays the authority for its own
 * selectable rows — while each disclosure keeps its per-member selection
 * semantics. Nothing here infers a group from a row or a row from a group.
 */
export function ScopedUnownedHelperGroups({
  items,
  representedOwnerIds,
  onRefreshOrigin,
  selection,
}: {
  items: readonly VillageSessionListItem[];
  representedOwnerIds: ReadonlySet<string>;
  onRefreshOrigin: () => void;
  selection?: ScopedHelperSelection;
}) {
  return (
    <ScopedGroupedHelperRows
      items={unownedGroupedItems(items, representedOwnerIds)}
      onRefreshOrigin={onRefreshOrigin}
      selection={selection}
      selectOwner={false}
    />
  );
}
