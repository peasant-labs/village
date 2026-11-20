import {
  parseSegmentedReadVersionAdvertisement,
  SegmentedReadAPIVersion,
} from "@peasant-labs/schema";
import { useQuery } from "@tanstack/react-query";
import { API_URL_BASE } from "../api";

/** The choice is made before either reader is mounted. */
export const TranscriptReadMode = Object.freeze({
  Positioned: "positioned",
  Content: "content",
} as const);

export type TranscriptReadMode = (typeof TranscriptReadMode)[keyof typeof TranscriptReadMode];

/**
 * How long the page waits for the version read before it reads the whole
 * document instead. The page shows nothing until a reader is chosen, so a
 * stalled read must not hold it; five seconds is the generic bound, longer
 * than any healthy answer and short enough that a reader is not left waiting.
 */
export const SEGMENTED_READ_DISCOVERY_TIMEOUT_MS = 5_000;

/**
 * A failed discovery read does not establish support for positioned reads.
 * Once selected, a reader's own failures must not trigger another mode choice.
 * A read that has not answered by `deadline` (by default
 * SEGMENTED_READ_DISCOVERY_TIMEOUT_MS from the call) counts as failed.
 */
export async function segmentedReadCapability(
  signal?: AbortSignal,
  deadline: AbortSignal = AbortSignal.timeout(SEGMENTED_READ_DISCOVERY_TIMEOUT_MS),
): Promise<TranscriptReadMode> {
  try {
    const response = await fetch(`${API_URL_BASE}/schema/version`, {
      cache: "no-store",
      signal: AbortSignal.any(signal ? [signal, deadline] : [deadline]),
    });
    if (!response.ok) return TranscriptReadMode.Content;
    const window = parseSegmentedReadVersionAdvertisement(await response.text());
    return window?.current === SegmentedReadAPIVersion
      ? TranscriptReadMode.Positioned
      : TranscriptReadMode.Content;
  } catch {
    return TranscriptReadMode.Content;
  }
}

/**
 * The page's mode choice, asked once per browser session. The answer is held
 * for the session: a reader is chosen before it mounts and never switched.
 */
export function useSegmentedReadCapability(enabled = true) {
  return useQuery({
    queryKey: ["segmented-read-capability"],
    queryFn: ({ signal }) => segmentedReadCapability(signal),
    staleTime: Infinity,
    gcTime: Infinity,
    retry: false,
    enabled,
  });
}
