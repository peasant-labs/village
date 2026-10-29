import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderAppRoute,
  renderHeaderAt,
} from "@/test/mountedHomeRoute";
import { loadHomePageFixtures, type HomeCollectiveCase } from "@/test/homePageFixtures";
import { pushedRoutes, resetNextNavigation } from "@/test/nextNavigationMock";
import { DOCS_URL } from "@/lib/appLinks";
import { leaveForSignIn } from "@/lib/marketing";
import { isTranscriptPage } from "@/lib/signIn";
import { useDeclareSignedOutReadable } from "@/components/shell/signedOutDeclaration";

// Mounts the REAL app chrome (`AppFrame`: fairtrade's sidebar for somebody
// signed in, the public bar for anybody else) inside the real `AuthProvider`,
// with the session answered by the same `GET /auth/me` the app calls. The
// sidebar is the only place a person can see WHERE they are, so which entries
// it offers and which one it marks current is asserted on the rendered links,
// not on the groups the shell hands it.

const fixtures = loadHomePageFixtures();

installHomeRouteTeardown();

beforeEach(() => {
  vi.mocked(leaveForSignIn).mockClear();
});

/** The one collective every signed-in case belongs to. */
const ACME: HomeCollectiveCase = { id: "c011ec71-0000-4000-8000-000000000001", name: "Acme Platform", role: "owner", members: 12 };

const SECTIONS_NAV = "peasant sections";

/** A transcript page whose server did not mark it public, as it declares itself to the frame. */
function TranscriptNotMarkedPublic() {
  useDeclareSignedOutReadable(false);
  return null;
}

async function chromeSettled(expectNav: "sections" | "public" | "none"): Promise<void> {
  // Signed in, the account trigger means the session has answered and the
  // sidebar has drawn; on the privacy notice, the public bar's way in; anywhere
  // else signed out, the frame's word that it is taking the visitor to sign in.
  await waitFor(() => {
    if (expectNav === "sections") expect(document.querySelector(".asb-foot .asb-account")).not.toBeNull();
    else if (expectNav === "public") expect(screen.getByRole("link", { name: "sign in" })).toBeInTheDocument();
    else expect(screen.getByTestId("leaving-for-sign-in")).toBeInTheDocument();
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
      const path = c.pathname.split("?")[0];
      await renderHeaderAt(c.pathname, isTranscriptPage(path) ? <TranscriptNotMarkedPublic /> : null);
      await chromeSettled(c.expectNav);

      if (c.expectNav !== "sections") {
        // No sidebar at all, rather than an empty one a screen reader would
        // still announce. The public bar is the chrome on the privacy notice;
        // anywhere else the visitor is on their way to sign in.
        expect(screen.queryByRole("navigation", { name: SECTIONS_NAV })).toBeNull();
        expect(screen.queryByTestId("public-top-bar") !== null).toBe(c.expectNav === "public");
        expect(vi.mocked(leaveForSignIn).mock.calls.length).toBe(c.expectNav === "none" ? 1 : 0);
        return;
      }
      expect(screen.queryByTestId("public-top-bar")).toBeNull();

      const nav = await screen.findByRole("navigation", { name: SECTIONS_NAV });
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
    await chromeSettled("sections");
    const nav = screen.getByRole("navigation", { name: SECTIONS_NAV });
    await waitFor(() => expect(within(nav).getByRole("link", { name: "Solo Circle" })).toBeInTheDocument());
    const listed = within(nav).getAllByRole("link").slice(4);
    // User content keeps its case, and each row leads to its collective.
    expect(listed.map((a) => a.textContent)).toEqual(mine.map((c) => c.name));
    expect(listed.map((a) => a.getAttribute("href"))).toEqual(mine.map((c) => `/collectives/${c.id}`));
  });
});

