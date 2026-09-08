import type { VillageSessionRow } from "@peasant-labs/schema";

/**
 * Build explicit-ID batches from individually selected server rows. A context
 * container or helper-group summary is not a row and cannot be selected here.
 * Selection retains its own records across pages; a later page or filter must
 * never reinterpret a remembered ID as all members of its parent or project.
 */
export function groupedContributionBatches(selected: readonly VillageSessionRow[]): Map<string, string[]> {
  const batches = new Map<string, string[]>();
  const seen = new Set<string>();
  for (const row of selected) {
    const contribution = row.contributable;
    if (!contribution || contribution.already_shared || contribution.id !== row.session.id) {
      throw new Error("groupedContributionBatches refused before submission: a selected row is not currently contributable; nothing was sent; refresh the contribute list and select eligible transcripts individually");
    }
    if (seen.has(contribution.id)) continue;
    seen.add(contribution.id);
    const ids = batches.get(contribution.project_hash) ?? [];
    ids.push(contribution.id);
    batches.set(contribution.project_hash, ids);
  }
  return batches;
}

/** The existing selected-run path must never invoke whole-project fallback. */
export function requireExplicitContributionIDs(ids: readonly string[]): void {
  if (ids.length === 0) {
    throw new Error("contribute run refused before submission: this selected project contains no explicit transcript IDs; nothing was sent; select at least one transcript, or use the separately confirmed whole-project action");
  }
}
