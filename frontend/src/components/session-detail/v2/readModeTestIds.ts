import { closed } from "@peasant-labs/fairtrade/closed-value";

/** The test ids the transcript view renders for its read: which reader mounted, and the line an
 *  unserved deep link shows. A plain module, so the journeys and shoots import the same members. */
export const SessionDetailV2TestId = {
  ModePositioned: closed("transcript-mode-positioned", "a transcript read through the positioned reader"),
  ModeContent: closed("transcript-mode-content", "a transcript read as the whole document"),
  SnappedTurn: closed("transcript-snapped-turn", "the line a deep link or a saved position to an unserved turn shows"),
  DownloadStopped: closed("transcript-download-stopped", "the line a download the page's revision change stopped shows"),
  GraphLoading: closed("transcript-graph-loading", "the positioned page's graph while it reads the whole stored copy"),
} as const;
