import { SHARE_CARD_HEIGHTS, SHARE_CARD_WIDTH } from "@peasant-labs/fairtrade/share";

import { shareCardImage } from "@/lib/server/shareCardImage";

import { GENERIC_SHARE_CARD_ALT } from "./shareCardMetadata";

// Every platform but X: 1200x630.
// A public transcript's metadata names this image with its own alt text; this
// alt is used only when the page falls back to the site's metadata.
export const alt = GENERIC_SHARE_CARD_ALT;
export const size = { width: SHARE_CARD_WIDTH, height: SHARE_CARD_HEIGHTS[1] };
export const contentType = "image/png";
export const dynamic = "force-dynamic";

export default function Image({ params }: { params: Promise<{ id: string }> }) {
  return shareCardImage(params, size.height);
}
