"use client";

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";

/** Length of the single start/end terminal cap of the connector, in the same
 *  measured pixel space as the anchors it joins. */
const RAIL_CAP = 6;

/** One mounted row the connector passes through, in reading order. `key`
 *  names the element registered with `setRowElement`; `depth` is the column
 *  the row's checkbox sits in. */
export interface RailRow {
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

/**
 * Measures the checkbox of every row in `rows` (only rows that are actually
 * mounted belong there) and returns the one connector path through them.
 * `remeasure` lists whatever else can move a row without changing the list,
 * such as the selection or the previewed row.
 */
export function useTreeRail(rows: readonly RailRow[], remeasure: readonly unknown[] = []) {
  const containerRef = useRef<HTMLDivElement>(null);
  const rowRefs = useRef(new Map<string, HTMLElement>());
  const [path, setPath] = useState("");

  const setRowElement = useCallback((key: string, element: HTMLElement | null) => {
    if (element) rowRefs.current.set(key, element);
    else rowRefs.current.delete(key);
  }, []);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let frame = 0;
    const measure = () => {
      const containerRect = container.getBoundingClientRect();
      const anchors: RailAnchor[] = [];
      for (const row of rows) {
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
        setPath("");
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
      setPath(buildRailPath(anchors));
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
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `remeasure` is the caller's dependency list
  }, [rows, ...remeasure]);

  return { containerRef, setRowElement, path };
}

/** The connector layer and the rows it runs behind. */
export function TreeRail({
  containerRef,
  path,
  children,
}: {
  containerRef: React.RefObject<HTMLDivElement | null>;
  path: string;
  children: ReactNode;
}) {
  return (
    <div ref={containerRef} className="relative">
      <svg className="contribute-rail" aria-hidden="true">
        {path && (
          <path className="contribute-rail__path" d={path} shapeRendering="crispEdges" strokeLinecap="square" />
        )}
      </svg>
      <div className="contribute-rail-rows">{children}</div>
    </div>
  );
}
