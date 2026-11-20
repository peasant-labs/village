import { closed } from "@peasant-labs/fairtrade/closed-value";

/** The test ids the transcript view renders for its read: which reader mounted, and the line an
 *  unserved deep link shows. A plain module, so the journeys and shoots import the same members. */
export const SessionDetailV2TestId = {
  ModePositioned: closed("transcript-mode-positioned", "a transcript read through the positioned reader"),
  ModeContent: closed("transcript-mode-content", "a transcript read as the whole document"),
  SnappedTurn: closed("transcript-snapped-turn", "the line a deep link to an unserved turn shows"),
} as const;
