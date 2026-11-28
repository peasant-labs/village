import { closed } from "@peasant-labs/fairtrade/closed-value";

/**
 * The test ids the contribute page and its receipt render. A test or a visual
 * script finds the element through a member's `wire`; the label says what the
 * element is.
 *
 * This module imports nothing from the app, so the visual scripts under
 * `scripts/visual/` import it directly under Node.
 */
export const ContributePageTestId = {
  MemberPanel: closed("contribute-member-panel", "the contribute workspace a member of the collective sees"),
  NonMemberNotice: closed("contribute-non-member-notice", "the notice a signed-in person who is not a member sees"),
  SelectionTally: closed("contribute-selection-tally", "the line that counts the selection"),
  UnlistedNotice: closed("contribute-unlisted-notice", "the notice that selected sessions no longer match the search"),
  MeanwhileNotice: closed("contribute-meanwhile-notice", "the notice that selected sessions were contributed meanwhile"),
  ProjectsLoading: closed("contribute-projects-loading", "the placeholder while the project list loads"),
  RunProgress: closed("contribute-run-progress", "the progress of a running contribution"),
  RunOutcome: closed("contribute-run-outcome", "the line that says how the last run ended"),
} as const;

/** The test ids of the receipt a contribution run leaves. */
export const ContributionReceiptTestId = {
  Root: closed("contribute-run-receipt", "the receipt of what the run did with every session"),
  Contributed: closed("contribute-receipt-contributed", "the bucket of contributed sessions"),
  Already: closed("contribute-receipt-already", "the bucket of sessions an earlier attempt contributed"),
  Failed: closed("contribute-receipt-failed", "the bucket of sessions that were not contributed"),
  Unattempted: closed("contribute-receipt-unattempted", "the bucket of sessions the run did not reach"),
  Stopped: closed("contribute-receipt-stopped", "why the run stopped early"),
} as const;
