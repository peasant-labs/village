import { buildSessionTree, type ProjectNode, type TreeRowFacts } from "@/lib/contribute/tree";
import type { PendingShare } from "./types";

/**
 * One pending submission as the review tree reads it.
 *
 * The wire row is restated in the shared tree's own vocabulary — `id` rather
 * than `transcript_id`, a resolved `project_display_name`, a `git_branch` —
 * so the SAME fold that builds the contribute tree builds this one. Nothing
 * here recomputes grouping: the translation is names, plus the two facts the
 * wire cannot know (whether the viewer may see the publisher's handle, and
 * whether this row has already gone stale under the viewer).
 */
export interface ReviewRow extends TreeRowFacts {
  /** The submission's own wire row, for the actions and the preview column. */
  share: PendingShare;
  model_provider: string;
  /** The publisher as this viewer may see them: the real handle, or "anon"
   *  when the publisher is not discoverable. */
  owner_label: string;
  /** Another reviewer decided this row after the page loaded. It stays visible
   *  and marked rather than vanishing, so a reviewer sees WHY their selection
   *  shrank instead of watching rows disappear. */
  stale: boolean;
}

/** A publisher who is not discoverable is never named to a reviewer. */
export const ANONYMOUS_OWNER = "anon";

function ownerLabel(share: PendingShare): string {
  return share.owner_is_discoverable ? share.owner_username : ANONYMOUS_OWNER;
}

function formatDate(iso: string): string {
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return "unknown date";
  return parsed.toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

/**
 * Restates the pending queue as review rows.
 *
 * `stale` names the ids the server reported in `already_decided` on the last
 * action. `project_name` is optional on the wire, so a project with no name
 * falls back to its hash: a reviewer can still tell two projects apart, which
 * is what the grouping is for.
 */
export function toReviewRows(shares: PendingShare[], stale: ReadonlySet<string>): ReviewRow[] {
  return shares.map((share) => ({
    id: share.transcript_id,
    local_id: share.local_id,
    title: share.title,
    project_hash: share.project_hash,
    project_display_name: share.project_name ?? share.project_hash,
    git_branch: share.branch,
    parent_session_id: share.parent_session_id,
    share,
    model_provider: share.model_provider,
    owner_label: ownerLabel(share),
    stale: stale.has(share.transcript_id),
  }));
}

/**
 * The review page's project > branch > session tree.
 *
 * It is the SAME fold the contribute page uses, given the review queue's own
 * rows: a submission that another session started is read under the submission
 * that started it, and one whose starter was never offered to this collective
 * keeps its ordinary place under the project's orphans grouping. The owner is
 * the real publisher here, not a constant, because a queue holds several
 * publishers' work and `local_id` is unique only per publisher — matching on
 * the session id alone would let one publisher's submission capture another's.
 */
export function buildReviewTree(rows: ReviewRow[]): ProjectNode<ReviewRow>[] {
  return buildSessionTree(
    rows,
    (row) => ({
      meta: ["by @" + row.owner_label, formatDate(row.share.shared_at), row.model_provider || "unknown harness"].join(" · "),
      mark: row.stale ? "already decided" : null,
      locked: row.stale,
    }),
    (row) => row.share.owner_id,
  );
}
