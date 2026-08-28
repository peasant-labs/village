import type { ContributeNode, ProjectNode, SessionNode } from "./tree";

/** The selection is the set of selected LEAF transcript ids — never a
 *  set of node ids, so a project/branch/orphans node id can never leak into
 *  a POST body by being present in this set. */
export type Selection = ReadonlySet<string>;

/** The tri-state a checkbox row renders: no descendant selected, every
 *  eligible descendant selected, or something in between. */
export type NodeState = "none" | "some" | "all";

function isSessionNode(node: ContributeNode): node is SessionNode {
  return node.kind === "session";
}

/** Every real transcript under `node`, depth-first, INCLUDING `node` itself
 *  when it is already a session. Walks through project/branch/orphans
 *  grouping nodes transparently — the orphans synthetic node is never itself
 *  returned, only the real sessions nested under it. */
function sessionNodes(node: ContributeNode): SessionNode[] {
  if (isSessionNode(node)) {
    return [node, ...node.children.flatMap(sessionNodes)];
  }
  return node.children.flatMap(sessionNodes);
}

/** Leaf transcript ids under `node`, EXCLUDING any row already shared into
 *  this collective — an already-shared row is rendered disabled and can
 *  never enter a selection (see `already_shared_disabled`). */
export function leafIds(node: ContributeNode): string[] {
  return sessionNodes(node)
    .filter((session) => !session.row.already_shared)
    .map((session) => session.id);
}

/** The tri-state a node's checkbox row should render, per {@link NodeState}.
 *  A node with no selectable descendant (every row already shared, or an
 *  empty grouping) reads `"none"` — there is nothing to tick "all" of. */
export function nodeState(selection: Selection, node: ContributeNode): NodeState {
  const eligible = leafIds(node);
  if (eligible.length === 0) return "none";
  const selectedCount = eligible.filter((id) => selection.has(id)).length;
  if (selectedCount === 0) return "none";
  if (selectedCount === eligible.length) return "all";
  return "some";
}

/** Flips `node`'s tri-state: `"all"` clears every eligible descendant leaf,
 *  `"none"`/`"some"` selects every eligible descendant leaf. Returns a NEW
 *  `Selection` (selections are immutable — see {@link Selection}). */
export function toggleNode(selection: Selection, node: ContributeNode): Selection {
  const eligible = leafIds(node);
  const next = new Set(selection);
  if (nodeState(selection, node) === "all") {
    for (const id of eligible) next.delete(id);
  } else {
    for (const id of eligible) next.add(id);
  }
  return next;
}

/** Every selectable (not-already-shared) leaf id across the whole tree. */
export function selectAll(tree: ProjectNode[]): Selection {
  return new Set(tree.flatMap(leafIds));
}

/** The selected ids whose stored `visibility` is `"private"` — the
 *  confirm-before-share gate fires on a non-empty result. `tree` supplies the
 *  row lookup; a selection can only ever name ids the tree produced. */
export function privateIds(selection: Selection, tree: ProjectNode[]): string[] {
  const rows = tree.flatMap((project) => sessionNodes(project));
  return rows
    .filter((session) => selection.has(session.id) && session.row.visibility === "private")
    .map((session) => session.id);
}

/** Groups the CURRENT selection by `project_hash`, in project order, dropping
 *  any project with nothing selected — the caller (`useContributeRun`) fires
 *  exactly one POST per returned entry, so a project with zero selected
 *  ids must never appear as an empty-array entry. Node ids (branch/orphans)
 *  never leak in: every array here is exclusively transcript ids from
 *  {@link leafIds}. */
export function groupByProject(selection: Selection, tree: ProjectNode[]): Map<string, string[]> {
  const grouped = new Map<string, string[]>();
  for (const project of tree) {
    const ids = leafIds(project).filter((id) => selection.has(id));
    if (ids.length > 0) grouped.set(project.id, ids);
  }
  return grouped;
}

/** Every distinct harness (`model_provider`) among a node's leaf sessions —
 *  used by the filter UI's per-harness counts. Exported here (rather than
 *  duplicated in `filter.ts`) because it walks the same tree structure. */
export function sessionRows(node: ContributeNode) {
  return sessionNodes(node).map((session) => session.row);
}
