import { TranscriptReadMode } from "./queries/segmentedReadCapability";

/**
 * Per-transcript read state the host restores after a navigation round trip.
 *
 * Village owns URL creation and Back/scroll/query restoration (the design
 * boundary with Fairtrade). When a viewer follows a context/source link to the
 * current parent, the child route unmounts; browser Back re-mounts it with the
 * same URL but no component state. Persisting the child's transient read state
 * (search query, active turn, earlier-history disclosure, scroll offset) under
 * its own transcript id is what makes Back land the viewer where they left.
 *
 * The store is session-scoped: it survives same-tab navigation and Back, and
 * disappears when the tab closes. Storage being unavailable (private mode,
 * server render) degrades to an empty state, never an error.
 */
export interface TranscriptReadState {
  search: string;
  /** The active turn's recorded index: the index both reads share. The
   *  whole-document page's turn index is the recorded index; the positioned
   *  page saves its active row's recorded index, never its served position. */
  activeTurn: number | null;
  /** Keyed by history section (`earlier-N`), the same keys in both reads. */
  earlierHistoryOpen: Record<string, boolean>;
  /** The whole-document stream's scroll offset. A positioned page holds only
   *  some segments, so its offset means nothing to another read; it saves 0. */
  scrollTop: number;
  /** The positioned page only: how far the active row sat below the top of
   *  the stream, so a return can put it back in the same place. */
  turnOffset: number;
  /** Which read wrote the state; null for a state saved before reads were
   *  recorded, which only the whole-document page wrote. */
  readMode: TranscriptReadMode | null;
}

const STORAGE_PREFIX = "village:transcript-read-state:";

export function emptyTranscriptReadState(): TranscriptReadState {
  return { search: "", activeTurn: null, earlierHistoryOpen: {}, scrollTop: 0, turnOffset: 0, readMode: null };
}

function normalize(value: unknown): TranscriptReadState {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    return emptyTranscriptReadState();
  }
  const record = value as Record<string, unknown>;
  const activeTurn =
    typeof record.activeTurn === "number" && Number.isSafeInteger(record.activeTurn) && record.activeTurn >= 0
      ? record.activeTurn
      : null;
  const earlierHistoryOpen: Record<string, boolean> = {};
  if (record.earlierHistoryOpen !== null && typeof record.earlierHistoryOpen === "object" && !Array.isArray(record.earlierHistoryOpen)) {
    for (const [key, open] of Object.entries(record.earlierHistoryOpen as Record<string, unknown>)) {
      if (typeof open === "boolean") earlierHistoryOpen[key] = open;
    }
  }
  return {
    search: typeof record.search === "string" ? record.search : "",
    activeTurn,
    earlierHistoryOpen,
    scrollTop:
      typeof record.scrollTop === "number" && Number.isFinite(record.scrollTop) && record.scrollTop >= 0
        ? record.scrollTop
        : 0,
    turnOffset: typeof record.turnOffset === "number" && Number.isFinite(record.turnOffset) ? record.turnOffset : 0,
    readMode: Object.values(TranscriptReadMode).find((mode) => mode === record.readMode) ?? null,
  };
}

export function readTranscriptReadState(transcriptId: string): TranscriptReadState {
  if (!transcriptId || typeof window === "undefined") return emptyTranscriptReadState();
  try {
    const raw = window.sessionStorage.getItem(STORAGE_PREFIX + transcriptId);
    if (raw === null) return emptyTranscriptReadState();
    return normalize(JSON.parse(raw));
  } catch {
    return emptyTranscriptReadState();
  }
}

export function writeTranscriptReadState(transcriptId: string, state: TranscriptReadState): void {
  if (!transcriptId || typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(STORAGE_PREFIX + transcriptId, JSON.stringify(state));
  } catch {
    // A full or unavailable store must never break reading.
  }
}

export function clearTranscriptReadState(transcriptId: string): void {
  if (!transcriptId || typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(STORAGE_PREFIX + transcriptId);
  } catch {
    // Ignore.
  }
}
