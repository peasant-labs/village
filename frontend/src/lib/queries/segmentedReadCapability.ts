import {
  parseSegmentedReadVersionAdvertisement,
  SegmentedReadAPIVersion,
} from "@peasant-labs/schema";
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
