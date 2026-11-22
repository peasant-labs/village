"use client";

import { useCallback, useMemo, useState } from "react";
import { ChevronRight } from "lucide-react";
import type { ContributeNode, ProjectNode, SessionNode, TreeRowFacts } from "@/lib/contribute/tree";
import { leafIds, nodeState, type Selection } from "@/lib/contribute/selection";
import type { ContributeFilters } from "@/lib/contribute/filter";
import { Button, Input, Select, Tag } from "@/lib/ft-ui";
import { SessionGroupDisclosure } from "@/lib/ft-ui";
import { childSessionGroupLabel, childSessionGroupSelectionLabel } from "@/lib/childSessions";
import TriStateCheckbox from "./TriStateCheckbox";
import { TreeRail, useTreeRail, type RailRow } from "./treeRail";

interface ContributeTreeProps {
  tree: ProjectNode<TreeRowFacts>[];
  selection: Selection;
  onToggleNode: (node: ContributeNode<TreeRowFacts>) => void;
  /** Select or clear EVERY selectable leaf currently visible in the tree. */
  onToggleAll: (ids: string[], selectAll: boolean) => void;
  onPreview: (transcriptId: string) => void;
  previewId: string | null;
  filters: ContributeFilters;
  onFiltersChange: (next: ContributeFilters) => void;
  harnessCounts: Map<string, number>;
  /** The tally line under the filter bar, and the empty-filter message.
   *  A contributor is choosing what to OFFER; a moderator is choosing what to
   *  DECIDE, so the two pages count the same rows in different words. */
  countNoun?: string;
  emptyLabel?: string;
  /** Review queues keep session context inline rather than nesting projects. */
  layout?: "tree" | "sessions";
  sessionActions?: (session: SessionNode<TreeRowFacts>) => React.ReactNode;
  sessionFacts?: (session: SessionNode<TreeRowFacts>) => React.ReactNode;
  selectionSummary?: React.ReactNode;
  /** The saved helper groups hanging off one session row, mounted by the page
   *  that owns the grouped read. The slot receives the row this tree already
   *  draws (and the sessions it started) so the page can mount that very row as
   *  the OWNER of the canonical helper tree the groups hang in; the connector
   *  then traces the per-member checkboxes. The group renders directly beneath
   *  the row it belongs to, so the group's members are selectable without a
   *  second, duplicated copy of the owner row. Returning null (or undefined)
   *  means "this row carries no saved helper group": the row renders unchanged.
   *  The page owns the selection state. */
  helperGroupSlot?: (
    session: SessionNode<TreeRowFacts>,
    owner: React.ReactNode,
  ) => React.ReactNode;
}

/** Depth column each row type occupies in the connector: project 0, branch (or
 *  the synthetic orphans grouping) 1, session 2, a session's own child 3, and
 *  so on for deeper descendants. */
const RAIL_DEPTH = { project: 0, group: 1, session: 2 } as const;

