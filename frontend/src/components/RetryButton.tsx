"use client";

import type { ReactNode } from "react";

/**
 * The one retry control: a label, a busy state, and a handler.
 *
 * It exists because the busy state carries an invariant that is easy to get
 * wrong and was written out three times before this. A real `disabled` on the
 * control somebody just activated moves focus to the document body, and
 * re-enabling it does not give focus back, so a keyboard user would have to tab
 * from the top of the page after every attempt. The state is therefore carried
 * by `aria-disabled`, which announces it without touching focus, and the press
 * is refused in the handler instead.
 *
 * A retry that fails again renders the same words, so a control that did not
 * report being busy could not be told from one that did nothing when pressed.
 * That is why the busy state exists at all, and why the label is the caller's:
 * a surface that retries one page of many should name the page it will reload.
 */
export interface RetryButtonProps {
  /** Names the exact request being re-issued. */
  label: ReactNode;
  /** The request is already in flight; the press is refused. */
  busy?: boolean;
  onRetry: () => void;
  /** Marks the control for tests that assert WHICH surface offers it. */
  testId?: string;
}

export default function RetryButton({
  label,
  busy = false,
  onRetry,
  testId,
}: RetryButtonProps) {
  return (
    <button
      type="button"
      className="btn btn-secondary btn-sm shrink-0"
      data-testid={testId}
      aria-disabled={busy || undefined}
      onClick={() => {
        if (busy) return;
        onRetry();
      }}
    >
      {label}
    </button>
  );
}
