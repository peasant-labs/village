import { buildSessionTree, type ProjectNode, type TreeRowFacts } from "@/lib/contribute/tree";
import { HIDDEN_PROJECT_LABEL, resolveAttribution } from "@/lib/format";
import type { PendingShare } from "./types";

/**
 * One pending submission as the review tree reads it.
 *
 * The wire row is restated in the shared tree's own vocabulary — `id` rather
 * than `transcript_id`, a resolved `project_display_name`, a `git_branch` —
 * so the SAME fold that builds the contribute tree builds this one. Nothing
 * here recomputes grouping: the translation is names, plus the publisher's
 * label and whether this row has already gone stale under the viewer.
 */
export interface ReviewRow extends TreeRowFacts {
  /** The submission's own wire row, for the actions and the preview column. */
  share: PendingShare;
  model_provider: string;
  /** The publisher as the server resolved them for this viewer: the handle,
   *  or "anon". */
  owner_label: string;
  /** Another reviewer decided this row after the page loaded. It stays visible
   *  and marked rather than vanishing, so a reviewer sees WHY their selection
   *  shrank instead of watching rows disappear. */
  stale: boolean;
}

function ownerLabel(share: PendingShare): string {
  return resolveAttribution(share.owner).label;
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
 *
 * A submission whose project the server withheld (its publisher is anonymous
 * to the reviewer) is headed "hidden project". It is filed with the sessions
 * the server linked it to through `parent_transcript_id` and with nothing
 * else: one shared heading for every withheld project would claim they are
 * one project, and so one person's.
 */
export function toReviewRows(shares: PendingShare[], stale: ReadonlySet<string>): ReviewRow[] {
  const byID = new Map(shares.map((share) => [share.transcript_id, share]));
  // The topmost submission of a row's resolved parent chain in this queue.
  const chainRoot = (share: PendingShare): string => {
    const seen = new Set<string>();
    let current = share;
    while (current.parent_transcript_id != null && !seen.has(current.transcript_id)) {
      seen.add(current.transcript_id);
      const parent = byID.get(current.parent_transcript_id);
      if (parent === undefined) break;
      current = parent;
    }
    return current.transcript_id;
  };
  return shares.map((share) => ({
    id: share.transcript_id,
    local_id: share.local_id,
    title: share.title,
    project_hash: share.project_hash ?? `hidden:${chainRoot(share)}`,
    project_display_name: share.project_hash == null ? HIDDEN_PROJECT_LABEL : (share.project_name ?? share.project_hash),
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
 * keeps its ordinary place under the project's orphans grouping. A queue holds
 * several publishers' work and `local_id` is unique only per publisher, so the
 * parent is the one the server resolved within the same publisher
 * (`parent_transcript_id`): matching on the session id alone would let one
 * publisher's submission capture another's.
 */
export function buildReviewTree(rows: ReviewRow[]): ProjectNode<ReviewRow>[] {
  return buildSessionTree(
    rows,
    (row) => ({
      meta: ["by @" + row.owner_label, formatDate(row.share.shared_at), row.model_provider || "unknown harness"].join(" · "),
      mark: row.stale ? "already decided" : null,
      locked: row.stale,
    }),
    (row) => row.share.parent_transcript_id,
  );
}
