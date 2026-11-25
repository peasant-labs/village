import { closed } from "@peasant-labs/fairtrade/closed-value";

/**
 * The test ids of the chip that folds the sessions another session started
 * under it. `Root` is the base fairtrade's disclosure derives its wrapper,
 * toggle and label ids from; `Rows` is the region each surface renders the
 * started sessions into.
 *
 * This module imports nothing from the app, so the visual scripts under
 * `scripts/visual/` import it directly under Node.
 */
export const ChildSessionDisclosureTestId = {
  Root: closed("child-session-disclosure", "the chip of sessions this session started"),
  Toggle: closed("child-session-disclosure-toggle", "the control that opens and closes the chip"),
  Label: closed("child-session-disclosure-label", "the chip's count of started sessions"),
  Rows: closed("child-session-disclosure-rows", "the started sessions the chip reveals"),
} as const;
