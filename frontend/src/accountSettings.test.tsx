import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  installSettingsRouteREST,
  installSettingsRouteTeardown,
  renderHeader,
  renderSettingsRoute,
} from "@/test/mountedSettingsRoute";
import { loadAccountSettingsFixtures, type SettingsCase } from "@/test/accountSettingsFixtures";

// Mounts the REAL `/settings` route. Every assertion is on what lands in the
// DOM and on the writes the page actually sends, so a regression cannot hide
// behind a hook that was never called.

const routePush = vi.hoisted(() => vi.fn());
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: routePush, replace: vi.fn() }),
  usePathname: () => "/settings",
}));

const { cases } = loadAccountSettingsFixtures();

installSettingsRouteTeardown();

beforeEach(() => {
  // "last used" is an age, so the clock is frozen where the corpus says it is.
  // Only Date is faked: the page's requests still resolve on real timers.
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-09-30T12:00:00Z"));
});

function text(el: Element | null | undefined): string {
  return (el?.textContent ?? "").replace(/\s+/g, " ").trim();
}

function row(testId: string): HTMLElement {
  return screen.getByTestId(testId);
}

/** Every alert the page is announcing, in document order. */
function alerts(): string[] {
  return [...screen.getByTestId("settings-page").querySelectorAll('[role="alert"]')].map(text);
}

async function act_(c: SettingsCase): Promise<void> {
  switch (c.action) {
    case "none":
      return;
    case "change-handle": {
      await act(async () => {
        fireEvent.click(screen.getByRole("button", { name: "edit handle" }));
      });
      const field = screen.getByRole("textbox", { name: "handle" });
      await act(async () => {
        fireEvent.change(field, { target: { value: c.handleTo } });
      });
      await act(async () => {
        fireEvent.click(within(row("settings-handle")).getByRole("button", { name: "save" }));
      });
      return;
    }
    case "toggle-discoverable":
      await act(async () => {
        fireEvent.click(screen.getByRole("switch", { name: "discoverable profile" }));
      });
      return;
    case "sign-out-everywhere":
    case "delete-account": {
      const scope = within(row(c.action === "delete-account" ? "settings-delete" : "settings-peasant"));
      const label = c.action === "delete-account" ? "delete account" : "sign out everywhere";
      // The first press only asks: nothing is sent until the person confirms.
      await act(async () => {
        fireEvent.click(scope.getByRole("button", { name: label }));
      });
      expect(scope.getByRole("group", { name: `${label}?` })).toBeInTheDocument();
      await act(async () => {
        fireEvent.click(scope.getByRole("button", { name: c.confirm ? "yes" : "cancel" }));
      });
      if (!c.confirm) {
        // Cancel puts the button back and asks nothing.
        expect(scope.getByRole("button", { name: label })).toBeInTheDocument();
      }
      return;
    }
  }
}

describe("mounted settings route", () => {
  for (const c of cases) {
    it(c.name, async () => {
      const backend = installSettingsRouteREST(c);
      await renderSettingsRoute();
      await screen.findByTestId("settings-page");
      // The connection row has read the keys before anything is pressed.
      await waitFor(() =>
        expect(text(row("settings-peasant"))).not.toContain("checking where peasant is signed in"),
      );

      await act_(c);

      // Every write the page sent, in order, and nothing more.
      await waitFor(() => expect(backend.writes).toEqual(c.expectWrites));

      await waitFor(() =>
        expect(text(row("settings-handle").querySelector(".srow-text-value"))).toBe(c.expectHandle),
      );
      await waitFor(() =>
        expect(
          screen.getByRole("switch", { name: "discoverable profile" }).getAttribute("aria-checked"),
        ).toBe(String(c.expectDiscoverable)),
      );

      await waitFor(() =>
        expect(text(row("settings-peasant").querySelector(".srow-label"))).toBe(c.expectPeasant),
      );
      const help = text(row("settings-peasant").querySelector(".srow-help"));
      if (c.expectLastUsed === null) {
        expect(help).not.toContain("last used");
      } else {
        expect(help).toContain(`last used ${c.expectLastUsed}`);
      }
      // A button that signs peasant out is offered exactly when it is signed in
      // somewhere.
      expect(
        within(row("settings-peasant")).queryByRole("button", { name: "sign out everywhere" }) !== null,
      ).toBe(c.expectPeasant !== "peasant is not signed in on any computer");

      await waitFor(() => expect(alerts()).toEqual(c.expectAlert === null ? [] : [c.expectAlert]));
    });
  }

  it("names the account village signs in with and signs it out", async () => {
    const backend = installSettingsRouteREST(cases[0]);
    await renderSettingsRoute();
    const account = await screen.findByTestId("settings-sign-in-account");
    expect(text(account.querySelector(".srow-label"))).toBe("github · alice-dev");
    await act(async () => {
      fireEvent.click(within(account).getByRole("button", { name: "sign out" }));
    });
    await waitFor(() => expect(backend.writes).toEqual(["POST /auth/logout"]));
  });
});

describe("the account menu", () => {
  it("leads to the settings route", async () => {
    installSettingsRouteREST(cases[0]);
    await renderHeader();
    const trigger = await screen.findByRole("button", { name: /account menu/ });
    await act(async () => {
      fireEvent.click(trigger);
    });
    fireEvent.click(screen.getByRole("menuitem", { name: "settings" }));
    expect(routePush).toHaveBeenCalledWith("/settings");
  });
});
