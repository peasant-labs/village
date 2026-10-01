import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  installHomeRouteREST,
  installHomeRouteTeardown,
  renderAppRoute,
} from "@/test/mountedHomeRoute";
import { loadSignInPageFixtures } from "@/test/signInPageFixtures";
import { SIGN_IN_PROVIDERS, startSignIn } from "@/lib/signIn";

// Mounts the REAL header over the REAL route and reads which providers each
// sign-in button on the screen actually leads to. Leaving the app for a
// provider is the one boundary stubbed: `startSignIn` would navigate the whole
// document away, so it records the provider id instead, and the provider set is
// the ids the buttons hand it, not the labels they print.
vi.mock("@/lib/signIn", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/signIn")>();
  return { ...actual, startSignIn: vi.fn() };
});

const fixtures = loadSignInPageFixtures();

installHomeRouteTeardown();

beforeEach(() => {
  vi.mocked(startSignIn).mockClear();
});

/** Every provider one fairtrade split button leads to: its primary action, and
 *  each row behind its chevron when it has one. */
function providersBehind(split: HTMLElement): string[] {
  const reached: string[] = [];
  const record = () => {
    const calls = vi.mocked(startSignIn).mock.calls;
    reached.push(String(calls[calls.length - 1]?.[0]));
  };
  const primary = split.querySelector<HTMLButtonElement>(".si-split-primary");
  if (primary == null) throw new Error("a sign-in button with no primary action");
  fireEvent.click(primary);
  record();
  const caret = within(split).queryByRole("button", { name: "more sign-in providers" });
  if (caret != null) {
    fireEvent.click(caret);
    const rows = within(split).getAllByRole("menuitem");
    for (let i = 0; i < rows.length; i += 1) {
      // Choosing a row closes the menu, so reopen it for each one.
      if (i > 0) fireEvent.click(caret);
      fireEvent.click(within(split).getAllByRole("menuitem")[i]);
      record();
    }
  }
  return reached;
}

function offeredIn(region: "header" | "page"): string[] {
  const splits = Array.from(document.querySelectorAll<HTMLElement>(".si-split")).filter(
    (el) => (el.closest("header") != null) === (region === "header"),
  );
  return splits.flatMap(providersBehind);
}

describe("the providers village offers", () => {
  it("is exactly the corpus's closed set", () => {
    expect(SIGN_IN_PROVIDERS.map((p) => p.id).sort()).toEqual([...fixtures.offeredProviders].sort());
  });
});

describe("mounted sign-in buttons: which providers each screen leads to", () => {
  for (const c of fixtures.cases) {
    it(c.name, async () => {
      installHomeRouteREST({ viewerUsername: c.signedIn ? "alice-dev" : null, transcripts: [] });
      await renderAppRoute(c.path, { header: true });

      // Settled when the header's placeholder is gone and the page has drawn.
      await waitFor(() => {
        expect(document.querySelector("header .animate-shimmer")).toBeNull();
        expect(document.querySelector('[data-testid="root-route-pending"]')).toBeNull();
      });
      if (c.path === "/" && !c.signedIn) {
        await screen.findByTestId("sign-in-page");
        expect(
          screen.getByRole("heading", {
            level: 1,
            name: "the agent sessions behind your team's pull requests",
          }),
        ).toBeInTheDocument();
      }

      expect(offeredIn("page")).toEqual(c.expectPageProviders);
      expect(offeredIn("header")).toEqual(c.expectHeaderProviders);
    });
  }
});
