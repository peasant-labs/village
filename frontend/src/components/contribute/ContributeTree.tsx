"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ChevronRight } from "lucide-react";
import type { ContributableTranscript } from "@/lib/contribute/types";
import type { ContributeNode, ProjectNode, SessionNode } from "@/lib/contribute/tree";
import { leafIds, nodeState, type NodeState, type Selection } from "@/lib/contribute/selection";
import type { ContributeFilters } from "@/lib/contribute/filter";
import { Button, Input, Select, Tag } from "@/lib/ft-ui";
import SessionGroupDisclosure from "@/components/transcript/SessionGroupDisclosure";
import { childSessionGroupLabel } from "@/lib/childSessions";

interface ContributeTreeProps {
  tree: ProjectNode[];
  selection: Selection;
  onToggleNode: (node: ContributeNode) => void;
  /** Select or clear EVERY selectable leaf currently visible in the tree. */
  onToggleAll: (ids: string[], selectAll: boolean) => void;
  onPreview: (transcriptId: string) => void;
  previewId: string | null;
  filters: ContributeFilters;
  onFiltersChange: (next: ContributeFilters) => void;
  harnessCounts: Map<string, number>;
}

/** Depth column each row type occupies in the connector: project 0, branch (or
 *  the synthetic orphans grouping) 1, session 2, a session's own child 3, and
 *  so on for deeper descendants. */
const RAIL_DEPTH = { project: 0, group: 1, session: 2 } as const;

/** Length of the single start/end terminal cap of the connector, in the same
 *  measured pixel space as the anchors it joins. */
const RAIL_CAP = 6;

interface RailRow {
  key: string;
  depth: number;
}

interface RailAnchor {
  x: number;
  y: number;
  depth: number;
}

/**
 * Composes ONE continuous path through the ordered checkbox anchors. Between
 * adjacent rows the lone horizontal step sits at the DEEPER row's y (always an
 * indentation gutter, never across a row's label), and the vertical changes
 * column exactly once, so at any y there is exactly one vertical segment. The
 * path opens and closes with a single cap collinear with the first/last
 * segment.
 */
function buildRailPath(anchors: RailAnchor[]): string {
  if (anchors.length === 0) return "";
  const first = anchors[0];
  const parts = [`M ${first.x} ${first.y - RAIL_CAP}`, `L ${first.x} ${first.y}`];
  for (let i = 1; i < anchors.length; i++) {
    const prev = anchors[i - 1];
    const cur = anchors[i];
    if (cur.x === prev.x) {
      parts.push(`L ${cur.x} ${cur.y}`);
    } else if (cur.depth > prev.depth) {
      // Descending: fall at the parent column to the child row, then step in.
      parts.push(`L ${prev.x} ${cur.y}`, `L ${cur.x} ${cur.y}`);
    } else {
      // Ascending: step out at the deeper (previous) row, then fall to the row.
      parts.push(`L ${cur.x} ${prev.y}`, `L ${cur.x} ${cur.y}`);
    }
  }
  const last = anchors[anchors.length - 1];
  parts.push(`L ${last.x} ${last.y + RAIL_CAP}`);
  return parts.join(" ");
}

