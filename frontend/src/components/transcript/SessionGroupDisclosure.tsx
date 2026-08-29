"use client";

import type { ReactNode } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

interface SessionGroupDisclosureProps {
  /** The group's text while it is OPEN. */
  label: string;
  /**
   * The group's text while it is CLOSED.
   *
   * Written out by the caller rather than decorated here, because the two
   * groups no longer agree on it: the agent group announces itself with a
   * leading `+`, and the chip of sessions one row started does not. A shell
   * that added the mark would be deciding one group's wording from inside the
   * other's, and the two would have to be kept apart by a flag.
   */
  collapsedLabel: string;
  expanded: boolean;
  onToggle: () => void;
  /**
   * The `id` of the element this control reveals. The caller owns the revealed
   * element, so it owns the id: one group's control must never name another
   * group's rows.
   */
  rowsID: string;
  /** Base for the wrapper and control test ids: `<base>` and `<base>-toggle`. */
  testID: string;
  /** Drop the outer panel border when the group sits inside a bordered panel. */
  bare?: boolean;
  /** The revealed element. Rendered only while expanded. */
  children?: ReactNode;
}

/**
 * The collapsed-group control shared by the session groups at the end of a
 * transcript list.
 *
 * Both groups present the same thing to a viewer -- a count of rows held back
 * from the list, and one control that reveals them -- and differ only in where
 * their rows come from. This owns the shape they share, so a change to the
 * control is made once and cannot land on one group and not the other.
 *
 * Collapse state belongs to the caller. A group is an aside, and a viewer who
 * opened it once has not asked for it to be open on every future visit, so no
 * caller persists it.
 */
export default function SessionGroupDisclosure({
  label,
  collapsedLabel,
  expanded,
  onToggle,
  rowsID,
  testID,
  bare = false,
  children,
}: SessionGroupDisclosureProps) {
  // A div, not a section element: the design system styles a bare section as a
  // page band, centred inside its own max-width and gutters. That is right for
  // a top-level page region and wrong for a row at the end of a list, which
  // must take the width of the list it belongs to.
  return (
    <div
      className={bare ? "" : "border border-rule bg-surface"}
      data-testid={testID}
    >
      <button
        type="button"
        aria-expanded={expanded}
        aria-controls={rowsID}
        data-testid={`${testID}-toggle`}
        onClick={onToggle}
        className="w-full flex items-center gap-2 px-5 py-3 min-h-[44px] text-left font-mono text-xs text-ink-3 hover:text-ink hover:bg-surface-hover focus-mono transition-colors cursor-pointer"
      >
        {expanded ? (
          <ChevronDown size={12} strokeWidth={2} aria-hidden="true" />
        ) : (
          <ChevronRight size={12} strokeWidth={2} aria-hidden="true" />
        )}
        <span className="tabular-nums" data-testid={`${testID}-label`}>
          {expanded ? label : collapsedLabel}
        </span>
        <span className="flex-1" />
        <span className="text-ink-4">
          {expanded ? "hide" : "show"}
        </span>
      </button>

      {expanded && children}
    </div>
  );
}
