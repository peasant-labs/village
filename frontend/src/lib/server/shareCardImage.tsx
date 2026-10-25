import "server-only";

import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { crc32, deflateSync } from "node:zlib";

import { ShareCard, SHARE_CARD_FONTS, SHARE_CARD_WIDTH, type ShareCardData } from "@peasant-labs/fairtrade/share";
import tokens from "@peasant-labs/fairtrade/tokens.json";
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
 * The card's faces, read from the fairtrade package once per process. A read
 * that fails is logged and forgotten, so the next request tries again; this
 * request draws with next/og's built-in face rather than failing.
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
    fonts = undefined;
    return undefined;
  });
  return fonts;
}

async function renderPng(card: ShareCardData | null, height: Height): Promise<{ png: ArrayBuffer; withFonts: boolean }> {
  const faces = await shareCardFonts();
  const response = new ImageResponse(<ShareCard card={card} height={height} />, {
    width: SHARE_CARD_WIDTH,
    height,
    fonts: faces,
  });
  let timer: ReturnType<typeof setTimeout> | undefined;
  const deadline = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error("share card render timed out")), RENDER_DEADLINE_MS);
  });
  try {
    return { png: await Promise.race([response.arrayBuffer(), deadline]), withFonts: faces !== undefined };
  } finally {
    clearTimeout(timer);
  }
}

/** The limited-visibility card, kept per height once it has rendered with the card's fonts. */
const genericKept = new Map<Height, ArrayBuffer>();

async function genericPng(height: Height): Promise<Uint8Array | ArrayBuffer> {
  const kept = genericKept.get(height);
  if (kept) return kept;
  try {
    const { png, withFonts } = await renderPng(null, height);
    if (withFonts) genericKept.set(height, png);
    return png;
  } catch (error) {
    console.error("share card: the generic card did not render, serving a plain image", error);
    return plainPng(SHARE_CARD_WIDTH, height, tokens.color.surface.$value);
  }
}

function chunk(type: string, data: Uint8Array): Buffer {
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length);
  const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body));
  return Buffer.concat([length, body, crc]);
}

/**
 * The last resort: a PNG of one flat colour (`#rrggbb`), drawn without the
 * renderer, so the route still answers with an image of the right size.
 */
function plainPng(width: number, height: number, hex: string): Uint8Array {
  const rgb = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
  const row = Buffer.alloc(1 + width * 3);
  for (let x = 0; x < width; x++) row.set(rgb, 1 + x * 3);
  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header.set([8, 2, 0, 0, 0], 8); // 8-bit RGB, no interlace
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(Buffer.concat(Array.from({ length: height }, () => row)))),
    chunk("IEND", new Uint8Array()),
  ]);
}

/**
 * The share image for a transcript link: the card for a public transcript and
 * the limited-visibility card for anything else. It always answers 200 with a
 * PNG. A failed lookup or card render serves the limited-visibility card, and
 * if that cannot render either, a plain image of the card's background.
 */
export async function shareCardImage(params: Promise<{ id: string }>, height: Height): Promise<Response> {
  let card: ShareCardData | null = null;
  try {
    card = await loadShareCard((await params).id);
  } catch {
    card = null;
  }
  let png: Uint8Array | ArrayBuffer | undefined;
  if (card) {
    try {
      png = (await renderPng(card, height)).png;
    } catch (error) {
      console.error("share card: render failed, serving the generic card", error);
    }
  }
  png ??= await genericPng(height);
  return new Response(png as BodyInit, {
    headers: {
      "content-type": "image/png",
      // Visibility is checked on every request, so no copy may be reused.
      "cache-control": "no-store",
      // The card shows the first prompt, so image search may not list it either.
      "x-robots-tag": "noindex",
    },
  });
}