function formatDate(iso: string): string {
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return "unknown date";
  return parsed.toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

/**
 * The secondary metadata line under a session's title: the harness that
 * produced it, the date it was published, and its stored visibility. A single
 * mono line keeps every row the same height, so the column reads as one list
 * instead of a ragged stack of pills.
 */
function sessionMeta(row: ContributableTranscript): string {
  return [row.model_provider || "unknown harness", formatDate(row.published_at), row.visibility].join(" · ");
}

/**
 * A parent row's checkbox carries three states: none, some, or all of its
 * selectable descendants. "some" is drawn as the filled square with a dash
 * (the native indeterminate face styled in `globals.css`), so partial
 * selection reads as a distinct SHAPE and never rides on colour alone.
 */
function TriStateCheckbox({
  state,
  disabled = false,
  onChange,
  label,
  inputRef,
}: {
  state: NodeState;
  disabled?: boolean;
  onChange: () => void;
  label: string;
  inputRef?: (element: HTMLInputElement | null) => void;
}) {
  const ref = useRef<HTMLInputElement | null>(null);
  const mixed = state === "some";
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = mixed;
  }, [mixed]);
  return (
    <label className="check contribute-check">
      <input
        ref={(element) => {
          ref.current = element;
          inputRef?.(element);
        }}
        type="checkbox"
        className="check-box"
        data-state={state}
        data-indeterminate={mixed || undefined}
        aria-checked={mixed ? "mixed" : state === "all"}
        checked={state === "all"}
        disabled={disabled}
        onChange={onChange}
        aria-label={label}
      />
      {mixed && <span className="contribute-check__mixed" aria-hidden="true" />}
    </label>
  );
}

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
}: {
  session: SessionNode;
  depth: number;
  selection: Selection;
  onToggleNode: (node: ContributeNode) => void;
  onPreview: (transcriptId: string) => void;
  previewId: string | null;
  setRowElement: (key: string, element: HTMLElement | null) => void;
  openChildren: ReadonlySet<string>;
  onToggleChildren: (sessionId: string) => void;
}) {
  const childrenOpen = openChildren.has(session.id);
  const state = nodeState(selection, session);
  const row = session.row;
  const isActive = previewId === session.id;
  const rowRef = useCallback(
    (element: HTMLDivElement | null) => setRowElement(`s:${session.id}`, element),
    [session.id, setRowElement],
  );

  return (
    <div>
      <div
        ref={rowRef}
        className={`flex items-center gap-3 px-4 py-3 transition-colors ${
          isActive ? "bg-surface-hover" : "hover:bg-surface-hover"
        }`}
        data-testid={`contribute-session-row-${session.id}`}
      >
        <TriStateCheckbox
          state={state}
          disabled={row.already_shared}
          onChange={() => onToggleNode(session)}
          label={`select session ${session.label}`}
        />
        {/* Title and its row-level marks share ONE line; the metadata line owns
            the full width below them. Keeping the marks out of the metadata
            line is what stops a narrow column from truncating (or wrapping)
            the very facts the row exists to state. */}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => onPreview(session.id)}
              className="min-w-0 flex-1 text-sm text-ink truncate text-left cursor-pointer focus-mono"
            >
              {session.label}
            </button>
            {row.already_shared && <Tag className="shrink-0">already contributed</Tag>}
          </div>
          <div className="font-mono text-xs text-ink-3 tabular-nums [overflow-wrap:anywhere]">{sessionMeta(row)}</div>
        </div>
      </div>
      {/* The sessions this one started, behind the SAME control the home page
          and every other transcript list uses. The marker attribute is the same
          one those lists carry, so what a control belongs to is observable here
          in the same way rather than inferred from the order elements happen to
          appear in. */}
      {session.children.length > 0 && (
        <div className="pl-4" data-parent-transcript-id={session.id}>
          <SessionGroupDisclosure
            label={childSessionGroupLabel(session.children.length)}
            collapsedLabel={childSessionGroupLabel(session.children.length)}
            expanded={childrenOpen}
            onToggle={() => onToggleChildren(session.id)}
            rowsID={`contribute-child-sessions-${session.id}`}
            testID="child-session-disclosure"
            bare
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
                />
              ))}
            </div>
          </SessionGroupDisclosure>
        </div>
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
}: {
  node: ContributeNode & { children: SessionNode[] };
  selection: Selection;
  onToggleNode: (node: ContributeNode) => void;
  onPreview: (transcriptId: string) => void;
  previewId: string | null;
  setRowElement: (key: string, element: HTMLElement | null) => void;
  openChildren: ReadonlySet<string>;
  onToggleChildren: (sessionId: string) => void;
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
}: {
  project: ProjectNode;
  open: boolean;
  onToggleOpen: () => void;
  selection: Selection;
  onToggleNode: (node: ContributeNode) => void;
  onPreview: (transcriptId: string) => void;
  previewId: string | null;
  setRowElement: (key: string, element: HTMLElement | null) => void;
  openChildren: ReadonlySet<string>;
  onToggleChildren: (sessionId: string) => void;
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
}: ContributeTreeProps) {
  const [collapsedProjects, setCollapsedProjects] = useState<ReadonlySet<string>>(new Set());
  const [openChildren, setOpenChildren] = useState<ReadonlySet<string>>(new Set());
  const containerRef = useRef<HTMLDivElement>(null);
  const rowRefs = useRef(new Map<string, HTMLElement>());
  const [railPath, setRailPath] = useState("");

  const setRowElement = useCallback((key: string, element: HTMLElement | null) => {
    if (element) rowRefs.current.set(key, element);
    else rowRefs.current.delete(key);
  }, []);

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
    const walkSession = (session: SessionNode, depth: number) => {
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
  }, [collapsedProjects, openChildren, tree]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let frame = 0;
    const measure = () => {
      const containerRect = container.getBoundingClientRect();
      const anchors: RailAnchor[] = [];
      for (const row of orderedRows) {
        const element = rowRefs.current.get(row.key);
        const input = element?.querySelector('input[type="checkbox"]') as HTMLElement | null;
        if (!input) return; // not laid out yet - a later observer tick retries
        const rect = input.getBoundingClientRect();
        anchors.push({
          x: rect.left + rect.width / 2 - containerRect.left,
          y: rect.top + rect.height / 2 - containerRect.top,
          depth: row.depth,
        });
      }
      if (anchors.length === 0) {
        setRailPath("");
        return;
      }
      // Snap each depth to ONE column (its median x) so every vertical at a
      // given depth is exactly aligned - provably a single line per column.
      const byDepth = new Map<number, number[]>();
      for (const anchor of anchors) {
        const list = byDepth.get(anchor.depth) ?? [];
        list.push(anchor.x);
        byDepth.set(anchor.depth, list);
      }
      const column = new Map<number, number>();
      for (const [depth, xs] of byDepth) {
        const sorted = xs.slice().sort((a, b) => a - b);
        column.set(depth, sorted[Math.floor(sorted.length / 2)]);
      }
      for (const anchor of anchors) anchor.x = column.get(anchor.depth)!;
      setRailPath(buildRailPath(anchors));
    };
    const schedule = () => {
      if (typeof cancelAnimationFrame === "function") cancelAnimationFrame(frame);
      frame = typeof requestAnimationFrame === "function" ? requestAnimationFrame(measure) : (measure(), 0);
    };
    schedule();
    const observer = typeof ResizeObserver !== "undefined" ? new ResizeObserver(schedule) : null;
    if (observer) {
      observer.observe(container);
      for (const element of rowRefs.current.values()) observer.observe(element);
    }
    window.addEventListener("resize", schedule);
    return () => {
      if (typeof cancelAnimationFrame === "function") cancelAnimationFrame(frame);
      observer?.disconnect();
      window.removeEventListener("resize", schedule);
    };
  }, [orderedRows, selection, previewId]);

  return (
    <div className="flex flex-col h-full min-h-0">
      <div className="flex items-center gap-2 p-4 border-b border-rule shrink-0">
        <Input
          value={filters.search}
          onChange={(e) => onFiltersChange({ ...filters, search: e.target.value })}
          placeholder="search sessions"
          aria-label="search sessions"
          className="flex-1"
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
      <div className="flex flex-wrap items-center justify-between gap-3 px-4 py-3 border-b border-rule shrink-0">
        <div className="min-w-0 font-mono text-sm tabular-nums text-ink-2" data-testid="contribute-selection-tally">
          {selectedVisibleCount} selected · {selectableIds.length} session
          {selectableIds.length !== 1 ? "s" : ""}
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
      <div className="flex-1 min-h-0 overflow-y-auto">
        {tree.length === 0 ? (
          <p className="px-4 py-8 text-center text-sm text-ink-3">no contributable sessions match this filter.</p>
        ) : (
          <div ref={containerRef} className="relative">
            <svg className="contribute-rail" aria-hidden="true">
              {railPath && (
                <path
                  className="contribute-rail__path"
                  d={railPath}
                  shapeRendering="crispEdges"
                  strokeLinecap="square"
                />
              )}
            </svg>
            <div className="contribute-rail-rows">
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
                />
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
