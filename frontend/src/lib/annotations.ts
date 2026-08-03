import type { TurnLabel } from "@peasant-labs/transcript-browser";
import type { AnnotationSummary } from "@peasant-labs/schema";

export type { AnnotationSummary };

/** GET .../annotations response envelope. */
export interface ListAnnotationsResponse {
  annotations: AnnotationSummary[];
}

/**
 * A single permissible value for a manual-label type. Mirrors the shared
 * annotation value-domain concept without pulling in the whole peasant schema.
 */
export interface AnnotationTypeValue {
  value: string;
  label: string;
}

/** A manual-label type the village exposes in the per-turn label popover. */
export interface AnnotationType {
  typeId: string;
  typeName: string;
  /** Enumerated permissible values shown as choices in the popover. */
  values: AnnotationTypeValue[];
}

/**
 * The village's manual-label type registry. The backend stores `typeId`; the
 * display name (`typeName`) is purely a frontend concern (the GET echoes the raw
 * `typeId` as `typeName`), so the canonical names live here.
 *
 * This is intentionally a small, curated set of per-turn labels a human reviewer
 * applies while reading a transcript. Keep it in sync with the peasant annotation
 * type catalog as it stabilises.
 */
export const ANNOTATION_TYPES: AnnotationType[] = [
  {
    typeId: "turn_outcome",
    typeName: "Outcome",
    values: [
      { value: "good", label: "Good" },
      { value: "neutral", label: "Neutral" },
      { value: "bad", label: "Bad" },
    ],
  },
  {
    typeId: "turn_flag",
    typeName: "Flag",
    values: [
      { value: "error", label: "Error" },
      { value: "retry", label: "Retry loop" },
      { value: "revert", label: "Revert" },
      { value: "highlight", label: "Highlight" },
    ],
  },
];

const TYPE_BY_ID = new Map(ANNOTATION_TYPES.map((t) => [t.typeId, t]));

/**
 * Resolve a display name for an annotation type id. Falls back to the wire
 * `typeName` (which the backend sets to the raw `typeId`), then the id itself.
 */
export function annotationTypeName(typeId: string, wireTypeName?: string): string {
  return TYPE_BY_ID.get(typeId)?.typeName ?? wireTypeName ?? typeId;
}

/** Resolve a display label for a (typeId, value) pair. */
export function annotationValueLabel(typeId: string, value: string): string {
  const t = TYPE_BY_ID.get(typeId);
  return t?.values.find((v) => v.value === value)?.label ?? value;
}

/**
 * Map a wire `AnnotationSummary` to the package's framework-agnostic `TurnLabel`.
 * Only entry-level annotations carry a turn index; session/project-level rows are
 * dropped (returns `null`).
 */
export function summaryToTurnLabel(a: AnnotationSummary): TurnLabel | null {
  if (a.targetEntryIndex == null) return null;
  return {
    entryIndex: a.targetEntryIndex,
    typeId: a.typeId,
    typeName: annotationTypeName(a.typeId, a.typeName),
    value: a.value,
    id: a.id,
  };
}

/**
 * Group entry-level annotations into the `Map<entryIndex, TurnLabel[]>` the
 * package's `<SessionDetail>` consumes via `savedLabelsByEntry`.
 */
export function buildSavedLabelsByEntry(
  annotations: AnnotationSummary[],
): Map<number, TurnLabel[]> {
  const byEntry = new Map<number, TurnLabel[]>();
  for (const a of annotations) {
    const label = summaryToTurnLabel(a);
    if (!label) continue;
    const existing = byEntry.get(label.entryIndex);
    if (existing) existing.push(label);
    else byEntry.set(label.entryIndex, [label]);
  }
  return byEntry;
}
