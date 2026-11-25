import { closed } from "@peasant-labs/fairtrade/closed-value";

/**
 * The tri-state a parent row's checkbox renders: none, some, or all of its
 * selectable descendants are selected. The contribute and review trees and
 * the paged contribute tree all report their rows in these terms, and the
 * checkbox draws them.
 */
const CheckStateValues = {
  None: closed("none", "nothing under this row is selected"),
  Some: closed("some", "some of the rows under this row are selected"),
  All: closed("all", "every selectable row under this row is selected"),
} as const;
export type CheckState = (typeof CheckStateValues)[keyof typeof CheckStateValues];
export const CheckState = CheckStateValues;
