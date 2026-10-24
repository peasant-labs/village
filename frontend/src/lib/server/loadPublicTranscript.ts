import "server-only";

import { parseTranscriptResponseText } from "@/lib/queries/transcriptContent";
import type { Transcript, TranscriptDetailResponse } from "@/lib/types";

import { shareCardEnv } from "./shareCardEnv";

export interface LoadDeps {
  fetch?: typeof fetch;
  /** Aborts the reads when the caller's overall deadline passes. */
  signal?: AbortSignal;
}

function anonymousInit(signal?: AbortSignal): RequestInit {
  return {
    cache: "no-store",
    credentials: "omit",
    headers: { Accept: "application/json" },
    signal,
  };
}

/**
 * Read a transcript's metadata as an anonymous visitor. Returns `null` for
 * anything that is not readable as public: private, shared, missing, an API
 * failure or an abort all look the same, so the result never reveals whether
 * an id exists. Never cached, so a transcript made private stops resolving on
 * the next call. No request carries credentials: visibility is decided by the
 * API answering an anonymous read.
 */
export async function readPublicTranscript(id: string, deps: LoadDeps = {}): Promise<Transcript | null> {
  const env = shareCardEnv();
  if (!env) return null;
  const doFetch = deps.fetch ?? fetch;
  try {
    const res = await doFetch(`${env.apiBase}/transcripts/${encodeURIComponent(id)}`, anonymousInit(deps.signal));
    if (!res.ok) return null;
    const detail = (await res.json()) as Partial<TranscriptDetailResponse>;
    const transcript = detail.transcript;
    if (!transcript || transcript.visibility !== "public") return null;
    return transcript;
  } catch {
    return null;
  }
}

/**
 * Read and parse a public transcript's content anonymously. Throws on any
 * failure, so a caller that caches the result never stores a failed read.
 */
export async function readPublicContent(id: string, deps: LoadDeps = {}): Promise<unknown> {
  const env = shareCardEnv();
  if (!env) throw new Error("preview environment is not configured");
  const doFetch = deps.fetch ?? fetch;
  const res = await doFetch(`${env.apiBase}/transcripts/${encodeURIComponent(id)}/content`, anonymousInit(deps.signal));
  if (!res.ok) throw new Error(`content read failed: ${res.status}`);
  return parseTranscriptResponseText(await res.text());
}
