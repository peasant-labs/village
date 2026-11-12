import { closed } from "@peasant-labs/fairtrade/closed-value";

/**
 * The test ids `HomePage.tsx` renders. A test or a visual script finds the
 * element through a member's `wire`; the label says what the element is.
 *
 * This module imports nothing from the app, so the visual scripts under
 * `scripts/visual/` import it directly under Node.
 */
const HomeTestIdValues = {
  Transcripts: closed("home-transcripts", "the region that holds the transcript table"),
  TranscriptRow: closed("home-transcript-row", "the title cell of one transcript row"),
} as const;

export type HomeTestId = (typeof HomeTestIdValues)[keyof typeof HomeTestIdValues];
export const HomeTestId = HomeTestIdValues;
