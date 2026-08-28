import type { SessionDetailPayload } from "@/types/messages";

/**
 * Narrowing guard: a {@link SessionDetailPayload} always carries a `turns`
 * array. Extracted from `src/app/transcripts/[id]/page.tsx` (its original,
 * sole caller) so `TranscriptPreview.tsx` can share the exact same check
 * rather than re-deriving it — the two surfaces must agree on what counts as
 * a renderable payload.
 */
export function isSessionDetailPayload(value: unknown): value is SessionDetailPayload {
  return (
    !!value &&
    typeof value === "object" &&
    !Array.isArray(value) &&
    Array.isArray((value as { turns?: unknown }).turns)
  );
}
