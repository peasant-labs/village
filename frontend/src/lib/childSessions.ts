import type { TranscriptListItem } from "@/lib/types";

/**
 * Grouping of a session list by the session that started each row.
 *
 * A harness can start a session from inside another session. The started
 * session is published as its own transcript that carries the starting
 * session's id in `parent_session_id`, and it classifies by content like any
 * other session, so it would otherwise sit in a browse list beside the session
 * that started it. This module folds those rows under their parent.
 *
 * The identity a `parent_session_id` names is `transcripts.local_id`, the id
 * the recording harness used. That column is unique per owner, not globally
 * (`UNIQUE (owner_id, local_id)` in the initial migration), so a parent is
 * matched on the owner AND the session id. Matching on the session id alone
 * would let one publisher's row capture another publisher's row.
 *
 * NOTHING IS EVER REMOVED FROM THE LIST HERE. A row is folded only when the
 * session that started it is in the SAME response; otherwise it keeps its
 * ordinary browse row, exactly as it had before this fold existed. The reason
 * is that one response is not the viewer's whole readable corpus: the list is
 * paged and filtered, so a parent can be absent for three different reasons --
 * it is on another page, the active filters excluded it, or the viewer may not
 * read it -- and this module cannot tell them apart. To leave a row out on that
 * evidence would make a session the viewer can open disappear from browsing.
 * Leaving out a row whose parent the viewer genuinely cannot see needs the
 * server to answer "may this viewer read the session named by this id", which
 * the discovery list cannot be asked today.
 */

/** One visible parent row and the rows it started, in server order. */
export type ChildSessionGroup = {
  parent: TranscriptListItem;
  children: TranscriptListItem[];
};

/** The two exclusive outcomes for the rows of one list response. */
export type ChildSessionGrouping = {
  /** Rows that keep their place in the browse list, in server order. */
  rootItems: TranscriptListItem[];
  /** One group per browse row that started at least one row in this response. */
  groups: ChildSessionGroup[];
};

/**
 * The collapsed chip's label. Lowercase chrome with a tabular count, matching
 * the agent-session group it shares its control with.
 */
export function childSessionGroupLabel(count: number): string {
  return `${count} child session${count === 1 ? "" : "s"}`;
}

// A NUL separator: neither an owner id nor a session id can contain it, so
// two different pairs can never collide on one key.
const KEY_SEPARATOR = "\u0000";

function sessionKey(ownerID: string, sessionID: string): string {
  return `${ownerID}${KEY_SEPARATOR}${sessionID}`;
}

function parentSessionID(item: TranscriptListItem): string | null {
  const raw = item.transcript.parent_session_id;
  if (typeof raw !== "string") return null;
  const trimmed = raw.trim();
  return trimmed.length > 0 ? trimmed : null;
}

/**
 * Fold a list response's rows into browse rows and per-parent groups.
 *
 * A row with no `parent_session_id` is a browse row and is untouched. So is a
 * row whose parent is not in this response, for the reason above. A row whose
 * chain of parents reaches a row in this response is folded under the topmost
 * such row, so a session started two levels down appears exactly once, under a
 * row the viewer can open.
 *
 * A row that names itself as its parent, and a ring of rows that name each
 * other, are both cycles; every row in one stays a browse row.
 */
export function groupChildSessions(items: TranscriptListItem[]): ChildSessionGrouping {
  const byKey = new Map<string, TranscriptListItem>();
  for (const item of items) {
    const key = sessionKey(item.transcript.owner_id, item.transcript.local_id);
    if (!byKey.has(key)) byKey.set(key, item);
  }

  /** The topmost row of `item`'s parent chain that is present in this response.
   *  Returns `item` itself when nothing above it is present. */
  function resolveRoot(item: TranscriptListItem): TranscriptListItem {
    const seen = new Set<string>();
    let current = item;
    for (;;) {
      const currentKey = sessionKey(current.transcript.owner_id, current.transcript.local_id);
      // A row that names itself, or a ring of rows that name each other, is a
      // cycle. Keep the row where it is rather than fold it into nothing.
      if (seen.has(currentKey)) return item;
      seen.add(currentKey);

      const parentID = parentSessionID(current);
      if (parentID === null) return current;

      const parent = byKey.get(sessionKey(current.transcript.owner_id, parentID));
      // The chain leaves this response. `current` is the highest row the fold
      // can honestly speak about, so it keeps its own browse row.
      if (parent === undefined) return current;
      current = parent;
    }
  }

  const rootItems: TranscriptListItem[] = [];
  const childrenByRootID = new Map<string, TranscriptListItem[]>();

  for (const item of items) {
    const root = resolveRoot(item);
    if (root === item) {
      rootItems.push(item);
      continue;
    }
    const rootID = root.transcript.id;
    const bucket = childrenByRootID.get(rootID);
    if (bucket === undefined) childrenByRootID.set(rootID, [item]);
    else bucket.push(item);
  }

  // Group order follows the browse rows, so the groups below the list read in
  // the same order as the rows they belong to.
  const groups: ChildSessionGroup[] = [];
  for (const parent of rootItems) {
    const children = childrenByRootID.get(parent.transcript.id);
    if (children !== undefined && children.length > 0) groups.push({ parent, children });
  }

  return { rootItems, groups };
}

/**
 * The number of transcripts the browse list is showing, for the count above the
 * grid and for the pager that reads the same value.
 *
 * The server counts every transcript matching the active filters, including the
 * ones this page folded into a group, so the raw number can disagree with the
 * cards under it. It is corrected ONLY when this response holds the whole
 * result set, because that is the only case where the page knows the whole
 * answer: there, the corrected count is exactly the browse rows on screen.
 *
 * On a longer result set the server's own total is returned unchanged. A
 * partial correction would be worse than no correction: the count also drives
 * the pager, this page's folds say nothing about any other page's, and a total
 * that shrinks as the viewer pages can end the pager early and put a page of
 * real sessions out of reach. Making the count exact on every page needs the
 * server to count what the fold leaves.
 */
export function visibleTranscriptTotal(
  serverTotal: number,
  responseRowCount: number,
  grouping: ChildSessionGrouping,
): number {
  if (serverTotal > responseRowCount) return serverTotal;
  return grouping.rootItems.length;
}

/**
 * The groups of one grouping, indexed by the id of the row they hang under.
 *
 * A list renders its rows in server order and asks, at each row, whether that
 * row started anything in this response. That is a lookup, so the grouping is
 * turned into one here rather than scanned once per row.
 *
 * The key is `transcript.id`, the row's own database id, which is what a list
 * already has in hand at the row it is rendering. It is not the
 * `(owner, local_id)` pair the fold matches parents on: that pair answers
 * "which row does this child name", and this map answers "what hangs under the
 * row I am drawing".
 */
export function childSessionsByParentID(
  grouping: ChildSessionGrouping,
): Map<string, TranscriptListItem[]> {
  const byParentID = new Map<string, TranscriptListItem[]>();
  for (const group of grouping.groups) {
    byParentID.set(group.parent.transcript.id, group.children);
  }
  return byParentID;
}