function SessionRow({
  session,
  depth,
  selection,
  onToggleNode,
  onPreview,
  previewId,
  setRowElement,
  openChildren,
  onToggleChildren,
  helperGroupSlot,
  sessionActions,
  sessionFacts,
}: {
  sessionActions?: ContributeTreeProps["sessionActions"];
  sessionFacts?: ContributeTreeProps["sessionFacts"];
  session: SessionNode<TreeRowFacts>;
  depth: number;
  selection: Selection;
  onToggleNode: (node: ContributeNode<TreeRowFacts>) => void;
  onPreview: (transcriptId: string) => void;
  previewId: string | null;
  setRowElement: (key: string, element: HTMLElement | null) => void;
  openChildren: ReadonlySet<string>;
  onToggleChildren: (sessionId: string) => void;
  helperGroupSlot?: (
    session: SessionNode<TreeRowFacts>,
    owner: React.ReactNode,
  ) => React.ReactNode;
}) {
  const childrenOpen = openChildren.has(session.id);
  const state = nodeState(selection, session);
  const isActive = previewId === session.id;
  const rowRef = useCallback(
    (element: HTMLDivElement | null) => setRowElement(`s:${session.id}`, element),
    [session.id, setRowElement],
  );

  const row = (
    <div
      ref={rowRef}
      className={`contribute-session-row flex items-center gap-3 px-4 py-3 transition-colors ${
        isActive ? "bg-surface-hover" : "hover:bg-surface-hover"
      }`}
      data-preview-active={isActive || undefined}
      data-testid={`contribute-session-row-${session.id}`}
    >
      <TriStateCheckbox
        state={state}
        disabled={session.locked}
        onChange={() => onToggleNode(session)}
        label={`select session ${session.label}`}
      />
      {/* A narrow pane puts marks below the title so they cannot consume its
          reading space. Metadata keeps its own line in either presentation. */}
      <div className="min-w-0 flex-1">
        <div className="contribute-session-heading flex items-center gap-3">
          <button
            type="button"
            aria-pressed={isActive}
            onClick={() => onPreview(session.id)}
            className="min-w-0 flex-1 text-sm text-ink truncate text-left cursor-pointer focus-mono"
          >
            {session.label}
          </button>
          {session.mark != null && <Tag className="contribute-session-mark shrink-0">{session.mark}</Tag>}
        </div>
        <div className="font-mono text-[length:var(--fs-label)] text-ink-3 tabular-nums [overflow-wrap:anywhere]">{sessionFacts?.(session) ?? session.meta}</div>
        {sessionActions?.(session)}
      </div>
    </div>
  );

  // The sessions this one started, behind the SAME control the home page
  // and every other transcript list uses. The marker attribute is the same
  // one those lists carry, so what a control belongs to is observable here
  // in the same way rather than inferred from the order elements happen to
  // appear in.
  const startedSessions = session.children.length > 0 && (
    <div data-parent-transcript-id={session.id}>
      <SessionGroupDisclosure
        // Open, the rows are on screen and their own ticks say what is
        // selected. CLOSED, they are not, so the control carries the count
        // itself. A session's checkbox governs its whole subtree, so a
        // selected hidden row does leave this row ticked or part-ticked --
        // but an indeterminate mark is something a reader has to infer,
        // and a count is something the control states.
        label={childSessionGroupLabel(session.children.length)}
        collapsedLabel={childSessionGroupSelectionLabel(
          session.children.length,
          session.children.filter((child) => selection.has(child.id)).length,
        )}
        expanded={childrenOpen}
        onToggle={() => onToggleChildren(session.id)}
        rowsID={`contribute-child-sessions-${session.id}`}
        testID="child-session-disclosure"
        bare
        indent
      >
        <div
          id={`contribute-child-sessions-${session.id}`}
          data-testid="child-session-disclosure-rows"
          className="contribute-subtree"
        >
          {session.children.map((child) => (
            <SessionRow
              key={child.id}
              session={child}
              depth={depth + 1}
              selection={selection}
              onToggleNode={onToggleNode}
              onPreview={onPreview}
              previewId={previewId}
              setRowElement={setRowElement}
              openChildren={openChildren}
              onToggleChildren={onToggleChildren}
              helperGroupSlot={helperGroupSlot}
              sessionActions={sessionActions}
              sessionFacts={sessionFacts}
            />
          ))}
        </div>
      </SessionGroupDisclosure>
    </div>
  );

  // The row and the sessions it started are ONE owner slot: the page mounts
  // them as the owner of the canonical helper tree it composes around the saved
  // helper groups, so the row keeps its own checkbox and this tree's own
  // hierarchy connector while the helper tree adds its connector over the
  // members. Both stay inside that slot, so the order a reader sees - the row,
  // its started sessions, then its saved helpers - does not move.
  const ownerRow = (
    <>
      {row}
      {startedSessions}
    </>
  );
  const grouped = helperGroupSlot?.(session, ownerRow);

  return (
    <div>
      {grouped == null ? (
        ownerRow
      ) : (
        // The marker names the owner row a group hangs under, so a group's place
        // in the tree is observable rather than inferred.
        <div data-helper-group-owner={session.id}>{grouped}</div>
      )}
    </div>
  );
}

/** A branch (or the synthetic orphans grouping) and the sessions under it. */
function GroupRow({
  node,
  selection,
  onToggleNode,
  onPreview,
  previewId,
  setRowElement,
  openChildren,
  onToggleChildren,
  helperGroupSlot,
}: {
  node: ContributeNode<TreeRowFacts> & { children: SessionNode<TreeRowFacts>[] };
  selection: Selection;
  onToggleNode: (node: ContributeNode<TreeRowFacts>) => void;
  onPreview: (transcriptId: string) => void;
  previewId: string | null;
  setRowElement: (key: string, element: HTMLElement | null) => void;
  openChildren: ReadonlySet<string>;
  onToggleChildren: (sessionId: string) => void;
  helperGroupSlot?: (
    session: SessionNode<TreeRowFacts>,
    owner: React.ReactNode,
  ) => React.ReactNode;
}) {
  const state = nodeState(selection, node);
  // A branch names itself; the synthetic orphans grouping is not a branch and
  // must not claim to be one, so only a real branch carries the prefix.
  const label = node.kind === "branch" ? `branch · ${node.label}` : node.label;
  const groupRef = useCallback(
    (element: HTMLHeadingElement | null) => setRowElement(`g:${node.id}`, element),
    [node.id, setRowElement],
  );
  return (
    <div>
      <h3
        ref={groupRef}
        className="flex items-center gap-3 px-4 py-2 font-mono text-sm text-ink-3"
        data-testid={`contribute-group-row-${node.id}`}
      >
        <TriStateCheckbox
          state={state}
          onChange={() => onToggleNode(node)}
          label={`select ${label}`}
        />
        <span className="truncate">{label}</span>
      </h3>
      <div className="contribute-subtree">
        {node.children.map((session) => (
          <SessionRow
            key={session.id}
            session={session}
            depth={RAIL_DEPTH.session}
            selection={selection}
            onToggleNode={onToggleNode}
            onPreview={onPreview}
            previewId={previewId}
            setRowElement={setRowElement}
            openChildren={openChildren}
            onToggleChildren={onToggleChildren}
            helperGroupSlot={helperGroupSlot}
          />
        ))}
      </div>
    </div>
  );
}

