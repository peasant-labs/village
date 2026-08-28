"use client";

import { SearchX } from "lucide-react";
import type { ReactNode } from "react";

/**
 * The one full-surface "this request failed" panel: a heading, the actionable
 * message, and a retry control that re-issues the SAME request.
 *
 * It exists because a failed list request must never be rendered as an empty
 * one. "Nothing here" and "we could not find out" are different answers, and
 * only the second one is worth a retry button.
 *
 * The markup is the discovery list's original failure surface, lifted so the
 * home page shows the same panel instead of growing a second dialect of it.
 * This covers the FULL-SURFACE arm only. The inline notice shape, the one a
 * surface shows above rows it is keeping, is still written out at each of its
 * call sites; folding those together is a separate change.
 *
 * Fairtrade's `DataState` carries an error slot, but its panel is worded for a
 * program running on the reader's own computer ("lost connection to the local
 * program … nothing has left your machine") and only its body is overridable.
 * Village is a hosted commons, so that title would name a component that does
 * not exist in this product. The panel below therefore stays local, built from
 * design-system tokens, until the shared component can carry its own title.
 */
export interface RequestFailureStateProps {
  /** The short headline: what could not be loaded. */
  title: ReactNode;
  /** The actionable body: what failed, what it means, and what retrying does. */
  message: ReactNode;
  /** Re-issues the failed request. */
  onRetry: () => void;
  /** The retry control's label; names the exact request being re-issued. */
  retryLabel: ReactNode;
}

export default function RequestFailureState({
  title,
  message,
  onRetry,
  retryLabel,
}: RequestFailureStateProps) {
  return (
    <div className="flex flex-col gap-6 animate-fade-up">
      <div
        role="alert"
        className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center"
      >
        <SearchX size={28} className="text-ink-4" />
        <p className="text-sm font-medium text-ink">{title}</p>
        <p className="text-[13px] text-ink-3 max-w-sm">{message}</p>
        <button
          type="button"
          className="btn btn-secondary btn-sm shrink-0"
          onClick={onRetry}
        >
          {retryLabel}
        </button>
      </div>
    </div>
  );
}
