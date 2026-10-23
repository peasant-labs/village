import {
  assertShareEventActorExhaustive,
  assertShareEventStatusExhaustive,
  type ShareEvent,
  type ShareEventActor,
  type ShareEventStatus,
} from "@/lib/types";

/**
 * The lowercase chrome label shown above each counter. Each names its own
 * unit, because the four do not count the same thing: TWO count distinct
 * transcripts (approved, awaiting review) and TWO count submission attempts
 * (rejected, withdrawn), so one transcript refused three times counts three.
 * A label that stopped naming its unit would let the numbers read alike.
 */
export const CONTRIBUTION_COUNTER_LABELS = {
  approved: "approved transcripts",
  pending: "transcripts awaiting review",
  rejectedAttempts: "rejected attempts",
  withdrawnAttempts: "withdrawn attempts",
} as const;

/**
 * The chip label shown for one submission PAIR's latest status, in the
 * per-collective submissions panel.
 *
 * `pending`, `approved` and `rejected` render as themselves. `retracted`
 * (owner withdrew) and `revoked` (collective removed) both render as
 * "withdrawn" — the SAME grouping the withdrawn counter applies. This
 * grouping is a chip-level simplification only: the per-submission event
 * history still distinguishes the two by actor (see {@link shareEventLabel}),
 * so nothing here loses that distinction, it merely does not repeat it on the
 * closed row.
 */
export function submissionPairChip(status: ShareEventStatus): string {
  switch (status) {
    case "pending":
      return "pending";
    case "approved":
      return "approved";
    case "rejected":
      return "rejected";
    case "retracted":
    case "revoked":
      return "withdrawn";
    default:
      return assertShareEventStatusExhaustive(status);
  }
}

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
