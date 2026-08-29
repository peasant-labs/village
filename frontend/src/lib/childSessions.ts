import type { Transcript, TranscriptListItem } from "@/lib/types";

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
 *
 * Every list in this app folds with the SAME implementation. The surfaces do
 * not agree on a row shape -- a collective's browse table, a moderation queue,
 * a person's own contributions and the contribute tree each receive their own
 * wire shape -- so the fold is written once over an identity a caller reads out
 * of its own row ({@link SessionIdentity}), and {@link groupChildSessions} is
 * that reading for the list-item shape most surfaces already hold. A second
 * fold would be a second answer to "what started this session", and the two
 * would drift.
 */

/**
 * What the fold must know about one row, whatever shape that row arrives in.
 *
 * A caller reads this out of its own wire type. Nothing else about the row is
 * the fold's business, which is what lets one implementation serve a browse
 * table, a moderation queue and a selection tree at once.
 */
export type SessionIdentity = {
  /** The row's own identity within this list -- what a renderer already has in
   *  hand at the row it is drawing, and the key {@link childSessionsByRowID}
   *  indexes a group by. It is NOT the harness session id. */
  rowID: string;
  /** The publisher. Session ids are unique per owner, never globally. */
  ownerID: string;
  /** The id the recording harness used for this session (`local_id`). */
  sessionID: string;
  /** The harness id of the session that started this one, or absent when
   *  nothing did. A missing, null or blank value is read as "nothing started
   *  it": the wire shapes on these surfaces do not agree on how they spell an
   *  absence, and all of them mean the same thing here. */
  parentSessionID: string | null | undefined;
};

/** One visible parent row and the rows it started, in server order. */
export type SessionGroup<Row> = {
  parent: Row;
  children: Row[];
};

/** The two exclusive outcomes for the rows of one list response. */
export type SessionGrouping<Row> = {
  /** Rows that keep their place in the browse list, in server order. */
  rootItems: Row[];
  /** One group per browse row that started at least one row in this response. */
  groups: SessionGroup<Row>[];
};

/** One visible parent row and the rows it started, for the list-item shape. */
export type ChildSessionGroup = SessionGroup<TranscriptListItem>;

/** The two exclusive outcomes, for the list-item shape. */
export type ChildSessionGrouping = SessionGrouping<TranscriptListItem>;

/**
 * The collapsed control's label. Lowercase chrome with a tabular count.
 *
 * Every surface in this app labels the sessions one row started with this
 * function, so the wording is decided in one place. It carries no leading `+`:
 * the control hangs off its own parent's row, where the count reads as part of
 * that row rather than as an item being offered.
 */
export function childSessionGroupLabel(count: number): string {
  return `${count} child session${count === 1 ? "" : "s"}`;
}

/**
 * The same count, plus how many of the rows this control HIDES are selected.
 *
 * A list whose rows carry checkboxes has a failure this wording exists to
 * close: selecting a parent reaches the rows folded under it, and a fold
 * starts CLOSED, so a viewer can select a group, untick every box they can
 * see, and still be holding a selection made entirely of rows that are off
 * screen. Where the action on that selection cannot be taken back - approving
 * or rejecting a contribution, removing one from a collective - a count they
 * cannot see is not good enough.
 *
 * `selectedCount` of zero reads as the bare label: a "0 selected" hanging off
 * every unselected fold in a long list is noise, and the thing worth saying is
 * that a hidden row IS selected.
 *
 * It lives here, beside {@link childSessionGroupLabel}, because this module is
 * the one place that decides how this app names the sessions a session started
 * - a second module composing these words is exactly what that rule forbids.
 */
export function childSessionGroupSelectionLabel(count: number, selectedCount: number): string {
  const hidden = childSessionGroupLabel(count);
  return selectedCount > 0 ? `${hidden}, ${selectedCount} selected` : hidden;
}

// A NUL separator: neither an owner id nor a session id can contain it, so
// two different pairs can never collide on one key.
const KEY_SEPARATOR = "\u0000";

function sessionKey(ownerID: string, sessionID: string): string {
  return `${ownerID}${KEY_SEPARATOR}${sessionID}`;
}

/**
 * The parent a row names, or null when it names none.
 *
 * A blank string is an absence: some of these wire shapes carry "no parent"
 * that way (`pgtype.Text` marshals a present-but-empty column as `""`, not as
 * null). Exported because a caller that ALSO branches on "does this row name a
 * parent" must reach the same answer -- the contribute tree asks exactly that
 * to tell a branch root from an orphan, and testing the raw column there would
 * file an ordinary root session under "orphaned transcripts" the moment a
 * publisher sent a blank.
 */
export function namesAParent(row: {
  parentSessionID: string | null | undefined;
}): boolean {
  return namedParent(row) !== null;
}

function namedParent(identity: {
  parentSessionID: string | null | undefined;
}): string | null {
  const raw = identity.parentSessionID;
  if (typeof raw !== "string") return null;
  const trimmed = raw.trim();
  return trimmed.length > 0 ? trimmed : null;
}