describe("mounted sidebar: the other apps", () => {
  it("offers docs in a new tab without community or duplicate app links", async () => {
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    await renderHeaderAt("/");
    await chromeSettled("sections");

    // Search and docs share the shortcuts near the sidebar footer.
    const apps = screen.getByRole("navigation", { name: "shortcuts" });
    // The sidebar is this app, so the switcher does not name it, and the local
    // app runs on the person's own computer, so it is not offered from here.
    expect(within(apps).queryByText("online")).toBeNull();
    expect(within(apps).queryByText("local")).toBeNull();
    // The docs are the one way out; community is intentionally absent.
    expect(within(apps).getAllByRole("link")).toHaveLength(1);
    const docs = within(apps).getByRole("link", { name: /^docs/ });
    expect(docs).toHaveAttribute("href", DOCS_URL);
    expect(docs).toHaveAttribute("href", "https://peasantlabs.org/docs");
    expect(docs).toHaveAttribute("target", "_blank");
    expect(docs).toHaveTextContent("opens in a new tab");
    expect(screen.queryByText(/community/i)).not.toBeInTheDocument();
    // The docs are offered once, in the switcher, not again among the sections.
    expect(screen.getAllByRole("link", { name: /^docs/ })).toHaveLength(1);
  });
});

describe("mounted sidebar: the search", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  // The app's search is /explore's transcript search: the sidebar item and
  // the keyboard both go there, and its box takes the cursor on arrival.
  it("opens discover from the sidebar's search item", async () => {
    resetNextNavigation();
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    await renderHeaderAt("/");
    await chromeSettled("sections");
    fireEvent.click(screen.getByRole("button", { name: /^search/ }));
    expect(pushedRoutes).toEqual(["/discover?view=transcripts"]);
  });

  it("opens discover with command or control and k", async () => {
    resetNextNavigation();
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    await renderHeaderAt("/collectives");
    await chromeSettled("sections");
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    fireEvent.keyDown(window, { key: "K", ctrlKey: true });
    expect(pushedRoutes).toEqual(["/discover?view=transcripts", "/discover?view=transcripts"]);
  });

  it("puts the cursor in explore's search box when it arrives there", async () => {
    resetNextNavigation();
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    await renderHeaderAt("/");
    await chromeSettled("sections");
    fireEvent.click(screen.getByRole("button", { name: /^search/ }));
    cleanup();
    await renderAppRoute("/explore", { header: true });
    // The page redraws its box as it settles; the box drawn last holds the cursor.
    await waitFor(() =>
      expect(document.activeElement).toBe(screen.getByRole("textbox", { name: /^search transcripts/ })),
    );
  });

  it("puts the cursor in the box at once when explore is already open", async () => {
    // Long after any earlier request to focus the box on arrival.
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(Date.now() + 60_000);
    resetNextNavigation();
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    await renderAppRoute("/explore", { header: true });
    await chromeSettled("sections");
    const box = await screen.findByRole("textbox", { name: /^search transcripts/ });
    // Explore is the whole page, so its title is the page's one h1.
    expect(screen.getAllByRole("heading", { level: 1 }).map((h) => h.textContent)).toEqual(["explore transcripts"]);
    expect(document.activeElement).not.toBe(box);
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(document.activeElement).toBe(box);
    expect(pushedRoutes).toEqual([]);
  });
});

describe("mounted sidebar: the settings gear", () => {
  it("leads to the person's settings and is current there, and only there", async () => {
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    await renderHeaderAt("/settings");
    await chromeSettled("sections");
    const gear = screen.getByRole("link", { name: "settings" });
    expect(gear).toHaveAttribute("href", "/settings");
    expect(gear).toHaveAttribute("aria-current", "page");
    const sections = screen.getByRole("navigation", { name: SECTIONS_NAV });
    expect(within(sections).queryByRole("link", { current: "page" })).toBeNull();
  });
});

describe("mounted sidebar: the account menu", () => {
  it("names the signed-in person and holds exactly the fixture's items, in order", async () => {
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    await renderHeaderAt("/");
    await chromeSettled("sections");

    const trigger = screen.getByRole("button", { name: "@alice-dev" });
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
      await chromeSettled("sections");

      fireEvent.click(screen.getByRole("button", { name: "@alice-dev" }));
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

  it("switches the theme from the sidebar's footer", async () => {
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    await renderHeaderAt("/");
    await chromeSettled("sections");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    fireEvent.click(screen.getByRole("button", { name: "switch to light mode" }));
    expect(document.documentElement.getAttribute("data-theme")).toBe("light");
    expect(localStorage.getItem("peasant-theme")).toBe("light");
  });
});
