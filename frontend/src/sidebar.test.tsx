import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderHeaderAt,
} from "@/test/mountedHomeRoute";
import { loadHomePageFixtures, type HomeCollectiveCase } from "@/test/homePageFixtures";
import { pushedRoutes, resetNextNavigation } from "@/test/nextNavigationMock";
import { DOCS_URL } from "@/lib/appLinks";

// Mounts the REAL app chrome (`AppFrame`: fairtrade's sidebar for somebody
// signed in, the public bar for anybody else) inside the real `AuthProvider`,
// with the session answered by the same `GET /auth/me` the app calls. The
// sidebar is the only place a person can see WHERE they are, so which entries
// it offers and which one it marks current is asserted on the rendered links,
// not on the groups the shell hands it.

const fixtures = loadHomePageFixtures();

installHomeRouteTeardown();

/** The one collective every signed-in case belongs to. */
const ACME: HomeCollectiveCase = { id: "c011ec71-0000-4000-8000-000000000001", name: "Acme Platform", role: "owner", members: 12 };

const NAV_NAMES = { sections: "peasant sections" } as const;

async function chromeSettled(isLoggedIn: boolean): Promise<void> {
  // Signed in, the account trigger means the session has answered and the
  // sidebar has drawn; signed out, the public bar's way in.
  await waitFor(() => {
    if (isLoggedIn) expect(screen.getByTestId("account-menu-trigger")).toBeInTheDocument();
    else expect(screen.getByRole("link", { name: "sign in" })).toBeInTheDocument();
  });
}

describe("mounted sidebar: which entries are offered, and which one is current", () => {
  for (const c of fixtures.navCases) {
    it(c.name, async () => {
      installHomeRouteREST({
        viewerUsername: c.isLoggedIn ? "alice-dev" : null,
        transcripts: [],
        collectives: c.isLoggedIn ? [ACME] : [],
      });
      await renderHeaderAt(c.pathname);
      await chromeSettled(c.isLoggedIn);

      if (c.expectNav === "none") {
        // No sidebar at all, rather than an empty one a screen reader would
        // still announce; the public bar is the chrome instead.
        expect(screen.queryByRole("navigation", { name: NAV_NAMES.sections })).toBeNull();
        expect(screen.getByTestId("public-top-bar")).toBeInTheDocument();
        return;
      }
      expect(screen.queryByTestId("public-top-bar")).toBeNull();

      const nav = await screen.findByRole("navigation", { name: NAV_NAMES[c.expectNav] });
      // The person's collectives arrive with their own read.
      await waitFor(() =>
        expect(within(nav).getAllByRole("link").map((a) => a.textContent)).toEqual(c.expectLabels),
      );
      const links = within(nav).getAllByRole("link");

      // At most one entry may be current: two would leave a person unable to
      // tell which page they are on.
      const current = links.filter((a) => a.getAttribute("aria-current") === "page");
      expect(current.map((a) => a.textContent)).toEqual(
        c.expectActiveLabel === null ? [] : [c.expectActiveLabel],
      );

      // The sidebar has no way back of its own on any page.
      expect(screen.queryByRole("navigation", { name: "back" })).toBeNull();
    });
  }
});

describe("mounted sidebar: the person's collectives", () => {
  it("lists every collective the person belongs to, in the order served, each leading to its page", async () => {
    const mine: HomeCollectiveCase[] = [
      ACME,
      { id: "44444444-4444-4444-8444-444444444444", name: "Solo Circle", role: "member", members: 1 },
    ];
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [], collectives: mine });
    await renderHeaderAt("/");
    await chromeSettled(true);
    const nav = screen.getByRole("navigation", { name: NAV_NAMES.sections });
    await waitFor(() => expect(within(nav).getByRole("link", { name: "Solo Circle" })).toBeInTheDocument());
    const listed = within(nav).getAllByRole("link").slice(4);
    // User content keeps its case, and each row leads to its collective.
    expect(listed.map((a) => a.textContent)).toEqual(mine.map((c) => c.name));
    expect(listed.map((a) => a.getAttribute("href"))).toEqual(mine.map((c) => `/collectives/${c.id}`));
  });
});

describe("mounted sidebar: the other apps", () => {
  it("offers only the docs, in a new tab, and never lists this app or the local one", async () => {
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    await renderHeaderAt("/");
    await chromeSettled(true);

    const apps = screen.getByRole("navigation", { name: "apps" });
    // The sidebar is this app, so the switcher does not name it, and the local
    // app runs on the person's own computer, so it is not offered from here.
    expect(within(apps).queryByText("online")).toBeNull();
    expect(within(apps).queryByText("local")).toBeNull();
    expect(within(apps).getAllByRole("link")).toHaveLength(1);
    const docs = within(apps).getByRole("link", { name: /^docs/ });
    expect(docs).toHaveAttribute("href", DOCS_URL);
    expect(docs).toHaveAttribute("href", "https://peasantlabs.org/docs");
    expect(docs).toHaveAttribute("target", "_blank");
    expect(docs).toHaveTextContent("opens outside this app");
    // The docs are offered once, in the switcher, not again among the sections.
    expect(screen.getAllByRole("link", { name: /^docs/ })).toHaveLength(1);
  });
});

describe("mounted sidebar: the account menu", () => {
  it("names the signed-in person and holds exactly the fixture's items, in order", async () => {
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    await renderHeaderAt("/");
    await chromeSettled(true);

    const trigger = screen.getByRole("button", { name: "account menu for @alice-dev" });
    expect(trigger).toHaveAttribute("aria-haspopup", "menu");
    expect(trigger).toHaveAttribute("aria-expanded", "false");

    fireEvent.click(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    const menu = screen.getByRole("menu");
    expect(within(menu).getAllByRole("menuitem").map((i) => i.textContent)).toEqual(
      fixtures.accountMenuCases.map((c) => c.item),
    );
  });

  // The menu is the only way to the person's own profile and the chrome's only
  // way to sign out, so each item is asserted by what choosing it DOES.
  for (const c of fixtures.accountMenuCases) {
    it(c.name, async () => {
      resetNextNavigation();
      const backend = installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
      await renderHeaderAt("/");
      await chromeSettled(true);

      fireEvent.click(screen.getByRole("button", { name: "account menu for @alice-dev" }));
      fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: c.item }));

      if (c.effect === "navigate") {
        expect(pushedRoutes).toEqual([c.target]);
        expect(backend.logouts).toBe(0);
      } else {
        await waitFor(() => expect(backend.logouts).toBe(1));
        expect(pushedRoutes).toEqual([]);
      }
    });
  }

  it("switches the theme from the account corner", async () => {
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    await renderHeaderAt("/");
    await chromeSettled(true);
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    fireEvent.click(screen.getByRole("button", { name: "switch to light mode" }));
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    expect(localStorage.getItem("peasant-theme")).toBe("light");
  });
});
