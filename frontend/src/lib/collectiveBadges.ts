import type { ContributedCollective, VisibleGroup } from "./types";

/**
 * What one collectives-list row says about the caller's own standing in that
 * collective. The two axes are independent: a person can be a member who has
 * never contributed, a contributor who never joined, both, or neither.
 */
export interface CollectiveStanding {
  /** The caller's role, or null when they do not belong to this collective. */
  memberRole: string | null;
  /** Whether the caller has a contribution this collective currently holds or is still reviewing. */
  hasContributed: boolean;
}

/**
 * The collective ids the caller has a LIVE contribution to.
 *
 * Approved and pending both count, and nothing else does. An approved
 * contribution is one the collective holds; a pending one is an offer it has
 * not answered yet, and a person who is waiting on an answer needs the row to
 * say so. Refusals and withdrawals are past events, not a standing, so a
 * collective the caller only ever had refused reads the same as one they never
 * approached.
 *
 * The counters come from `GET /users/me/collectives/contributions`, which is
 * the caller's own by route, so this can only ever describe the caller.
 */
export function contributedCollectiveIds(
  contributions: readonly ContributedCollective[] | undefined,
): ReadonlySet<string> {
  const ids = new Set<string>();
  for (const contribution of contributions ?? []) {
    if (contribution.approved_count > 0 || contribution.pending_count > 0) {
      ids.add(contribution.id);
    }
  }
  return ids;
}

/**
 * The caller's standing in one visible collective.
 *
 * A row admitted by the public or open rule alone carries a null role, and that
 * null is the answer, not a missing value: it means the caller is not a member.
 * An empty-string role is treated the same way, so a server or fixture that
 * sends `""` cannot produce a membership badge with no role in it.
 */
export function collectiveStanding(
  group: Pick<VisibleGroup, "id" | "role">,
  contributedIds: ReadonlySet<string>,
): CollectiveStanding {
  const role = group.role?.trim() ? group.role : null;
  return { memberRole: role, hasContributed: contributedIds.has(group.id) };
}
