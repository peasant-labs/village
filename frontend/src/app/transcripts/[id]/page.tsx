import type { Metadata } from "next";

import { loadShareCard } from "@/lib/server/loadShareCard";

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
 */
export async function generateMetadata({ params }: { params: Promise<{ id: string }> }): Promise<Metadata> {
  const { id } = await params;
  let card = null;
  try {
    card = await loadShareCard(id);
  } catch {
    card = null;
  }
  return shareCardMetadata(card, id);
}

export default function Page({ params }: { params: Promise<{ id: string }> }) {
  return <TranscriptDetailPage params={params} />;
}
