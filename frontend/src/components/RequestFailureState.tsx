"use client";

import { SearchX } from "lucide-react";
import type { ReactNode } from "react";
import RetryButton from "@/components/RetryButton";

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
  /**
   * The retry is already in flight. A retry that fails again renders the same
   * words, so without this the control is indistinguishable from one that did
   * nothing when it was pressed.
   * Carried by the shared retry control, which explains how it is rendered.
   */
  retryDisabled?: boolean;
}

export default function RequestFailureState({
  title,
  message,
  onRetry,
  retryLabel,
  retryDisabled = false,
}: RequestFailureStateProps) {
  return (
    <div className="flex flex-col gap-6 animate-fade-up">
      <div
        className="border border-rule bg-surface px-5 py-12 flex flex-col items-center gap-3 text-center"
      >
        <SearchX size={28} className="text-ink-4" aria-hidden="true" />
        {/* The heading and the message are the alert; the control is not. An
            alert is atomic, so a control inside it re-announces the whole
            failure assertively every time its label changes — which is exactly
            what a busy state does, and it would interrupt the polite region
            that already says the request is going out again. */}
        <div role="alert" className="flex flex-col items-center gap-3">
          <p className="text-sm font-medium text-ink">{title}</p>
          <p className="text-[13px] text-ink-3 max-w-sm">{message}</p>
        </div>
        <RetryButton label={retryLabel} busy={retryDisabled} onRetry={onRetry} />
      </div>
    </div>
  );
}
