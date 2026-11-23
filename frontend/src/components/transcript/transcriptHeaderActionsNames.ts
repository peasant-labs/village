import { closed } from "@peasant-labs/fairtrade/closed-value";

/** The test ids the transcript header's actions render. A plain module, so the journeys and
 *  shoots import the same members as the component. */
export const TranscriptHeaderActionsTestId = {
  Root: closed("transcript-header-actions", "the transcript header's copy-link and more actions"),
} as const;

/** The names a reader meets: the menu trigger and every reader's download rows. */
export const TranscriptHeaderActionsLabel = {
  More: closed("more", "the menu of transcript actions"),
  DownloadMarkdown: closed("download markdown", "save the transcript as markdown"),
  DownloadJSON: closed("download json", "save the stored transcript as json"),
  DownloadJSONL: closed("download jsonl", "save the stored transcript's turns as json lines"),
} as const;
