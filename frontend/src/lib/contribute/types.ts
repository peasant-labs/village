import type { NameSource } from "@/lib/types";
import type { SessionOrigin } from "@/lib/sessionOrigin";

/**
 * The frozen wire contract for `GET /groups/{id}/contributable` and
 * `POST /groups/{id}/shares` (village#65). This module mirrors the Go wire
 * structs in `backend/internal/handler/group_shares.go` field-for-field —
 * keep both in sync when either side changes; the endpoint pair does not
 * carry its own generated types, so this is the single hand-maintained copy
 * the frontend trusts.
 */

/** One row of `ListContributable` — a transcript the caller owns that COULD
 *  be shared into this collective. `already_shared` distinguishes a live
 *  submission (pending or approved) from one still eligible to select. */
export interface ContributableTranscript {
  id: string;
  local_id: string;
  title: string | null;
  visibility: "public" | "private" | "shared";
  project_hash: string;
  project_display_name: string;
  project_name_source: NameSource;
  git_branch: string | null;
  parent_session_id: string | null;
  session_origin: SessionOrigin;
  model_provider: string;
  published_at: string;
  already_shared: boolean;
}

/** `GET /groups/{id}/contributable` response body. */
export interface ContributableResponse {
  group_id: string;
  transcripts: ContributableTranscript[];
}

/** `POST /groups/{id}/shares` request body — every id must belong to the same
 *  `project_hash`: one POST per project. */
export interface BatchShareRequest {
  project_hash: string;
  transcript_ids: string[];
  visibility_confirmed: boolean;
}

/** Per-transcript disposition inside a successful batch response. */
export type BatchShareStatus = "approved" | "pending";

/** `POST /groups/{id}/shares` response body. */
export interface BatchShareResponse {
  project_hash: string;
  shared: { transcript_id: string; status: BatchShareStatus }[];
  already_shared: string[];
}
