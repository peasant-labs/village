import { groupSessionRows, namesAParent, type SessionIdentity } from "@/lib/childSessions";
import type { ContributableTranscript } from "./types";

/**
 * The closed set of node shapes the contribute tree renders. `orphans` is a
 * DISPLAY-ONLY synthetic grouping node — see {@link OrphansNode} — never a
 * real transcript, so it never carries an `id` a POST body can reference.
 */
export type ContributeNodeKind = "project" | "branch" | "session" | "orphans";

/** Project > branch > session tree root. Grouped by `project_hash` (the
 *  project's identity); `label` is the resolved `project_display_name`. */
export interface ProjectNode {
  kind: "project";
  /** `project_hash` — the stable grouping key. */
  id: string;
  label: string;
  children: (BranchNode | OrphansNode)[];
}

/** One branch within a project. `git_branch ?? null` is the grouping key;
 *  `label` degrades to `"(unknown branch)"` for a null branch, and unknown
 *  branches sort last among a project's branches. */
export interface BranchNode {
  kind: "branch";
  id: string;
  label: string;
  children: SessionNode[];
}

/** A single contributable transcript. `id` is the transcript id — the leaf
 *  identity {@link leafIds} collects and a batch-share POST names. `children`
 *  are every row this one started, however far down: a session started two
 *  levels below arrives here directly, under the topmost row present, and not
 *  nested a second time inside its own immediate starter. That is the shared
 *  fold's answer, and it is what makes the count on the disclosure the number
 *  of rows the control actually reveals. A session node inside `children` is
 *  therefore always a leaf. */
export interface SessionNode {
  kind: "session";
  id: string;
  label: string;
  row: ContributableTranscript;
  children: SessionNode[];
}

/** Synthetic per-project grouping for rows whose `parent_session_id` names no
 *  `local_id` present anywhere in the fetched corpus (a parent the caller
 *  does not own, already shared without being re-listed, or simply not
 *  returned by this page's limit). This node is never sent in a POST body —
 *  it exists only so an orphaned child transcript remains selectable and
 *  visible instead of silently vanishing from the tree. Ticking it ticks
 *  every session under it (via {@link toggleNode}); it can never itself be a
 *  leaf id. */
export interface OrphansNode {
  kind: "orphans";
  id: string;
  label: "orphaned transcripts";
  children: SessionNode[];
}

export type ContributeNode = ProjectNode | BranchNode | SessionNode | OrphansNode;

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

function contributableIdentity(row: ContributableTranscript): SessionIdentity {
  return {
    rowID: row.id,
    ownerID: SINGLE_OWNER_ENDPOINT,
    sessionID: row.local_id,
    parentSessionID: row.parent_session_id,
  };
}

function sessionNode(row: ContributableTranscript, children: ContributableTranscript[]): SessionNode {
  return {
    kind: "session",
    id: row.id,
    label: row.title ?? row.id,
    row,
    children: children.map((child) => sessionNode(child, [])),
  };
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
export function buildContributeTree(rows: ContributableTranscript[]): ProjectNode[] {
  const byProject = new Map<string, ContributableTranscript[]>();
  for (const row of rows) {
    const bucket = byProject.get(row.project_hash);
    if (bucket) bucket.push(row);
    else byProject.set(row.project_hash, [row]);
  }

  const projects: ProjectNode[] = [];
  for (const [projectHash, projectRows] of byProject) {
    // ONE fold, shared with every transcript list in this app. It is run per
    // project because `local_id` is a per-project value here: matching across
    // projects would be a false attach. Everything else -- which row keeps its
    // place, which row hangs under another, and what happens to a row that
    // names itself or a ring of rows that name each other -- is the shared
    // fold's answer, not a second one written here.
    const fold = groupSessionRows(projectRows, contributableIdentity);
    const startedBy = new Map(
      fold.groups.map((group) => [group.parent.id, group.children]),
    );

    const branchRoots: ContributableTranscript[] = [];
    const orphanRoots: ContributableTranscript[] = [];
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
      if (!namesAParent(contributableIdentity(row))) branchRoots.push(row);
      else orphanRoots.push(row);
    }

    const branchesByKey = new Map<string, ContributableTranscript[]>();
    for (const row of branchRoots) {
      const key = row.git_branch ?? "";
      const bucket = branchesByKey.get(key);
      if (bucket) bucket.push(row);
      else branchesByKey.set(key, [row]);
    }
    const branches: BranchNode[] = [...branchesByKey.entries()]
      .map(([key, branchRows]): BranchNode => ({
        kind: "branch",
        id: `${projectHash}::branch::${key || "__unknown__"}`,
        label: key || UNKNOWN_BRANCH_LABEL,
        children: branchRows.map((row) => sessionNode(row, startedBy.get(row.id) ?? [])),
      }))
      .sort((a, b) => branchSortKey(a.label).localeCompare(branchSortKey(b.label)));

    const children: (BranchNode | OrphansNode)[] = [...branches];
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
