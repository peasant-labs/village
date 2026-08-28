import { describe, expect, it } from "vitest";
import { isSectionActive, navSections } from "@/lib/nav/sections";
import { loadHomePageFixtures } from "@/test/homePageFixtures";

// The top nav is the only place a visitor can see WHERE they are. Discovery
// moved off `/`, so which entry is offered and which one is marked active is
// the part of that move most likely to regress without anybody noticing.

const fixtures = loadHomePageFixtures();

describe("top nav: which entries are offered, and which one is active", () => {
  for (const c of fixtures.navCases) {
    it(c.name, () => {
      const sections = navSections({
        isLoggedIn: c.isLoggedIn,
        githubUsername: c.isLoggedIn ? "alice-dev" : undefined,
      });

      expect(sections.map((s) => s.label)).toEqual(c.expectLabels);

      const active = sections.filter((s) => isSectionActive(s, c.pathname));
      // Exactly one entry may be highlighted: two would leave a visitor unable
      // to tell which section they are in.
      expect(active.map((s) => s.label)).toEqual([c.expectActiveLabel]);
    });
  }
});
