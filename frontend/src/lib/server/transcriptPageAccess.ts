import "server-only";

import { cache } from "react";

import { signedOutDestination } from "@/lib/marketing";
import type { Transcript } from "@/lib/types";

import { readPublicTranscript } from "./loadPublicTranscript";
import { SHARE_CARD_DEADLINE_MS } from "./loadShareCard";

/**
 * This request's anonymous read of a transcript's metadata. React scopes the
 * memo to one server request, so the page and its link-preview metadata share
 * one API read, and the next request reads again: a transcript made private
 * stops being served at once. Bounded by the share card's deadline; a read that
 * outlasts it is `null`, like any other failure.
 */
export const readPublicTranscriptForRequest = cache(
  (id: string): Promise<Transcript | null> =>
    readPublicTranscript(id, { signal: AbortSignal.timeout(SHARE_CARD_DEADLINE_MS) }),
);

/** What the server knows about a request for a transcript page. */
export interface TranscriptPageRequest {
  /** The transcript id, decoded, as the route's params give it. */
  id: string;
  /** The request's query, with its `?`, or `""`. */
  search: string;
  /** Whether the request carries a session cookie. Only its presence is read. */
  hasSession: boolean;
  /** The browser's `Sec-Fetch-Dest`, or null for a client that sends none. */
  fetchDest: string | null;
}

export type TranscriptPageDecision =
  /** Serve the page. `openSignedOut` tells the app frame not to send a signed-out visitor to sign in. */
  | { serve: true; openSignedOut: boolean }
  /** Send the visitor to sign in, coming back to this page. */
  | { serve: false; destination: string };

/**
 * Whether a request for `/transcripts/<id>` is served, and whether a visitor
 * without a session may stay on it.
 *
 * - A request with a session cookie is served as before, with no read: the app
 *   reads the transcript as that person.
 * - Without one, the transcript is read as an anonymous visitor. A public
 *   transcript is served and marked open to a signed-out reader.
 * - Anything else (private, shared, missing, an API failure, the server's API
 *   address not configured) is sent to sign in, the same way for every case,
 *   so the answer never says which it was. A client that sends no
 *   `Sec-Fetch-Dest` (a link-preview crawler) is still served the page, unmarked:
 *   its metadata is the site's generic card, and an older browser's app frame
 *   then sends the visitor to sign in.
 */
export async function decideTranscriptPage(
  request: TranscriptPageRequest,
  read: (id: string) => Promise<Transcript | null> = readPublicTranscriptForRequest,
): Promise<TranscriptPageDecision> {
  if (request.hasSession) return { serve: true, openSignedOut: false };
  let transcript: Transcript | null = null;
  try {
    transcript = await read(request.id);
  } catch {
    transcript = null;
  }
  if (transcript?.visibility === "public") return { serve: true, openSignedOut: true };
  if (request.fetchDest === null) return { serve: true, openSignedOut: false };
  return {
    serve: false,
    destination: signedOutDestination(`/transcripts/${encodeURIComponent(request.id)}`, request.search),
  };
}

/** A page's search params as the query string `signedOutDestination` takes, with its `?`. */
export function searchString(params: Record<string, string | string[] | undefined>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    for (const one of Array.isArray(value) ? value : value === undefined ? [] : [value]) query.append(key, one);
  }
  const text = query.toString();
  return text ? `?${text}` : "";
}
