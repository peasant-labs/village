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
 * A failed discovery read does not establish support for positioned reads.
 * Once selected, a reader's own failures must not trigger another mode choice.
 */
export async function segmentedReadCapability(signal?: AbortSignal): Promise<TranscriptReadMode> {
  try {
    const response = await fetch(`${API_URL_BASE}/schema/version`, { cache: "no-store", signal });
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
