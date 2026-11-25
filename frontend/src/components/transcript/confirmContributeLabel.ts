import { closed } from "@peasant-labs/fairtrade/closed-value";

/**
 * The names a contributor meets in the dialog that asks consent before a
 * private session is made visible. The dialog renders a member's `wire` and
 * the tests query by it.
 */
export const ConfirmContributeLabel = {
  Consent: closed("i understand and consent to sharing these transcripts", "the box a contributor ticks to consent"),
  Confirm: closed("contribute & make visible", "the control that contributes and makes the sessions visible"),
  Cancel: closed("cancel", "the control that closes the dialog without contributing"),
} as const;
