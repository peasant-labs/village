/**
 * The wire contract for the collective review surface. The shapes come from
 * the published contract package, which village serves and enforces from the
 * same bytes; the names below are the historical local names, kept as aliases
 * for one release so consumers do not change.
 */
import type {
  VillageBatchReviewRequest,
  VillageBatchReviewResponse,
  VillagePendingShare,
  VillageReviewDecision,
} from "@peasant-labs/schema";
import type { AsPlainString } from "@/lib/types";

/**
 * One row of `GET /groups/{id}/pending`: a submission awaiting a decision.
 * A queue can hold rows from several publishers, and `local_id` is unique per
 * owner rather than globally, so matching a started session to its starter
 * needs `owner_id` and `local_id` together.
 *
 * @deprecated Import VillagePendingShare from "@peasant-labs/schema". Alias kept for one release.
 */
export type PendingShare = AsPlainString<VillagePendingShare, "project_hash">;

/**
 * The one decision a batch applies to every id it carries. Approving some
 * rows and rejecting others is two actions, never one request.
 *
 * @deprecated Import VillageReviewDecision from "@peasant-labs/schema". Alias kept for one release.
 */
export type ReviewDecision = VillageReviewDecision;

/** @deprecated Import VillageBatchReviewRequest from "@peasant-labs/schema". Alias kept for one release. */
export type BatchReviewRequest = VillageBatchReviewRequest;

/**
 * `PATCH /groups/{id}/shares` response body. `decided` names the submissions
 * this action moved out of the queue; `already_decided` names every requested
 * id that did not move, because another reviewer decided it first or it was
 * never a live submission to this collective. Both read the same way to the
 * page: the row is stale, refetch.
 *
 * @deprecated Import VillageBatchReviewResponse from "@peasant-labs/schema". Alias kept for one release.
 */
export type BatchReviewResponse = VillageBatchReviewResponse;
