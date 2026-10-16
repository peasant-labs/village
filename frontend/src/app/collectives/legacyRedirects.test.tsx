import { describe, expect, it, vi } from "vitest";
import { loadLegacyCollectiveFixtures } from "@/test/collectivesLegacyFixtures";
import list from "../groups/page";
import detail from "../groups/[id]/page";
import settings from "../groups/[id]/settings/page";
import contribute from "../groups/[id]/contribute/page";
import review from "../groups/[id]/review/page";

vi.mock("next/navigation", () => ({ redirect: (url: string) => { throw new Error(`redirect:${url}`); } }));

describe("legacy collective pages", () => {
  const pages = { list, detail, settings, contribute, review };
  for (const row of loadLegacyCollectiveFixtures()) {
    it(`redirects ${row.name} with repeated, empty and encoded query values intact`, async () => {
      const search: Record<string, string | string[]> = {};
      for (const [key, value] of new URLSearchParams(row.query)) {
        const prior = search[key];
        search[key] = prior === undefined ? value : [...(Array.isArray(prior) ? prior : [prior]), value];
      }
      await expect(pages[row.name as keyof typeof pages]({ params: Promise.resolve({ id: row.suffix.split("/")[1] }), searchParams: Promise.resolve(search) }))
        .rejects.toThrow(`redirect:/collectives${row.suffix}?${new URLSearchParams(row.query)}`);
    });
  }
});
