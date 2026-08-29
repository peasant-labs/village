import { groupSessionRows, namesAParent, type SessionIdentity } from "@/lib/childSessions";
import type { ContributableTranscript } from "./types";

/**
 * The facts the project > branch > session fold reads from ONE row, whichever
 * list the row came from. The contribute page folds transcripts the caller may
 * OFFER; the review page folds submissions a moderator must DECIDE. They are
 * different wire shapes answering different questions, and both state these
 * seven facts, so the fold is written once against them rather than twice
 * against each list's own row.
 */
export interface TreeRowFacts {
  /** The transcript id - the leaf identity a selection and a request body name. */
  id: string;
  /** The id the recording harness used. Unique per owner, not globally. */
  local_id: string;
  title: string | null;
  project_hash: string;
  project_display_name: string;
  git_branch: string | null;
  parent_session_id: string | null;
}

/**
 * What a session row SAYS, decided by the page that owns the list rather than
 * by the fold. The two pages describe the same session differently - a
 * contributor reads "claude-code, a date, private", a moderator reads "by
 * @someone, a date" - and neither meaning belongs inside a shared fold.
 */
export interface SessionPresentation {
  /** The mono metadata line under the title. */
  meta: string;
  /** A short mark beside the title, or null for no mark. */
  mark: string | null;
  /** The row is drawn but can never enter a selection: already offered to
   *  this collective on the contribute tree, already decided by another
   *  moderator on the review tree. */
  locked: boolean;
}

/**
 * The closed set of node shapes the contribute tree renders. `orphans` is a
 * DISPLAY-ONLY synthetic grouping node — see {@link OrphansNode} — never a
 * real transcript, so it never carries an `id` a POST body can reference.
 */
export type ContributeNodeKind = "project" | "branch" | "session" | "orphans";

/** Project > branch > session tree root. Grouped by `project_hash` (the
 *  project's identity); `label` is the resolved `project_display_name`. */
export interface ProjectNode<Row extends TreeRowFacts = ContributableTranscript> {
  kind: "project";
  /** `project_hash` — the stable grouping key. */
  id: string;
  label: string;
  children: (BranchNode<Row> | OrphansNode<Row>)[];
}

/** One branch within a project. `git_branch ?? null` is the grouping key;
 *  `label` degrades to `"(unknown branch)"` for a null branch, and unknown
 *  branches sort last among a project's branches. */
export interface BranchNode<Row extends TreeRowFacts = ContributableTranscript> {
  kind: "branch";
  id: string;
  label: string;
  children: SessionNode<Row>[];
}

/** A single contributable transcript. `id` is the transcript id — the leaf
 *  identity {@link leafIds} collects and a batch-share POST names. `children`
 *  are every row this one started, however far down: a session started two
 *  levels below arrives here directly, under the topmost row present, and not
 *  nested a second time inside its own immediate starter. That is the shared
 *  fold's answer, and it is what makes the count on the disclosure the number
 *  of rows the control actually reveals. A session node inside `children` is
 *  therefore always a leaf. */
export interface SessionNode<Row extends TreeRowFacts = ContributableTranscript>
  extends SessionPresentation {
  kind: "session";
  id: string;
  label: string;
  row: Row;
  children: SessionNode<Row>[];
}

/** Synthetic per-project grouping for rows whose `parent_session_id` names no
 *  `local_id` present anywhere in the fetched corpus (a parent the caller
 *  does not own, already shared without being re-listed, or simply not
 *  returned by this page's limit). This node is never sent in a POST body —
 *  it exists only so an orphaned child transcript remains selectable and
 *  visible instead of silently vanishing from the tree. Ticking it ticks
 *  every session under it (via {@link toggleNode}); it can never itself be a
 *  leaf id. */
export interface OrphansNode<Row extends TreeRowFacts = ContributableTranscript> {
  kind: "orphans";
  id: string;
  label: "orphaned transcripts";
  children: SessionNode<Row>[];
}