/**
 * Fold a list response's rows into browse rows and per-parent groups.
 *
 * A row that names no parent is a browse row and is untouched. So is a row
 * whose parent is not in this response, for the reason in this module's own
 * documentation. A row whose chain of parents reaches a row in this response is
 * folded under the topmost such row, so a session started two levels down
 * appears exactly once, under a row the viewer can open.
 *
 * A row that names itself as its parent, and a ring of rows that name each
 * other, are both cycles; every row in one stays a browse row.
 *
 * `identify` reads the fold's four facts out of the caller's own row shape. It
 * is called more than once for a row and must answer the same way each time.
 */
export function groupSessionRows<Row>(
  rows: Row[],
  identify: (row: Row) => SessionIdentity,
): SessionGrouping<Row> {
  const byKey = new Map<string, Row>();
  for (const row of rows) {
    const identity = identify(row);
    const key = sessionKey(identity.ownerID, identity.sessionID);
    if (!byKey.has(key)) byKey.set(key, row);
  }

  /** The topmost row of `row`'s parent chain that is present in this response.
   *  Returns `row` itself when nothing above it is present. */
  function resolveRoot(row: Row): Row {
    const seen = new Set<string>();
    let current = row;
    for (;;) {
      const identity = identify(current);
      const currentKey = sessionKey(identity.ownerID, identity.sessionID);
      // A row that names itself, or a ring of rows that name each other, is a
      // cycle. Keep the row where it is rather than fold it into nothing.
      if (seen.has(currentKey)) return row;
      seen.add(currentKey);

      const parentID = namedParent(identity);
      if (parentID === null) return current;

      const parent = byKey.get(sessionKey(identity.ownerID, parentID));
      // The chain leaves this response. `current` is the highest row the fold
      // can honestly speak about, so it keeps its own browse row.
      if (parent === undefined) return current;
      current = parent;
    }
  }

  const rootItems: Row[] = [];
  const childrenByRootID = new Map<string, Row[]>();

  for (const row of rows) {
    const root = resolveRoot(row);
    if (root === row) {
      rootItems.push(row);
      continue;
    }
    const rootID = identify(root).rowID;
    const bucket = childrenByRootID.get(rootID);
    if (bucket === undefined) childrenByRootID.set(rootID, [row]);
    else bucket.push(row);
  }

  // Group order follows the browse rows, so the groups below the list read in
  // the same order as the rows they belong to.
  const groups: SessionGroup<Row>[] = [];
  for (const parent of rootItems) {
    const children = childrenByRootID.get(identify(parent).rowID);
    if (children !== undefined && children.length > 0) groups.push({ parent, children });
  }

  return { rootItems, groups };
}

/**
 * A row that carries a whole transcript. Several endpoints answer with one --
 * the transcripts list, a project's page, a collective's browse page -- and
 * they differ in what ELSE they send, so the fold names only the part it reads.
 */
export type TranscriptCarryingRow = {
  transcript: Pick<Transcript, "id" | "owner_id" | "local_id" | "parent_session_id">;
};

/**
 * The fold's four facts, read out of a row that carries a transcript. Exported
 * so a surface holding one shape and a surface holding another name the same
 * reading rather than each inventing one.
 */
export function transcriptListItemIdentity(item: TranscriptCarryingRow): SessionIdentity {
  return {
    rowID: item.transcript.id,
    ownerID: item.transcript.owner_id,
    sessionID: item.transcript.local_id,
    parentSessionID: item.transcript.parent_session_id,
  };
}

/** {@link groupSessionRows} for any row that carries a whole transcript. */
export function groupChildSessions<Row extends TranscriptCarryingRow>(
  items: Row[],
): SessionGrouping<Row> {
  return groupSessionRows(items, transcriptListItemIdentity);
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
export function visibleTranscriptTotal<Row>(
  serverTotal: number,
  responseRowCount: number,
  grouping: SessionGrouping<Row>,
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
 * The key is {@link SessionIdentity.rowID}, the row's own identity in the list,
 * which is what a list already has in hand at the row it is rendering. It is
 * not the `(owner, session id)` pair the fold matches parents on: that pair
 * answers "which row does this child name", and this map answers "what hangs
 * under the row I am drawing".
 */
export function childSessionsByRowID<Row>(
  grouping: SessionGrouping<Row>,
  identify: (row: Row) => SessionIdentity,
): Map<string, Row[]> {
  const byRowID = new Map<string, Row[]>();
  for (const group of grouping.groups) {
    byRowID.set(identify(group.parent).rowID, group.children);
  }
  return byRowID;
}

/** {@link childSessionsByRowID} for any row that carries a whole transcript. */
export function childSessionsByParentID<Row extends TranscriptCarryingRow>(
  grouping: SessionGrouping<Row>,
): Map<string, Row[]> {
  return childSessionsByRowID(grouping, transcriptListItemIdentity);
}