function ProjectRow({
  project,
  open,
  onToggleOpen,
  selection,
  onToggleNode,
  onPreview,
  previewId,
  setRowElement,
  openChildren,
  onToggleChildren,
  helperGroupSlot,
}: {
  project: ProjectNode<TreeRowFacts>;
  open: boolean;
  onToggleOpen: () => void;
  selection: Selection;
  onToggleNode: (node: ContributeNode<TreeRowFacts>) => void;
  onPreview: (transcriptId: string) => void;
  previewId: string | null;
  setRowElement: (key: string, element: HTMLElement | null) => void;
  openChildren: ReadonlySet<string>;
  onToggleChildren: (sessionId: string) => void;
  helperGroupSlot?: (
    session: SessionNode<TreeRowFacts>,
    owner: React.ReactNode,
  ) => React.ReactNode;
}) {
  const state = nodeState(selection, project);
  const projectRef = useCallback(
    (element: HTMLHeadingElement | null) => setRowElement(`p:${project.id}`, element),
    [project.id, setRowElement],
  );
  return (
    <section className="border-b border-rule last:border-b-0" aria-label={`project ${project.label}`}>
      <h2 ref={projectRef} className="flex items-center gap-3 px-4 py-3 font-mono font-semibold text-sm text-ink">
        <TriStateCheckbox
          state={state}
          onChange={() => onToggleNode(project)}
          label={`select project ${project.label}`}
        />
        {/* The collapse control follows the checkbox so the checkbox columns
            stay strictly nested (project left of branch left of session) -
            that ordering is what the single connector traces. */}
        <button
          type="button"
          onClick={onToggleOpen}
          aria-label={open ? `collapse project ${project.label}` : `expand project ${project.label}`}
          className="inline-flex size-6 shrink-0 items-center justify-center text-ink-3 hover:text-ink hover:bg-surface-hover transition-colors cursor-pointer focus-mono"
        >
          <ChevronRight
            className={`size-3.5 transition-transform duration-150 ${open ? "rotate-90" : ""}`}
          />
        </button>
        <span className="truncate">{project.label}</span>
      </h2>
      {open && (
        <div className="contribute-subtree">
          {project.children.map((child) => (
            <GroupRow
              key={child.id}
              node={child}
              selection={selection}
              onToggleNode={onToggleNode}
              onPreview={onPreview}
              previewId={previewId}
              setRowElement={setRowElement}
              openChildren={openChildren}
              onToggleChildren={onToggleChildren}
              helperGroupSlot={helperGroupSlot}
            />
          ))}
        </div>
      )}
    </section>
  );
}

/**
 * The contribute page's left column: the project > branch > session tree
 * (including the per-project synthetic orphans grouping), a search + harness
 * filter, a selection header, and the tri-state selection checkboxes traced by
 * one continuous hierarchy connector. Clicking a session's title (not hovering
 * it) drives `onPreview` - the right-hand preview column.
 *
 * The wire rows carry no token total, so the header counts SESSIONS rather
 * than tokens; if the contributable contract ever grows a token count, this is
 * the one place that reads it.
 */