export type ContributeNode<Row extends TreeRowFacts = ContributableTranscript> =
  | ProjectNode<Row>
  | BranchNode<Row>
  | SessionNode<Row>
  | OrphansNode<Row>;

const UNKNOWN_BRANCH_LABEL = "(unknown branch)";

function branchSortKey(label: string): string {
  // Unknown branch sorts after every named branch, regardless of its literal
  // text — a plain string sort would place "(unknown branch)" arbitrarily
  // among real branch names depending on punctuation.
  return label === UNKNOWN_BRANCH_LABEL ? `￿${label}` : label;
}

/**
 * The shared fold's four facts, read out of a contributable row.
 *
 * `GET /groups/{id}/contributable` answers with the caller's OWN transcripts
 * only, so every row here has one owner and the fold's per-owner scoping has
 * nothing to separate. It is still stated, because the fold matches a parent on
 * the owner AND the session id, and a constant is the honest way to say "these
 * all belong to the same person" rather than leaving the field to be guessed.
 *
 * The constant is named for its PRECONDITION, because that precondition is
 * load-bearing: per-owner scoping is what stops one publisher's session id
 * capturing another publisher's row, and a single owner switches it off. If
 * that endpoint ever answers with more than one person's rows -- an owner
 * selecting on someone's behalf, an organisation-scoped listing -- this
 * silently merges them and no test goes red. Widening it means giving
 * `ContributableTranscript` a real `owner_id` and reading it here, and the
 * wire-contract note in `./types.ts` says so at the other end.
 */
const SINGLE_OWNER_ENDPOINT = "the caller";

/**
 * How the fold identifies ONE row's owner. A list that can hold several
 * publishers' rows (the review queue) must state the real owner, because
 * `local_id` is unique per owner and matching on the session id alone would
 * let one publisher's submission capture another publisher's.
 */
export type OwnerOf<Row> = (row: Row) => string;

function identityOf<Row extends TreeRowFacts>(ownerOf: OwnerOf<Row>) {
  return (row: Row): SessionIdentity => ({
    rowID: row.id,
    ownerID: ownerOf(row),
    sessionID: row.local_id,
    parentSessionID: row.parent_session_id,
  });
}

/**
 * Builds the project > branch > session tree the contribute page renders.
 *
 * Grouping key is `project_hash` (label `project_display_name`); within a
 * project, branch key is `git_branch` (label falls back to
 * {@link UNKNOWN_BRANCH_LABEL}, sorted last).
 *
 * Which row hangs under which is the SHARED fold's answer, not one written
 * here, and it is not the same rule this function used to apply: a row hangs
 * under the TOPMOST row of its parent chain present in this project, so a
 * session started two levels down appears once, directly under that row, and
 * never nested a second time inside its own immediate starter. `SessionNode`
 * documents that shape.
 *
 * A row is a branch ROOT when it names no parent. A row that kept its own
 * place while still naming one is an ORPHAN, and there are two ways to be one:
 * the row it names is not in this project's corpus (not fetched, foreign, or
 * already shared off the list), or the chain it names is a ring. Both go under
 * that project's single synthetic {@link OrphansNode} rather than being
 * silently dropped — the ring case used to vanish from this tree entirely.
 *
 * The fold runs per project because `local_id` is a per-project value here;
 * matching across projects would be a false attach.
 */
