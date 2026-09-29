import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderHeaderAt,
} from "@/test/mountedHomeRoute";
import { loadHomePageFixtures } from "@/test/homePageFixtures";

// Mounts the REAL header (`Navbar`) inside the real `AuthProvider`, with the
// session answered by the same `GET /auth/me` the app calls. The nav is the
// only place a visitor can see WHERE they are, so which entries it offers and
// which one it marks active is asserted on the rendered links, not on the
// registry the header reads.

const fixtures = loadHomePageFixtures();

installHomeRouteTeardown();

function mainNav(): HTMLElement | null {
  return screen.queryByRole("navigation", { name: "main navigation" });
}

async function headerSettled(isLoggedIn: boolean): Promise<void> {
  // The header shows a placeholder until the session is known; the account
  // trigger (signed in) or the privacy link with no placeholder (signed out)
  // means it has settled.
  await waitFor(() => {
    expect(document.querySelector("header .animate-shimmer")).toBeNull();
    if (isLoggedIn) expect(screen.getByTestId("account-menu-trigger")).toBeInTheDocument();
  });
}

describe("mounted header: which nav entries are offered, and which one is active", () => {
  for (const c of fixtures.navCases) {
    it(c.name, async () => {
      installHomeRouteREST({ viewerUsername: c.isLoggedIn ? "alice-dev" : null, transcripts: [] });
      await renderHeaderAt(c.pathname);
      await headerSettled(c.isLoggedIn);

      const nav = mainNav();
      if (c.expectLabels.length === 0) {
        // No entries means no nav landmark at all, rather than an empty one a
        // screen reader would still announce.
        expect(nav).toBeNull();
        return;
      }
      expect(nav).not.toBeNull();
      const links = within(nav!).getAllByRole("link");
      expect(links.map((a) => a.textContent)).toEqual(c.expectLabels);

      // At most one entry may be highlighted: two would leave a visitor unable
      // to tell which section they are in.
      const active = links.filter((a) => a.getAttribute("aria-current") === "page");
      expect(active.map((a) => a.textContent)).toEqual(
        c.expectActiveLabel === null ? [] : [c.expectActiveLabel],
      );
    });
  }
});

describe("mounted header: the account menu", () => {
  it("names the signed-in person and holds profile and sign out", async () => {
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    await renderHeaderAt("/");
    await headerSettled(true);

    const trigger = screen.getByRole("button", { name: "account menu for @alice-dev" });
    expect(trigger).toHaveAttribute("aria-haspopup", "menu");
    expect(trigger).toHaveAttribute("aria-expanded", "false");

    fireEvent.click(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    const menu = screen.getByRole("menu");
    expect(within(menu).getAllByRole("menuitem").map((i) => i.textContent)).toEqual([
      "profile",
      "sign out",
    ]);
  });
});
