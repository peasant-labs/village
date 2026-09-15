import type { VillageSessionListItem } from "@peasant-labs/schema";

/**
 * Build explicit-ID batches from individually selected server rows. A context
 * container or helper-group summary is not a row and cannot be selected here.
 * Selection retains its own records across pages; a later page or filter must
 * never reinterpret a remembered ID as all members of its parent or project.
 */
export function groupedContributionBatches(selected: readonly VillageSessionListItem[]): Map<string, string[]> {
  const batches = new Map<string, string[]>();
  const seen = new Set<string>();
  for (const item of selected) {
    const row = item.transcript;
    if (item.kind !== "transcript" || !row || item.context) {
      throw new Error("groupedContributionBatches refused before submission: a context container or helper group cannot be selected; nothing was sent; expand the group and select an eligible transcript individually");
    }
    const contribution = row.contributable;
    if (!contribution || contribution.already_shared || contribution.id !== row.session.id) {
      throw new Error("groupedContributionBatches refused before submission: a selected row is not currently contributable; nothing was sent; refresh the contribute list and select eligible transcripts individually");
    }
    const transcriptId = row.session.id;
    if (seen.has(transcriptId)) continue;
    seen.add(transcriptId);
    const ids = batches.get(contribution.project_hash) ?? [];
    ids.push(transcriptId);
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

/**
 * The union of explicit-ID batch maps, keyed by project. The ordinary tree
 * contributes the rows a contributor ticked there; the grouped helper
 * disclosures contribute the members ticked individually under an expanded
 * group. Both maps already carry explicit transcript ids, and one identity
 * repeated across them is still one request entry.
 */
export function mergeContributionBatches(
  ...maps: readonly Map<string, string[]>[]
): Map<string, string[]> {
  const merged = new Map<string, string[]>();
  const seen = new Map<string, Set<string>>();
  for (const map of maps) {
    for (const [projectHash, ids] of map) {
      const known = seen.get(projectHash) ?? new Set<string>();
      for (const id of ids) {
        if (known.has(id)) continue;
        known.add(id);
        const target = merged.get(projectHash) ?? [];
        target.push(id);
        merged.set(projectHash, target);
      }
      seen.set(projectHash, known);
    }
  }
  return merged;
}

/**
 * The explicit transcript ids a review decision may name, from submissions a
 * moderator ticked individually under a grouped helper disclosure. A context
 * container or helper-group summary is not a submission and cannot be decided
 * here; a row that is no longer a live pending submission is refused rather
 * than sent, so a stale membership can never be approved by accident.
 */
export function groupedReviewSelection(selected: readonly VillageSessionListItem[]): string[] {
  const ids: string[] = [];
  const seen = new Set<string>();
  for (const item of selected) {
    const row = item.transcript;
    if (item.kind !== "transcript" || !row || item.context) {
      throw new Error("review selection refused before submission: a context container or helper group cannot be decided; nothing was sent; expand the group and select an eligible submission individually");
    }
    const pending = row.pending;
    if (!pending || pending.transcript_id !== row.session.id) {
      throw new Error("review selection refused before submission: a selected row is not a live pending submission; nothing was sent; refresh the review queue and select submissions individually");
    }
    if (seen.has(row.session.id)) continue;
    seen.add(row.session.id);
    ids.push(row.session.id);
  }
  return ids;
}
