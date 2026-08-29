import type { ContributableTranscript } from "./types";
import type { ContributeNode, ProjectNode, SessionNode, TreeRowFacts } from "./tree";

/** The selection is the set of selected LEAF transcript ids — never a
 *  set of node ids, so a project/branch/orphans node id can never leak into
 *  a POST body by being present in this set. */
export type Selection = ReadonlySet<string>;

/** The tri-state a checkbox row renders: no descendant selected, every
 *  eligible descendant selected, or something in between. */
export type NodeState = "none" | "some" | "all";

function isSessionNode<Row extends TreeRowFacts>(node: ContributeNode<Row>): node is SessionNode<Row> {
  return node.kind === "session";
}

/** Every real transcript under `node`, depth-first, INCLUDING `node` itself
 *  when it is already a session. Walks through project/branch/orphans
 *  grouping nodes transparently — the orphans synthetic node is never itself
 *  returned, only the real sessions nested under it. */
function sessionNodes<Row extends TreeRowFacts>(node: ContributeNode<Row>): SessionNode<Row>[] {
  if (isSessionNode(node)) {
    return [node, ...node.children.flatMap((child) => sessionNodes<Row>(child))];
  }
  return node.children.flatMap((child) => sessionNodes<Row>(child));
}

/** Leaf transcript ids under `node`, EXCLUDING every LOCKED row — one already
 *  offered to this collective on the contribute tree, or one another moderator
 *  already decided on the review tree. A locked row is rendered disabled and
 *  can never enter a selection. */
export function leafIds<Row extends TreeRowFacts>(node: ContributeNode<Row>): string[] {
  return sessionNodes(node)
    .filter((session) => !session.locked)
    .map((session) => session.id);
}

/** The tri-state a node's checkbox row should render, per {@link NodeState}.
 *  A node with no selectable descendant (every row already shared, or an
 *  empty grouping) reads `"none"` — there is nothing to tick "all" of. */
export function nodeState<Row extends TreeRowFacts>(selection: Selection, node: ContributeNode<Row>): NodeState {
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
export function toggleNode<Row extends TreeRowFacts>(selection: Selection, node: ContributeNode<Row>): Selection {
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
export function selectAll<Row extends TreeRowFacts>(tree: ProjectNode<Row>[]): Selection {
  return new Set(tree.flatMap(leafIds));
}

/** The selected ids whose stored `visibility` is `"private"` — the
 *  confirm-before-share gate fires on a non-empty result. `tree` supplies the
 *  row lookup; a selection can only ever name ids the tree produced. */
export function privateIds(selection: Selection, tree: ProjectNode<ContributableTranscript>[]): string[] {
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
export function groupByProject<Row extends TreeRowFacts>(
  selection: Selection,
  tree: ProjectNode<Row>[],
): Map<string, string[]> {
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
export function sessionRows<Row extends TreeRowFacts>(node: ContributeNode<Row>): Row[] {
  return sessionNodes(node).map((session) => session.row);
}
