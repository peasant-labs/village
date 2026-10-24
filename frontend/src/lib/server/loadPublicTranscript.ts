import "server-only";

import { unstable_cache } from "next/cache";

import { parseTranscriptResponseText } from "@/lib/queries/transcriptContent";
import type { Transcript, TranscriptDetailResponse } from "@/lib/types";

import { shareCardEnv } from "./shareCardEnv";

/** Bound on each API read, so a slow API never stalls a crawler. */
const READ_TIMEOUT_MS = 3000;

export interface LoadDeps {
  fetch?: typeof fetch;
  now?: () => number;
}

export interface PublicTranscript {
  transcript: Transcript;
  content: unknown;
}

/**
 * Parsed content reads that are already running, by transcript id and
 * `updated_at`. Concurrent cold requests for one transcript share one read.
 */
const inFlight = new Map<string, Promise<unknown>>();

/**
 * Read a transcript as an anonymous visitor. Returns `null` for anything that
 * is not readable as public: private, shared, missing, an API failure or a
 * timeout all look the same, so the result never reveals whether an id exists.
 *
 * The metadata read is never cached, so a transcript made private stops
 * resolving on the next call. Only the content is cached, keyed by id and
 * `updated_at`. No request carries credentials: visibility is decided by the
 * API answering an anonymous read.
 */
export async function loadPublicTranscript(
  id: string,
  deps: LoadDeps = {},
): Promise<PublicTranscript | null> {
  const env = shareCardEnv();
  if (!env) return null;
  const doFetch = deps.fetch ?? fetch;
  const path = `${env.apiBase}/transcripts/${encodeURIComponent(id)}`;

  try {
    const metaRes = await doFetch(path, {
      cache: "no-store",
      credentials: "omit",
      headers: { Accept: "application/json" },
      signal: AbortSignal.timeout(READ_TIMEOUT_MS),
    });
    if (!metaRes.ok) return null;
    const detail = (await metaRes.json()) as Partial<TranscriptDetailResponse>;
    const transcript = detail.transcript;
    if (!transcript || transcript.visibility !== "public") return null;

    const key = `${transcript.id}\u0000${transcript.updated_at}`;
    const read = async (): Promise<unknown> => {
      const res = await doFetch(`${path}/content`, {
        cache: "no-store",
        credentials: "omit",
        headers: { Accept: "application/json" },
        signal: AbortSignal.timeout(READ_TIMEOUT_MS),
      });
      if (!res.ok) throw new Error(`content read failed: ${res.status}`);
      return parseTranscriptResponseText(await res.text());
    };
    // A failed read throws, which the cache does not store.
    const cached = unstable_cache(read, ["public-transcript-content", transcript.id, transcript.updated_at]);

    let pending = inFlight.get(key);
    if (!pending) {
      pending = cached().finally(() => inFlight.delete(key));
      inFlight.set(key, pending);
    }
    return { transcript, content: await pending };
  } catch {
    return null;
  }
}
