import "server-only";

import { shareCardFromPayload, type ShareCardData } from "@peasant-labs/fairtrade/share";
import { unstable_cache } from "next/cache";

import { shownTranscriptTitle } from "@/components/session-detail/v2/transcriptChrome";

import { readPublicContent, readPublicTranscript, type LoadDeps } from "./loadPublicTranscript";

/** One deadline for a caller's whole lookup (metadata, content and mapping), so a slow API never stalls a crawler. */
export const SHARE_CARD_DEADLINE_MS = 3000;

/**
 * The bound on one cache fill (the content read and its mapping). It is the
 * fill's own and longer than a caller's deadline, so a cold read that is slow
 * but finishes is still cached for the next caller.
 */
export const SHARE_CARD_FILL_TIMEOUT_MS = 25_000;

/**
 * Cache fills that are already running, by transcript id and `updated_at`.
 * Concurrent cold requests for one transcript share one content read. A fill
 * runs on its own timeout, not on any caller's deadline: each caller waits for
 * it only until its own deadline, and the entry stays until the fill settles,
 * so a caller arriving meanwhile joins it instead of starting another read.
 */
const inFlight = new Map<string, Promise<ShareCardData>>();

/**
 * The most of the first prompt that is stored. This equals the head fairtrade's
 * `ShareCard` measures (it never renders past the first 2000 UTF-16 code units),
 * so the cap cannot change what the card shows. It bounds what the data cache
 * stores and is not a display rule.
 */
export const STORED_FIRST_PROMPT_UNITS = 2000;

/** Cut to the cap without splitting a surrogate pair. */
function capFirstPrompt(text: string): string {
  if (text.length <= STORED_FIRST_PROMPT_UNITS) return text;
  let end = STORED_FIRST_PROMPT_UNITS;
  const last = text.charCodeAt(end - 1);
  if (last >= 0xd800 && last <= 0xdbff) end -= 1;
  return text.slice(0, end);
}

class CardUnavailable extends Error {}

/**
 * The share-card data for a public transcript, or `null` for anything else:
 * private, shared, missing, unparseable, a missing `updated_at`, an API failure
 * or a lookup that outlasts the deadline. All of them look the same, so the
 * result never reveals whether an id exists.
 *
 * The metadata read runs first on every call and is never cached, so a
 * transcript made private stops resolving at once. Only the small card is
 * cached, keyed by id and `updated_at`; the transcript content is never stored.
 *
 * The card's title is the one the transcript page shows, taken from that
 * uncached metadata read, so a renamed transcript unfurls with its new title
 * at once even when `updated_at` did not change.
 */
export async function loadShareCard(id: string, deps: Pick<LoadDeps, "fetch"> = {}): Promise<ShareCardData | null> {
  // URL resolution reads "." and ".." as path steps even when encoded, so the
  // read would reach another API route. No transcript has such an id.
  if (id === "." || id === "..") return null;
  const controller = new AbortController();
  let timer: ReturnType<typeof setTimeout> | undefined;
  const deadline = new Promise<null>((resolve) => {
    timer = setTimeout(() => {
      controller.abort();
      resolve(null);
    }, SHARE_CARD_DEADLINE_MS);
  });
  try {
    return await Promise.race([lookup(id, { ...deps, signal: controller.signal }), deadline]);
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

async function lookup(id: string, deps: LoadDeps): Promise<ShareCardData | null> {
  const transcript = await readPublicTranscript(id, deps);
  if (!transcript) return null;
  const updatedAt: unknown = transcript.updated_at;
  if (typeof updatedAt !== "string" || updatedAt === "") return null;

  const key = `${id}\u0000${updatedAt}`;
  let pending = inFlight.get(key);
  if (!pending) {
    pending = fill(id, updatedAt, deps.fetch).finally(() => inFlight.delete(key));
    // A fill whose callers have all given up still settles quietly.
    pending.catch(() => {});
    inFlight.set(key, pending);
  }
  let card: ShareCardData;
  try {
    card = await pending;
  } catch {
    return null;
  }
  return withStoredTitle(card, transcript.title);
}

/**
 * Read the content and cache its card under id and `updated_at`, bounded by
 * the fill's own timeout. Throws on any failure, which keeps it out of the cache.
 */
async function fill(id: string, updatedAt: string, doFetch: LoadDeps["fetch"]): Promise<ShareCardData> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), SHARE_CARD_FILL_TIMEOUT_MS);
  const build = unstable_cache(
    async (): Promise<ShareCardData> => {
      const card = shareCardFromPayload(await readPublicContent(id, { fetch: doFetch, signal: controller.signal }));
      if (!card) throw new CardUnavailable("content did not map to a share card");
      return { ...card, firstPrompt: capFirstPrompt(card.firstPrompt) };
    },
    ["share-card", id, updatedAt],
  );
  try {
    return await build();
  } finally {
    clearTimeout(timer);
  }
}

/** The card with the page's title; a title standing in for the first prompt follows it. */
function withStoredTitle(card: ShareCardData, storedTitle: string | null | undefined): ShareCardData {
  const title = shownTranscriptTitle(storedTitle);
  return { ...card, title, ...(card.firstPromptIsTitle ? { firstPrompt: title } : {}) };
}
