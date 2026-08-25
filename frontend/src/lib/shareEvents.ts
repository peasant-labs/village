import {
  assertShareEventActorExhaustive,
  assertShareEventStatusExhaustive,
  type ShareEvent,
  type ShareEventActor,
  type ShareEventStatus,
} from "@/lib/types";

/**
 * The unit each contribution counter measures, in the words the profile
 * renders beneath it.
 *
 * This is the single declaration of the attempts-versus-transcripts asymmetry
 * in the UI. `approved_count` and `pending_count` count distinct transcripts;
 * `rejected_attempt_count` counts refusal events, so one transcript refused
 * three times by one collective contributes three. Printing "3 rejected"
 * beside "2 approved" without these units invites the reader to compare two
 * numbers that do not measure the same thing.
 */
export const CONTRIBUTION_COUNTER_UNITS = {
  approved: "transcripts",
  pending: "transcripts",
  rejectedAttempts: "submission attempts",
} as const;

/**
 * The sentence that states the asymmetry once, above the counters, so the
 * units beneath each number are explained rather than merely present.
 */
export const CONTRIBUTION_COUNTER_EXPLANATION =
  "approved and awaiting review count transcripts. rejected counts submission attempts, " +
  "so one transcript refused three times counts three.";

/** The lowercase chrome label shown above each counter. */
export const CONTRIBUTION_COUNTER_LABELS = {
  approved: "approved",
  pending: "awaiting review",
  rejectedAttempts: "rejected",
} as const;

/**
 * What HAPPENED at one share event, as a verb.
 *
 * `pending` reads as "submitted" because a pending event is the moment a
 * submission was made. Every other value names its own outcome, and the three
 * ways a contribution can end (`rejected`, `retracted`, `revoked`) keep three
 * distinct verbs: refusal by a reviewer, withdrawal by the owner, and removal
 * by the collective are three different things that happened to the person
 * reading this log.
 */
export function shareEventVerb(status: ShareEventStatus): string {
  switch (status) {
    case "pending":
      return "submitted";
    case "approved":
      return "approved";
    case "rejected":
      return "rejected";
    case "retracted":
      return "retracted";
    case "revoked":
      return "revoked";
    default:
      return assertShareEventStatusExhaustive(status);
  }
}

/**
 * WHO acted, as a trailing clause, or the empty string when the event has not
 * been decided and so has no actor.
 *
 * The wire carries an actor CLASS and never a user id (see
 * {@link ShareEventActor}), so there is no name to resolve here and nothing
 * that could be "unknown". An undecided event simply gets no clause.
 */
export function shareEventActorClause(actor: ShareEventActor): string {
  switch (actor) {
    case "":
      return "";
    case "owner":
      return "by owner";
    case "collective":
      return "by collective";
    case "moderator":
      return "by moderator";
    default:
      return assertShareEventActorExhaustive(actor);
  }
}

/**
 * The full label for one event: the verb, plus who acted when that is known.
 *
 * A withdrawal reads as a withdrawal and names its actor ("retracted by
 * owner", "revoked by collective"). It is never phrased as a re-submission,
 * because the owner reading this log has to be able to tell the moment they
 * pulled a contribution back from the moment they offered it again.
 */
export function shareEventLabel(event: Pick<ShareEvent, "status" | "decided_by_actor">): string {
  const verb = shareEventVerb(event.status);
  const clause = shareEventActorClause(event.decided_by_actor);
  return clause === "" ? verb : `${verb} ${clause}`;
}

/**
 * True when the event ends the contribution's life in that collective without
 * a refusal: the owner withdrew it, or the collective removed it. Kept
 * separate from a refusal so a surface that tints or groups outcomes cannot
 * quietly file a withdrawal under "rejected".
 */
export function isWithdrawal(status: ShareEventStatus): boolean {
  return status === "retracted" || status === "revoked";
}