export function buildSessionTree<Row extends TreeRowFacts>(
  rows: Row[],
  present: (row: Row) => SessionPresentation,
  ownerOf: OwnerOf<Row>,
): ProjectNode<Row>[] {
  function sessionNode(row: Row, children: Row[]): SessionNode<Row> {
    return {
      kind: "session",
      id: row.id,
      label: row.title ?? row.id,
      row,
      ...present(row),
      children: children.map((child) => sessionNode(child, [])),
    };
  }

  const byProject = new Map<string, Row[]>();
  for (const row of rows) {
    const bucket = byProject.get(row.project_hash);
    if (bucket) bucket.push(row);
    else byProject.set(row.project_hash, [row]);
  }

  const projects: ProjectNode<Row>[] = [];
  for (const [projectHash, projectRows] of byProject) {
    // ONE fold, shared with every transcript list in this app. It is run per
    // project because `local_id` is a per-project value here: matching across
    // projects would be a false attach. Everything else -- which row keeps its
    // place, which row hangs under another, and what happens to a row that
    // names itself or a ring of rows that name each other -- is the shared
    // fold's answer, not a second one written here.
    const identify = identityOf(ownerOf);
    const fold = groupSessionRows(projectRows, identify);
    const startedBy = new Map(
      fold.groups.map((group) => [group.parent.id, group.children]),
    );

    const branchRoots: Row[] = [];
    const orphanRoots: Row[] = [];
    for (const row of fold.rootItems) {
      // A row that kept its place while still naming a parent is an ORPHAN: the
      // session that started it is not in this project's corpus (not fetched,
      // foreign, already shared off the list) or the chain it names is a ring.
      // It is shown under the project's synthetic orphans grouping rather than
      // dropped, so it stays selectable.
      // Asked through the fold's OWN reading, not by testing the raw column:
      // the fold treats a blank string as naming nobody, and a second reading
      // here that disagreed would file an ordinary root session under
      // "orphaned transcripts" and tell the person their starter is missing.
      if (!namesAParent(identify(row))) branchRoots.push(row);
      else orphanRoots.push(row);
    }

    const branchesByKey = new Map<string, Row[]>();
    for (const row of branchRoots) {
      const key = row.git_branch ?? "";
      const bucket = branchesByKey.get(key);
      if (bucket) bucket.push(row);
      else branchesByKey.set(key, [row]);
    }
    const branches: BranchNode<Row>[] = [...branchesByKey.entries()]
      .map(([key, branchRows]): BranchNode<Row> => ({
        kind: "branch",
        id: `${projectHash}::branch::${key || "__unknown__"}`,
        label: key || UNKNOWN_BRANCH_LABEL,
        children: branchRows.map((row) => sessionNode(row, startedBy.get(row.id) ?? [])),
      }))
      .sort((a, b) => branchSortKey(a.label).localeCompare(branchSortKey(b.label)));

    const children: (BranchNode<Row> | OrphansNode<Row>)[] = [...branches];
    if (orphanRoots.length > 0) {
      children.push({
        kind: "orphans",
        id: `${projectHash}::orphans`,
        label: "orphaned transcripts",
        children: orphanRoots.map((row) => sessionNode(row, startedBy.get(row.id) ?? [])),
      });
    }

    const label = projectRows[0]?.project_display_name ?? projectHash;
    projects.push({ kind: "project", id: projectHash, label, children });
  }

  return projects.sort((a, b) => a.label.localeCompare(b.label));
}

/**
 * The contribute page's tree: the caller's OWN transcripts, so every row has
 * one owner and a row already offered here is locked out of the selection.
 */
export function buildContributeTree(rows: ContributableTranscript[]): ProjectNode<ContributableTranscript>[] {
  return buildSessionTree(
    rows,
    (row) => ({
      meta: contributableMeta(row),
      mark: row.already_shared ? "already contributed" : null,
      locked: row.already_shared,
    }),
    () => SINGLE_OWNER_ENDPOINT,
  );
}

function formatDate(iso: string): string {
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return "unknown date";
  return parsed.toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

/**
 * The secondary metadata line under a contributable session's title: the
 * harness that produced it, the date it was published, and its stored
 * visibility. A single mono line keeps every row the same height, so the
 * column reads as one list instead of a ragged stack of pills.
 */
export function contributableMeta(row: ContributableTranscript): string {
  return [row.model_provider || "unknown harness", formatDate(row.published_at), row.visibility].join(" · ");
}

export { formatDate as formatSessionDate };
