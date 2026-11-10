import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { HOME_VIEW_STORAGE_KEY, LEGACY_HOME_VIEW_STORAGE_KEY } from "@/lib/homeView";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";
import type { HomeTranscriptCase } from "@/test/homePageFixtures";
import { installHomeRouteREST, installHomeRouteTeardown, renderAppRoute } from "@/test/mountedHomeRoute";

type AddressCase = {
  name: string;
  path: string;
  expectedAddress: string;
  perPage: string;
  groupLabel: string;
  legacyColumns?: Record<string, boolean>;
  expectedColumns?: string[];
};

const fixture = parse(readFileSync(resolve(process.cwd(), "src/testdata/library-address.yaml"), "utf8"), {
  strict: true,
}) as { row: HomeTranscriptCase; cases: AddressCase[] };
assertExactKeys(fixture, ["row", "cases"], "library address root");
assertNamesMatch(fixture.cases.map((c) => c.name), [
  "default-mount-keeps-the-address-clean",
  "non-default-deep-link-and-legacy-columns-survive",
], "library address cases");

installHomeRouteTeardown();

describe("mounted Library addresses", () => {
  for (const c of fixture.cases) {
    it(c.name, async () => {
      if (c.legacyColumns !== undefined) {
        localStorage.setItem(LEGACY_HOME_VIEW_STORAGE_KEY, JSON.stringify({ columns: c.legacyColumns }));
      }
      const backend = installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [fixture.row] });
      await renderAppRoute(c.path);
      await screen.findByText(fixture.row.title);
      await waitFor(() => expect(document.querySelector('[data-testid="home-list-loading"]')).toBeNull());
      const address = `${window.location.pathname}${window.location.search}${window.location.hash}`;
      expect(address).toBe(c.expectedAddress);
      expect(backend.requested.some((path) => {
        const [route, query] = path.split("?");
        return route === "/transcripts" && new URLSearchParams(query).get("per_page") === c.perPage;
      })).toBe(true);
      expect(document.querySelector('[data-testid="home-transcripts"]')?.textContent?.toLowerCase()).toContain(c.groupLabel);
      if (c.expectedColumns !== undefined) {
        expect(JSON.parse(localStorage.getItem(HOME_VIEW_STORAGE_KEY)!).columns).toEqual(c.expectedColumns);
      }
    }, 15000);
  }
});
