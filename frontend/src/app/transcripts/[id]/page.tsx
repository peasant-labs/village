import type { Metadata } from "next";
import { cookies, headers } from "next/headers";
import { redirect } from "next/navigation";

import { AUTH_COOKIE } from "@/lib/authCookieName";
import { loadShareCard } from "@/lib/server/loadShareCard";
import {
  decideTranscriptPage,
  readPublicTranscriptForRequest,
  searchString,
} from "@/lib/server/transcriptPageAccess";

import TranscriptDetailPage from "./TranscriptDetailPage";
import { shareCardMetadata } from "./shareCardMetadata";

// Visibility is checked on every request, so this route is never prerendered.
export const dynamic = "force-dynamic";

/**
 * Link-preview metadata for a transcript. Only a public transcript (read as an
 * anonymous visitor) unfurls into its card; anything else, including a failed
 * lookup, gets the site's own metadata.
 *
 * It is the page's, not a layout's: the segment's share images are attached at
 * the page level too, so only page metadata can name them with the card's alt.
 * Its metadata read is the one the page makes for the same request.
 */
export async function generateMetadata({ params }: { params: Promise<{ id: string }> }): Promise<Metadata> {
  const { id } = await params;
  let card = null;
  try {
    card = await loadShareCard(id, { readTranscript: readPublicTranscriptForRequest });
  } catch {
    card = null;
  }
  return shareCardMetadata(card, id);
}

/**
 * The transcript page. Without a session, the server decides before anything
 * is served: a public transcript is served and opens without signing in;
 * anything else sends the visitor to sign in (see `decideTranscriptPage`).
 * This runs for a page load and for a navigation inside the app alike, so a
 * signed-out reader following a link to a transcript that is not public is
 * sent to sign in from here too.
 */
export default async function Page({
  params,
  searchParams,
}: {
  params: Promise<{ id: string }>;
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const [{ id }, query, cookieStore, headerList] = await Promise.all([params, searchParams, cookies(), headers()]);
  const decision = await decideTranscriptPage({
    id,
    search: searchString(query),
    hasSession: Boolean(cookieStore.get(AUTH_COOKIE)?.value),
    fetchDest: headerList.get("sec-fetch-dest"),
  });
  if (!decision.serve) redirect(decision.destination);
  return <TranscriptDetailPage params={params} openSignedOut={decision.openSignedOut} />;
}
