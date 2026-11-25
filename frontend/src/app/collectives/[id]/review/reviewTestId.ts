import { closed } from "@peasant-labs/fairtrade/closed-value";

/**
 * The test ids the collective review page renders. A test or a visual script
 * finds the element through a member's `wire`; the label says what the element
 * is.
 *
 * This module imports nothing from the app, so the visual scripts under
 * `scripts/visual/` import it directly under Node.
 */
export const ReviewPageTestId = {
  SelectionCount: closed("review-selection-count", "the count of contributions selected for a decision"),
} as const;