export default function ContributeTree({
  tree,
  selection,
  onToggleNode,
  onToggleAll,
  onPreview,
  previewId,
  filters,
  onFiltersChange,
  harnessCounts,
  countNoun = "session",
  emptyLabel = "no contributable sessions match this filter.",
  layout = "tree",
  sessionActions,
  sessionFacts,
  selectionSummary,
  helperGroupSlot,
}: ContributeTreeProps) {
  const [collapsedProjects, setCollapsedProjects] = useState<ReadonlySet<string>>(new Set());
  const [openChildren, setOpenChildren] = useState<ReadonlySet<string>>(new Set());
  // Every selectable leaf currently visible, and how much of it is selected -
  // the header's counts and the select-all control read from this one place so
  // they can never disagree with the rows.
  const selectableIds = useMemo(() => tree.flatMap((project) => leafIds(project)), [tree]);
  const selectedVisibleCount = useMemo(
    () => selectableIds.filter((id) => selection.has(id)).length,
    [selectableIds, selection],
  );
  const allSelected = selectableIds.length > 0 && selectedVisibleCount === selectableIds.length;

  // The flattened, ordered rows the single connector traces. Only rows that
  // are actually mounted appear here: a collapsed project contributes just its
  // own row, so the connector never traces a row nobody can see.
  const orderedRows = useMemo<RailRow[]>(() => {
    const rows: RailRow[] = [];
    if (layout === "sessions") return rows;
    const walkSession = (session: SessionNode<TreeRowFacts>, depth: number) => {
      rows.push({ key: `s:${session.id}`, depth });
      if (!openChildren.has(session.id)) return;
      for (const child of session.children) walkSession(child, depth + 1);
    };
    for (const project of tree) {
      rows.push({ key: `p:${project.id}`, depth: RAIL_DEPTH.project });
      if (collapsedProjects.has(project.id)) continue;
      for (const group of project.children) {
        rows.push({ key: `g:${group.id}`, depth: RAIL_DEPTH.group });
        for (const session of group.children) walkSession(session, RAIL_DEPTH.session);
      }
    }
    return rows;
  }, [collapsedProjects, openChildren, tree, layout]);

  const { containerRef, setRowElement, path: railPath } = useTreeRail(orderedRows, [selection, previewId]);

  return (
    <div className="on-contribute-tree">
      <div className="on-contribute-filters">
        <Input
          value={filters.search}
          onChange={(e) => onFiltersChange({ ...filters, search: e.target.value })}
          placeholder="search sessions"
          aria-label="search sessions"
        />
        <Select
          value={filters.harness ?? ""}
          onChange={(e) =>
            onFiltersChange({ ...filters, harness: e.target.value === "" ? null : e.target.value })
          }
          aria-label="filter by harness"
        >
          <option value="">every harness</option>
          {[...harnessCounts.entries()].map(([harness, count]) => (
            <option key={harness} value={harness}>
              {harness} ({count})
            </option>
          ))}
        </Select>
      </div>
      <div className="on-contribute-selection">
        <div className="min-w-0 font-mono text-sm tabular-nums text-ink-2" data-testid="contribute-selection-tally">
          {selectionSummary ?? <>{selectedVisibleCount} selected · {selectableIds.length} {countNoun}
          {selectableIds.length !== 1 ? "s" : ""}</>}
        </div>
        <Button
          size="sm"
          variant="ghost"
          pressed={allSelected}
          disabled={selectableIds.length === 0}
          onClick={() => onToggleAll(selectableIds, !allSelected)}
        >
          {allSelected ? "deselect all" : "select all"}
        </Button>
      </div>
      <div className="on-contribute-rows">
        {tree.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-ink-3">{emptyLabel}</p>
        ) : layout === "sessions" ? (
          <div className="on-review-session-list">
            {tree.flatMap((project) => project.children.flatMap((group) => group.children)).map((session) => (
              <SessionRow key={session.id} session={session} depth={0}
                selection={selection} onToggleNode={onToggleNode} onPreview={onPreview} previewId={previewId}
                setRowElement={setRowElement} openChildren={openChildren}
                onToggleChildren={(id) => setOpenChildren((before) => {
                  const next = new Set(before);
                  if (next.has(id)) next.delete(id); else next.add(id);
                  return next;
                })}
                helperGroupSlot={helperGroupSlot} sessionActions={sessionActions} sessionFacts={sessionFacts}
              />
            ))}
          </div>
        ) : (
          <TreeRail containerRef={containerRef} path={railPath}>
              {tree.map((project) => (
                <ProjectRow
                  key={project.id}
                  project={project}
                  open={!collapsedProjects.has(project.id)}
                  onToggleOpen={() =>
                    setCollapsedProjects((prev) => {
                      const next = new Set(prev);
                      if (next.has(project.id)) next.delete(project.id);
                      else next.add(project.id);
                      return next;
                    })
                  }
                  selection={selection}
                  onToggleNode={onToggleNode}
                  onPreview={onPreview}
                  previewId={previewId}
                  setRowElement={setRowElement}
                  openChildren={openChildren}
                  onToggleChildren={(sessionId) =>
                    setOpenChildren((prev) => {
                      const next = new Set(prev);
                      if (next.has(sessionId)) next.delete(sessionId);
                      else next.add(sessionId);
                      return next;
                    })
                  }
                  helperGroupSlot={helperGroupSlot}
                />
              ))}
          </TreeRail>
        )}
      </div>
    </div>
  );
}
