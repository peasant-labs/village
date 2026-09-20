"use client";

import { Button } from "@/lib/ft-ui";

/**
 * The way to the grouped units a browse surface has not read yet.
 *
 * One server page holds at most the list endpoint's own `limit` of grouped
 * top-level units, and the flat list above this control can render owners the
 * grouped page did not reach. Without a continuation those later owners and
 * helper-only context containers would simply have no grouped exit. This is
 * that continuation: one explicit control, the server's own remaining count,
 * and the same originating filters behind it. It renders only while the server
 * says another page exists, and each press asks for exactly the next page.
 */
export default function ScopedGroupedContinuation({
  remaining,
  busy,
  onLoadMore,
}: {
  /** Grouped top-level units the server reports beyond the ones already read. */
  remaining: number;
  busy: boolean;
  onLoadMore: () => void;
}) {
  if (remaining <= 0) return null;
  return (
    <div
      className="flex items-center justify-between gap-3 px-5 py-3"
      data-testid="grouped-helper-continuation"
    >
      <span className="font-mono text-xs text-ink-3 tabular-nums">
        {remaining.toLocaleString()} more grouped {remaining === 1 ? "result" : "results"}
      </span>
      <Button size="sm" variant="ghost" disabled={busy} onClick={onLoadMore}>
        {busy ? "loading" : "load more"}
      </Button>
    </div>
  );
}
