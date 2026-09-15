import type { SessionRelationshipNavigation } from '@peasant-labs/schema';

/**
 * The exact branch-point anchor the host carries to the current target.
 *
 * A child's stored `context_from` relationship may record where it branched
 * off its source. The authorized metadata read emits an exact anchor
 * (`resolved` + entry ref + public revision) only when the stored revision
 * still identifies the target's current public representation; otherwise it
 * emits `general_link_only`. Village owns the route between the two, so it
 * must carry the verified anchor across that navigation instead of dropping
 * it and landing the reader at the target's default position.
 *
 * The anchor rides the target URL as three query parameters. They are a
 * one-time entry point (like the existing `?turn=` permalink), so the target
 * resolves them once at mount and never reinterprets them.
 */
export const ANCHOR_ENTRY_PARAM = 'entry';
export const ANCHOR_KIND_PARAM = 'entryKind';
export const ANCHOR_REVISION_PARAM = 'entryRevision';

/** The closed set of anchor kinds that name an exact redacted-entry boundary. */
const EXACT_ANCHOR_KINDS = ['before_redacted_entry', 'through_redacted_entry'] as const;
type ExactAnchorKind = (typeof EXACT_ANCHOR_KINDS)[number];

export interface ExactRelationshipAnchor {
  kind: ExactAnchorKind;
  sourceEntryRef: string;
  sourceRevisionRef: string;
}

/**
 * The executable anchor of an authorized navigation, or null when the
 * navigation is only a general current-target link. A `general_link_only`
 * or `started_by` navigation carries no entry boundary and stays general.
 */
export function exactRelationshipAnchor(
  navigation: SessionRelationshipNavigation,
): ExactRelationshipAnchor | null {
  if (navigation.status !== 'resolved') return null;
  const anchor = navigation.anchor;
  if (!anchor) return null;
  if (!EXACT_ANCHOR_KINDS.includes(anchor.kind as ExactAnchorKind)) return null;
  if (!anchor.sourceEntryRef || !anchor.sourceRevisionRef) return null;
  return {
    kind: anchor.kind as ExactAnchorKind,
    sourceEntryRef: anchor.sourceEntryRef,
    sourceRevisionRef: anchor.sourceRevisionRef,
  };
}

/**
 * The router href for following a relationship link: the CURRENT target route
 * (`/transcripts/{id}`), plus the exact branch-point anchor when the
 * navigation carries one. Returns null when the navigation names no target.
 */
export function relationshipHref(navigation: SessionRelationshipNavigation): string | null {
  if (!navigation.transcriptId) return null;
  const base = `/transcripts/${navigation.transcriptId}`;
  const anchor = exactRelationshipAnchor(navigation);
  if (!anchor) return base;
  const params = new URLSearchParams();
  params.set(ANCHOR_ENTRY_PARAM, anchor.sourceEntryRef);
  params.set(ANCHOR_KIND_PARAM, anchor.kind);
  params.set(ANCHOR_REVISION_PARAM, anchor.sourceRevisionRef);
  return `${base}?${params.toString()}`;
}

/**
 * Read the carried anchor on the target route, accepted only when the target's
 * CURRENT public representation revision still matches the revision the
 * child's authorized navigation carried. A missing/short shape, a non-exact
 * kind, or a changed target revision yields null, so the link stays the
 * ordinary current target. The comparison mirrors the server's exact-anchor
 * authority: the revision is the digest of the target's redacted public bytes,
 * never a raw native hash.
 */
export function readCarriedRelationshipAnchor(
  search: string,
  targetContentHash: string | null | undefined,
): ExactRelationshipAnchor | null {
  const params = new URLSearchParams(search);
  const entry = params.get(ANCHOR_ENTRY_PARAM);
  const kind = params.get(ANCHOR_KIND_PARAM);
  const revision = params.get(ANCHOR_REVISION_PARAM);
  if (!entry || !kind || !revision) return null;
  if (!EXACT_ANCHOR_KINDS.includes(kind as ExactAnchorKind)) return null;
  if (!targetContentHash || revision !== targetContentHash) return null;
  return { kind: kind as ExactAnchorKind, sourceEntryRef: entry, sourceRevisionRef: revision };
}

/**
 * The minimal cooked-turn shape the anchor resolves against. Fairtrade's
 * adapter folds the wire into rendered turns, so resolution runs against that
 * cooked mapping (each turn's `sourceEntryRef`, plus any folded call/result
 * refs) rather than the raw wire.
 */
export interface CookedTurnRef {
  index: number;
  sourceEntryRef?: string;
  toolCalls?: ReadonlyArray<{ callEntryRef?: string; resultEntryRef?: string }>;
}

/**
 * Resolve the exact anchor to the turn index the reader should land on, or
 * null when the boundary entry is not rendered (fail closed: the reader sees
 * the ordinary current target, never a guessed position).
 */
export function resolveRelationshipAnchorTurn(
  turns: readonly CookedTurnRef[],
  anchor: ExactRelationshipAnchor,
): number | null {
  for (const turn of turns) {
    if (turn.sourceEntryRef === anchor.sourceEntryRef) return turn.index;
    if (
      turn.toolCalls?.some(
        (call) =>
          call.callEntryRef === anchor.sourceEntryRef ||
          call.resultEntryRef === anchor.sourceEntryRef,
      )
    ) {
      return turn.index;
    }
  }
  return null;
}
