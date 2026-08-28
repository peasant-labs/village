import { waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderAppRoute,
} from "@/test/mountedHomeRoute";
import { loadHomePageFixtures } from "@/test/homePageFixtures";

// Mounts the REAL production routes: the root route (`/`), which serves the
// signed-in person's home page or the public discovery list depending on who
// is asking, and the explore route (`/explore`). Every assertion is on what
// lands in the DOM and on the requests the routes actually issue, so a
// regression cannot hide behind a prop snapshot.

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn() }),
}));

const fixtures = loadHomePageFixtures();

installHomeRouteTeardown();

function homeSurface(): Element | null {
  return document.querySelector('[data-testid="home-page"]');
}

function exploreSurface(): Element | null {
  return document.querySelector('[data-testid="session-list-results"]');
}

async function settled(): Promise<void> {
  await waitFor(() =>
    expect(document.querySelector('[data-testid="root-route-pending"]')).toBeNull(),
  );
  await waitFor(() => expect(homeSurface() ?? exploreSurface()).not.toBeNull());
}

describe("mounted routes: which surface each visitor lands on", () => {
  for (const c of fixtures.routeCases) {
    it(c.name, async () => {
      installHomeRouteREST({ viewerUsername: c.viewerUsername, transcripts: [] });
      await renderAppRoute(c.path);
      await settled();

      expect(homeSurface() !== null).toBe(c.expectSurface === "home");
      expect(exploreSurface() !== null).toBe(c.expectSurface === "explore");
    });
  }

});

describe("mounted home route: recent sessions, then projects", () => {
  for (const c of fixtures.homeCases) {
    it(c.name, async () => {
      const requested = installHomeRouteREST({
        viewerUsername: c.viewerUsername,
        transcripts: c.transcripts,
      });
      await renderAppRoute("/");
      await settled();

      const home = homeSurface();
      expect(home).not.toBeNull();

      // The page reads the viewer's OWN transcripts. A request without the
      // owner filter would list the whole commons on a page titled "your".
      const listRequests = requested.filter((p) => p.startsWith("/transcripts"));
      expect(listRequests.length).toBeGreaterThan(0);
      for (const p of listRequests) {
        const query = new URLSearchParams(p.slice(p.indexOf("?") + 1));
        expect(query.get("owner")).toBe(c.viewerUsername);
      }

      const empty = document.querySelector('[data-testid="home-empty-state"]');
      expect(empty !== null).toBe(c.expectEmptyState);

      const recentSection = document.querySelector('[data-testid="home-recent-sessions"]');
      if (c.expectEmptyState) {
        expect(recentSection).toBeNull();
        expect(document.querySelector('[data-testid="home-projects"]')).toBeNull();
        return;
      }

      const recentTitles = [...recentSection!.querySelectorAll("a[aria-label]")].map((a) =>
        (a.getAttribute("aria-label") ?? "").replace(/^Open transcript /, ""),
      );
      expect(recentTitles).toEqual(c.expectRecentTitles);

      const rows = [...document.querySelectorAll('[data-testid="home-project-row"]')];
      expect(rows.map((r) => r.getAttribute("href"))).toEqual(
        c.expectProjectRows.map((r) => r.href),
      );
      expect(
        rows.map((r) => [...r.children].map((child) => (child.textContent ?? "").trim())),
      ).toEqual(
        c.expectProjectRows.map((r) => [
          r.displayName,
          `${r.sessionCount} session${r.sessionCount !== 1 ? "s" : ""}`,
        ]),
      );

      // Recent sessions come FIRST. Order is part of the acceptance, and a
      // page that rendered both sections in the other order would otherwise
      // satisfy every assertion above.
      const sections = [...home!.querySelectorAll("[data-testid]")]
        .map((e) => e.getAttribute("data-testid"))
        .filter((id) => id === "home-recent-sessions" || id === "home-projects");
      expect(sections).toEqual(["home-recent-sessions", "home-projects"]);
    });
  }
});
