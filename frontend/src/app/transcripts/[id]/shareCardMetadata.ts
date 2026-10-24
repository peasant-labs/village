import type { Metadata } from "next";
import {
  SHARE_CARD_HEIGHTS,
  SHARE_CARD_WIDTH,
  shareCardAlt,
  shareCardDescription,
  type ShareCardData,
} from "@peasant-labs/fairtrade/share";

import { SITE_METADATA } from "@/app/siteMetadata";

/** The image alt text for the limited-visibility card. */
export const GENERIC_SHARE_CARD_ALT = "peasant labs share card, limited visibility";

const [X_HEIGHT, OPEN_GRAPH_HEIGHT] = SHARE_CARD_HEIGHTS;

/**
 * The site's own metadata, with X told to draw the large card: the segment's
 * image routes supply the limited-visibility image for it.
 */
const GENERIC_METADATA: Metadata = {
  ...SITE_METADATA,
  twitter: { ...SITE_METADATA.twitter, card: "summary_large_image" },
};

/** Whitespace runs, line breaks included, become one space. */
function oneLine(text: string): string {
  return text.replace(/\s+/g, " ").trim();
}

/**
 * The page metadata for a transcript link. A public transcript (a card) gets
 * its title, fairtrade's description and alt text, and both share images; any
 * other transcript (`null`) gets the site's own title and description, the same
 * whatever the id, so a private id and a missing one look the same. The generic
 * images come from the segment's image routes, whose alt is GENERIC_SHARE_CARD_ALT.
 */
export function shareCardMetadata(card: ShareCardData | null, id: string): Metadata {
  if (!card) return GENERIC_METADATA;

  // A crawler reads each field as one line, so the title and chip are put on
  // one line before fairtrade's copy is built from them.
  const shown: ShareCardData = { ...card, title: oneLine(card.title), chip: oneLine(card.chip) };
  const title = shown.title;
  const description = oneLine(shareCardDescription(shown));
  const alt = oneLine(shareCardAlt(shown));
  const path = `/transcripts/${encodeURIComponent(id)}`;
  const image = (name: string, height: number) => ({
    url: `${path}/${name}`,
    width: SHARE_CARD_WIDTH,
    height,
    type: "image/png",
    alt,
  });

  return {
    title,
    description,
    openGraph: { title, description, images: [image("opengraph-image", OPEN_GRAPH_HEIGHT)] },
    twitter: { card: "summary_large_image", title, description, images: [image("twitter-image", X_HEIGHT)] },
  };
}
