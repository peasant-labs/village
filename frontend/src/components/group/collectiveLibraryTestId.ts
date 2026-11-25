import { closed } from "@peasant-labs/fairtrade/closed-value";

/**
 * The test ids of the collective library's transcript-group disclosure.
 * `Disclosure` is the base fairtrade's disclosure derives its wrapper and
 * toggle ids from; `DisclosureToggle` is the toggle it derives.
 *
 * This module imports nothing from the app, so the visual and journey scripts
 * import it directly under Node.
 */
export const CollectiveLibraryTestId = {
  Disclosure: closed("collective-library-disclosure", "the disclosure that folds the library's transcript groups"),
  DisclosureToggle: closed("collective-library-disclosure-toggle", "the control that opens and closes the transcript groups"),
} as const;
