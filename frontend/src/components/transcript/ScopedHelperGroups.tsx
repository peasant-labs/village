"use client";

import { useCallback, useState, type ReactNode } from "react";
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
 * The host's per-member selection contract. Ids are individual transcript ids;
 * the component reports the exact row that was toggled and holds no state of
 * its own, so the surface that acts on a selection owns it.
 */
export interface ScopedHelperSelection {
  selectedIds: ReadonlySet<string>;
  onToggle: (transcriptId: string, selected: boolean) => void;
  /** Members the surface must not let the viewer pick (e.g. already shared). */
  isDisabled?: (transcriptId: string) => boolean;
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
  // While a page is in flight the server's own total is not yet on screen; the
  // summary's saved-identity count is the honest stand-in, never a review or
  // turn total.
  const total = payload?.total ?? group.helperThreadCount;
  const totalPages = Math.max(1, Math.ceil(total / HELPER_MEMBER_PAGE_SIZE));

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
          selectionDisabled={selection?.isDisabled?.(session.id) ?? false}
          onSelect={
            selection == null
              ? undefined
              : (id: string, nextSelected: boolean) => selection.onToggle(id, nextSelected)
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
    [onRefreshOrigin, selection],
  );

  const memberFooter =
    totalPages > 1 ? (
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
      members={rows}
      expanded={expanded}
      onExpandedChange={handleExpandedChange}
      scopeExpired={members.refreshRequired}
      onRefreshList={onRefreshOrigin}
      isMemberSelected={isMemberSelected}
      getMemberKey={(raw: unknown) => {
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
}: {
  item: VillageSessionListItem;
  onRefreshOrigin: () => void;
  selection?: ScopedHelperSelection;
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
          selected={selection?.selectedIds.has(session.id)}
          selectionDisabled={selection?.isDisabled?.(session.id) ?? false}
          onSelect={
            selection == null
              ? undefined
              : (id: string, nextSelected: boolean) => selection.onToggle(id, nextSelected)
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
 * The grouped helper rows of one page, in server order: an owner row for each
 * admitted ordinary transcript (with its helper groups), then the helper-only
 * context containers. A page with no helper groups renders nothing.
 */
export function ScopedGroupedHelperRows({
  items,
  onRefreshOrigin,
  selection,
}: {
  items: readonly VillageSessionListItem[];
  onRefreshOrigin: () => void;
  selection?: ScopedHelperSelection;
}) {
  const withGroups = items.filter((item) => (item.helperGroups ?? []).length > 0);
  if (withGroups.length === 0) return null;
  return (
    <div className="divide-y divide-rule" data-testid="grouped-helper-rows">
      {withGroups.map((item) => (
        <ScopedHelperGroupedRow
          key={itemSession(item)?.id ?? item.context?.groupId ?? ""}
          item={item}
          onRefreshOrigin={onRefreshOrigin}
          selection={selection}
        />
      ))}
    </div>
  );
}
