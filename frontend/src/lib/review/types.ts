/**
 * The wire contract for the collective review surface. This module mirrors the
 * Go shapes in `backend/internal/handler/groups.go`
 * (`ListPendingGroupShares` and `batchReviewRequest`/`batchReviewResponse`)
 * field-for-field — keep both in sync when either side changes; these routes
 * do not carry generated types, so this is the single hand-maintained copy the
 * frontend trusts.
 */

/** One row of `GET /groups/{id}/pending` — a submission awaiting a decision. */
export interface PendingShare {
  transcript_id: string;
  title: string | null;
  model_provider: string;
  /** The publisher and the id the recording harness used, which is how a
   *  submission that another session started is matched to the submission
   *  that started it. A queue can hold rows from several publishers, and
   *  `local_id` is unique per owner rather than globally, so both are
   *  needed: matching on the session id alone would let one publisher's
   *  submission capture another publisher's. */
  owner_id: string;
  local_id: string;
  /** The harness id of the session that started this one, or null. */
  parent_session_id: string | null;
  /** Where the submission was recorded. The review page groups the queue by
   *  project and branch exactly as the contribute page groups a contributor's
   *  own sessions. `project_name` is the publisher's own label and may be
   *  absent, in which case a reader falls back to `project_hash`. */
  project_hash: string;
  project_name: string | null;
  branch: string | null;
  owner_username: string;
  owner_is_discoverable: boolean;
  shared_at: string;
}

/** The one decision a batch applies to every id it carries. Approving some
 *  rows and rejecting others is two actions, never one request. */
export type ReviewDecision = "approved" | "rejected";

/** `PATCH /groups/{id}/shares` request body. */
export interface BatchReviewRequest {
  transcript_ids: string[];
  status: ReviewDecision;
}

/**
 * `PATCH /groups/{id}/shares` response body.
 *
 * `decided` names the submissions this action moved out of the queue.
 * `already_decided` names every requested id that did NOT move — another
 * reviewer decided it first, or it was never a live submission to this
 * collective. Both read the same way to the page: the row is stale, refetch.
 */
export interface BatchReviewResponse {
  decided: string[];
  already_decided: string[];
}
