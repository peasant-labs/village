/**
 * The wire contract for `GET /groups/{id}/contributable` and
 * `POST /groups/{id}/shares`. The shapes come from the published contract
 * package, which village serves and enforces from the same bytes; the names
 * below are the historical local names, kept as aliases for one release so
 * consumers do not change.
 */
import type {
  VillageBatchShareRequest,
  VillageBatchShareResponse,
  VillageContributableResponse,
  VillageContributableTranscript,
  VillageContributionStatus,
} from "@peasant-labs/schema";
import type { AsPlainString } from "@/lib/types";

/**
 * One row of the contributable list: a transcript the caller owns that could
 * be shared into this collective. `already_shared` distinguishes a live
 * submission (pending or approved) from one still eligible to select.
 *
 * There is deliberately no `owner_id` on this row: the endpoint answers with
 * the caller's own transcripts only, and the tree relies on that. It folds a
 * started session under its starter by session id alone, which is safe for
 * one owner and WRONG for several, because a session id is unique per owner
 * rather than globally. If the endpoint is ever widened to answer with more
 * than one person's rows, add `owner_id` to the contract and read it in
 * `./tree.ts` (see `SINGLE_OWNER_ENDPOINT` there) in the SAME change.
 *
 * @deprecated Import VillageContributableTranscript from "@peasant-labs/schema". Alias kept for one release.
 */
export type ContributableTranscript = AsPlainString<VillageContributableTranscript, "project_hash">;

/** @deprecated Import VillageContributableResponse from "@peasant-labs/schema". Alias kept for one release. */
export type ContributableResponse = Omit<VillageContributableResponse, "transcripts"> & { transcripts: ContributableTranscript[] };

/**
 * `POST /groups/{id}/shares` request body. Every id must belong to the same
 * `project_hash`: one POST per project.
 *
 * @deprecated Import VillageBatchShareRequest from "@peasant-labs/schema". Alias kept for one release.
 */
export type BatchShareRequest = AsPlainString<VillageBatchShareRequest, "project_hash">;

/**
 * Per-transcript disposition inside a successful batch response.
 *
 * @deprecated Import VillageContributionStatus from "@peasant-labs/schema". Alias kept for one release.
 */
export type BatchShareStatus = VillageContributionStatus;

/** @deprecated Import VillageBatchShareResponse from "@peasant-labs/schema". Alias kept for one release. */
export type BatchShareResponse = VillageBatchShareResponse;
