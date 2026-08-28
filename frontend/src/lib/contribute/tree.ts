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
 *  are the rows whose `parent_session_id` names this row's `local_id`,
 *  recursively — so a session's own descendants nest here whether the
 *  session itself is a normal branch root or an orphan root (see
 *  {@link OrphansNode}). */
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

function buildSessionNode(
  row: ContributableTranscript,
  childrenByParentLocalId: Map<string, ContributableTranscript[]>,
): SessionNode {
  const children = (childrenByParentLocalId.get(row.local_id) ?? []).map((child) =>
    buildSessionNode(child, childrenByParentLocalId),
  );
  return {
    kind: "session",
    id: row.id,
    label: row.title ?? row.id,
    row,
    children,
  };
}

/**
 * Builds the project > branch > session tree the contribute page renders.
 *
 * Grouping key is `project_hash` (label `project_display_name`); within a
 * project, branch key is `git_branch` (label falls back to
 * {@link UNKNOWN_BRANCH_LABEL}, sorted last). A row is a branch ROOT when its
 * `parent_session_id` is `null`; every other row nests under the row whose
 * `local_id` it names, scoped to the SAME project — `local_id` is a
 * per-project value, so matching across projects would be a false attach. A
 * row whose named parent does not exist anywhere in this project's rows (not
 * fetched, foreign, or already shared off the list) becomes an ORPHAN root
 * under that project's single synthetic {@link OrphansNode} instead of being
 * silently dropped.
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
    const localIds = new Set(projectRows.map((row) => row.local_id));
    const childrenByParentLocalId = new Map<string, ContributableTranscript[]>();
    const branchRoots: ContributableTranscript[] = [];
    const orphanRoots: ContributableTranscript[] = [];

    for (const row of projectRows) {
      if (row.parent_session_id == null) {
        branchRoots.push(row);
        continue;
      }
      if (!localIds.has(row.parent_session_id)) {
        // The named parent is absent from this project's corpus: this row is
        // an orphan ROOT (its own descendants, if any, still nest normally).
        orphanRoots.push(row);
        continue;
      }
      const bucket = childrenByParentLocalId.get(row.parent_session_id);
      if (bucket) bucket.push(row);
      else childrenByParentLocalId.set(row.parent_session_id, [row]);
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
        children: branchRows.map((row) => buildSessionNode(row, childrenByParentLocalId)),
      }))
      .sort((a, b) => branchSortKey(a.label).localeCompare(branchSortKey(b.label)));

    const children: (BranchNode | OrphansNode)[] = [...branches];
    if (orphanRoots.length > 0) {
      children.push({
        kind: "orphans",
        id: `${projectHash}::orphans`,
        label: "orphaned transcripts",
        children: orphanRoots.map((row) => buildSessionNode(row, childrenByParentLocalId)),
      });
    }

    const label = projectRows[0]?.project_display_name ?? projectHash;
    projects.push({ kind: "project", id: projectHash, label, children });
  }

  return projects.sort((a, b) => a.label.localeCompare(b.label));
}
