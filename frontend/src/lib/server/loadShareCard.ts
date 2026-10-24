import "server-only";

import { shareCardFromPayload, type ShareCardData } from "@peasant-labs/fairtrade/share";
import { unstable_cache } from "next/cache";

import { readPublicContent, readPublicTranscript, type LoadDeps } from "./loadPublicTranscript";

/** One deadline for the whole lookup (metadata, content and mapping), so a slow API never stalls a crawler. */
export const SHARE_CARD_DEADLINE_MS = 3000;

/**
 * Card reads that are already running, by transcript id and `updated_at`.
 * Concurrent cold requests for one transcript share one content read.
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
  // A joining caller shares the first caller's deadline and fails closed with it.
  let pending = inFlight.get(key);
  if (!pending) {
    const build = unstable_cache(
      async (): Promise<ShareCardData> => {
        const card = shareCardFromPayload(await readPublicContent(id, deps));
        // Throwing keeps an unusable result out of the cache.
        if (!card) throw new CardUnavailable("content did not map to a share card");
        return { ...card, firstPrompt: capFirstPrompt(card.firstPrompt) };
      },
      ["share-card", id, updatedAt],
    );
    pending = build().finally(() => inFlight.delete(key));
    inFlight.set(key, pending);
  }
  try {
    return await pending;
  } catch {
    return null;
  }
}
