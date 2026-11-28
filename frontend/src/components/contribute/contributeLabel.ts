import { closed } from "@peasant-labs/fairtrade/closed-value";

/**
 * The names a contributor meets on the contribute page, its project tree and
 * its receipt. The components render a member's `wire` and the tests query by
 * it, so a reworded control moves both together.
 */
export const ContributeLabel = {
  Search: closed("search transcripts", "the search over the sessions a member can contribute"),
  DeselectAll: closed("deselect all", "the control that clears a project's selection"),
  AlreadyContributed: closed("already contributed", "the mark on a session the collective already holds"),
  Stop: closed("stop", "the control that stops a running contribution"),
  RetryTranscripts: closed("retry your transcripts", "the retry after the project list failed to load"),
  NothingToContribute: closed("no transcripts to contribute", "the notice when a member has no session to offer"),
  Receipt: closed("contribution receipt", "the list of what a finished run settled"),
  RunFinished: closed("contribution finished", "the outcome of a run that reached every session"),
  RunStopped: closed("contribution stopped", "the outcome of a run that ended before every session"),
} as const;

/** `count` and `noun`, with the noun made plural unless the count is one. */
export function countOf(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? "" : "s"}`;
}

/**
 * The submit's name when it would contribute `count` transcripts. With nothing
 * selected it names the step still missing rather than a count of zero.
 */
export function contributeLabel(count: number): string {
  return count === 0 ? "select transcripts to contribute" : `contribute ${countOf(count, "transcript")}`;
}

/** The name of the control that drops the selected sessions the search no longer lists. */
export function deselectUnlistedLabel(count: number): string {
  return `deselect ${count === 1 ? "it" : "them"}`;
}
