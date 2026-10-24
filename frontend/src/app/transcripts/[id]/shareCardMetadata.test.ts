import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import YAML from "yaml";
import { describe, expect, it } from "vitest";
import {
  SHARE_CARD_HEIGHTS,
  SHARE_CARD_WIDTH,
  shareCardAlt,
  shareCardDescription,
  type ShareCardData,
} from "@peasant-labs/fairtrade/share";

import { SITE_METADATA } from "@/app/siteMetadata";

import { shareCardMetadata } from "./shareCardMetadata";

interface Case {
  name: string;
  id: string;
  card: ShareCardData | null;
  expect: "public" | "generic";
  title?: string;
  descriptionContains?: string;
  sameAs?: string;
}

const cases = (
  YAML.parse(readFileSync(resolve(process.cwd(), "testdata/share-card/metadata-cases.yaml"), "utf8"), { merge: true }) as {
    cases: Case[];
  }
).cases;

const REQUIRED = [
  "public",
  "public-no-figures",
  "generic",
  "quotes-and-newlines",
  "inner-newlines-collapse",
  "no-middle-dot",
  "id-is-path-escaped",
];

/** Every string anywhere in a value. */
function strings(value: unknown): string[] {
  if (typeof value === "string") return [value];
  if (Array.isArray(value)) return value.flatMap(strings);
  if (value && typeof value === "object") return Object.values(value).flatMap(strings);
  return [];
}

interface Image {
  url: string;
  width: number;
  height: number;
  type: string;
  alt: string;
}

describe("shareCardMetadata fixtures", () => {
  it("keeps every required case", () => {
    const names = new Set(cases.map((c) => c.name));
    for (const name of REQUIRED) expect(names, name).toContain(name);
  });

  for (const c of cases) {
    it(c.name, () => {
      const metadata = shareCardMetadata(c.card, c.id);
      const all = strings(metadata);

      for (const s of all) {
        expect(s, "one line").not.toMatch(/[\r\n]/);
        expect(s, "no middle dot").not.toContain("·");
      }

      if (c.expect === "generic") {
        // The site's own title and description, with only the card size added.
        expect(metadata).toEqual({
          ...SITE_METADATA,
          twitter: { ...SITE_METADATA.twitter, card: "summary_large_image" },
        });
        expect(metadata.openGraph).not.toHaveProperty("images");
        expect(metadata.twitter).not.toHaveProperty("images");
        if (c.sameAs) expect(shareCardMetadata(null, c.sameAs)).toEqual(metadata);
        expect(all.join("\n")).not.toContain(c.id);
        return;
      }

      const card = c.card!;
      const title = c.title ?? card.title;
      // The metadata carries fairtrade's copy for the card as drawn on one line.
      const oneLine = { ...card, title, chip: card.chip.replace(/\s+/g, " ").trim() };
      const description = shareCardDescription(oneLine);
      const alt = shareCardAlt(oneLine);

      expect(metadata.title).toBe(title);
      expect(metadata.description).toBe(description);
      expect(metadata.openGraph?.title).toBe(title);
      expect(metadata.openGraph?.description).toBe(description);
      expect(metadata.twitter?.title).toBe(title);
      expect(metadata.twitter?.description).toBe(description);
      expect((metadata.twitter as { card?: string } | undefined)?.card).toBe("summary_large_image");
      if (c.descriptionContains) expect(metadata.description).toContain(c.descriptionContains);

      const path = `/transcripts/${encodeURIComponent(c.id)}`;
      const og = metadata.openGraph?.images as Image[];
      const tw = metadata.twitter?.images as Image[];
      expect(og).toEqual([
        { url: `${path}/opengraph-image`, width: SHARE_CARD_WIDTH, height: 630, type: "image/png", alt },
      ]);
      expect(tw).toEqual([
        { url: `${path}/twitter-image`, width: SHARE_CARD_WIDTH, height: 600, type: "image/png", alt },
      ]);
      expect(SHARE_CARD_HEIGHTS).toEqual([600, 630]);
    });
  }
});
