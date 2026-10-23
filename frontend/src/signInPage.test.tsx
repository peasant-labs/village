import { screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderAppRoute,
} from "@/test/mountedHomeRoute";
import { loadSignInPageFixtures } from "@/test/signInPageFixtures";
import { SIGN_IN_PROVIDERS, startSignIn, takeNextPath } from "@/lib/signIn";
import { leaveForSignIn } from "@/lib/marketing";

// Mounts the REAL app chrome around the REAL route and reads where each step
// of signing in sends the visitor. Leaving the app is the boundary stubbed:
// `startSignIn` would navigate the whole document to the provider, so it
// records the provider id instead, and leaving for the marketing site's log-in
// popup is recorded the same way (vitest.setup.ts).
vi.mock("@/lib/signIn", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/signIn")>();
  return { ...actual, startSignIn: vi.fn() };
});

const fixtures = loadSignInPageFixtures();

installHomeRouteTeardown();

beforeEach(() => {
  vi.mocked(startSignIn).mockClear();
  vi.mocked(leaveForSignIn).mockClear();
  sessionStorage.clear();
});

describe("the providers peasant online offers", () => {
  it("is exactly the corpus's closed set", () => {
    expect(SIGN_IN_PROVIDERS.map((p) => p.id).sort()).toEqual([...fixtures.offeredProviders].sort());
  });
});

describe("mounted sign-in steps: where each screen sends the visitor", () => {
  for (const c of fixtures.cases) {
    it(c.name, async () => {
      installHomeRouteREST({ viewerUsername: c.signedIn ? "alice-dev" : null, transcripts: [] });
      await renderAppRoute(c.path, { header: true });

      // Settled once the session has answered: the frame and the page have
      // drawn their answer, and any step that leaves has left.
      await waitFor(() => {
        expect(document.querySelector('[data-testid="root-route-pending"]')).toBeNull();
        if (c.expectStart !== null) expect(vi.mocked(startSignIn)).toHaveBeenCalled();
        if (c.expectLeaving) expect(vi.mocked(leaveForSignIn)).toHaveBeenCalled();
        if (c.expectSignInLink !== null) expect(screen.getByRole("link", { name: "sign in" })).toBeInTheDocument();
        if (c.signedIn && c.path === "/") expect(screen.getByTestId("account-menu-trigger")).toBeInTheDocument();
      });

      expect(vi.mocked(startSignIn).mock.calls.map(([provider]) => provider)).toEqual(
        c.expectStart === null ? [] : [c.expectStart],
      );
      expect(takeNextPath()).toBe(c.expectRemembered);
      expect(vi.mocked(leaveForSignIn).mock.calls.length).toBe(c.expectLeaving ? 1 : 0);
      if (c.expectLeaving) expect(screen.getByTestId("leaving-for-sign-in")).toBeInTheDocument();

      // No screen offers a provider button of its own any more: signing in
      // starts on the marketing site's popup.
      expect(document.querySelector(".si-split")).toBeNull();
      const links = screen.queryAllByRole("link", { name: "sign in" });
      expect(links.map((link) => link.getAttribute("href"))).toEqual(
        c.expectSignInLink === null ? [] : [c.expectSignInLink],
      );
    });
  }
});
