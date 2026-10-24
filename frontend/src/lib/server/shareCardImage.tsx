import "server-only";

import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";

import { ShareCard, SHARE_CARD_FONTS, SHARE_CARD_WIDTH, type ShareCardData } from "@peasant-labs/fairtrade/share";
import { ImageResponse } from "next/og";

import { loadShareCard } from "./loadShareCard";

type Height = 600 | 630;
type Font = NonNullable<ConstructorParameters<typeof ImageResponse>[1]>["fonts"];

/**
 * Longest a render may take before the generic card is served instead. Glyphs
 * outside the card's fonts (CJK, emoji) make next/og fetch extra assets while
 * drawing; this keeps a slow fetch from stalling a crawler.
 */
const RENDER_DEADLINE_MS = 4000;

let fonts: Promise<Font> | undefined;

/**
 * The card's faces, read once per process from the fairtrade package. A
 * missing file is logged and the card falls back to next/og's built-in face
 * rather than failing the response.
 */
function shareCardFonts(): Promise<Font> {
  fonts ??= (async () => {
    const require = createRequire(path.join(process.cwd(), "package.json"));
    return Promise.all(
      SHARE_CARD_FONTS.map(async (font) => ({
        name: font.name,
        weight: font.weight,
        style: font.style,
        data: await readFile(require.resolve(`@peasant-labs/fairtrade/share/fonts/${font.file}`)),
      })),
    );
  })().catch((error: unknown) => {
    console.error("share card: could not read the card fonts", error);
    return undefined;
  });
  return fonts;
}

async function renderPng(card: ShareCardData | null, height: Height): Promise<ArrayBuffer> {
  const response = new ImageResponse(<ShareCard card={card} height={height} />, {
    width: SHARE_CARD_WIDTH,
    height,
    fonts: await shareCardFonts(),
  });
  let timer: ReturnType<typeof setTimeout> | undefined;
  const deadline = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error("share card render timed out")), RENDER_DEADLINE_MS);
  });
  try {
    return await Promise.race([response.arrayBuffer(), deadline]);
  } finally {
    clearTimeout(timer);
  }
}

/**
 * The share image for a transcript link: the card for a public transcript and
 * the limited-visibility card for anything else. It always answers 200 with a
 * PNG; a failed lookup or render serves the limited-visibility card. The image
 * is rendered in full before answering, so a failure can still change course.
 */
export async function shareCardImage(params: Promise<{ id: string }>, height: Height): Promise<Response> {
  let card: ShareCardData | null = null;
  try {
    card = await loadShareCard((await params).id);
  } catch {
    card = null;
  }
  let png: ArrayBuffer;
  try {
    png = await renderPng(card, height);
  } catch (error) {
    if (card) console.error("share card: render failed, serving the generic card", error);
    png = await renderPng(null, height);
  }
  return new Response(png, {
    headers: {
      "content-type": "image/png",
      // Visibility is checked on every request, so no copy may be reused.
      "cache-control": "no-store",
    },
  });
}
