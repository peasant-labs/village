import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  installSettingsRouteREST,
  installSettingsRouteTeardown,
  renderSettingsRoute,
} from "@/test/mountedSettingsRoute";
import { loadAccountSettingsFixtures } from "@/test/accountSettingsFixtures";
import { loadAccountOrgsFixtures } from "@/test/accountOrgsFixtures";

// Mounts the REAL `/settings` route and asserts on the switch a person presses
// and on the write village receives. Linking an organization is the only way a
// member of it can contribute to a collective that requires that organization,
// so the write, not just the drawn state, is what these cases hold.

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/settings",
}));

installSettingsRouteTeardown();

const profile = loadAccountSettingsFixtures().cases[0];

function text(el: Element | null | undefined): string {
  return (el?.textContent ?? "").replace(/\s+/g, " ").trim();
}

/** The org switches the page drew: one per organization, each named by its login. */
function orgSwitches(): HTMLElement[] {
  return [
    ...document.querySelectorAll<HTMLElement>('[data-testid^="settings-org-"] [role="switch"]'),
  ];
}

describe("the mounted organization links on settings", () => {
  for (const row of loadAccountOrgsFixtures()) {
    it(row.name, async () => {
      const items = row.orgs.map((org) => ({ ...org }));
      const backend = installSettingsRouteREST(profile, undefined, undefined, {
        items,
        read: row.read,
        write: row.write,
      });
      await renderSettingsRoute();
      await screen.findByTestId("settings-page");

      if (row.expected_list_state === "read-error") {
        const error = await screen.findByTestId("settings-orgs-read-error");
        expect(within(error).getByRole("alert")).toHaveTextContent("could not be read");
        // A list that failed to load is not an empty list: the page must not
        // also claim there are none, and nothing may be written from it.
        expect(screen.queryByTestId("settings-orgs-empty")).toBeNull();
        expect(orgSwitches()).toHaveLength(0);
        expect(backend.writes).toEqual([]);

        await act(async () => {
          fireEvent.click(screen.getByTestId("settings-orgs-retry"));
        });
        await waitFor(() =>
          expect(screen.queryByTestId("settings-orgs-read-error")).not.toBeInTheDocument(),
        );
        // The retry reached a real answer: this account has none, which is the
        // guidance case rather than a blank group.
        expect(text(await screen.findByTestId("settings-orgs-empty"))).toContain(
          "sign out and sign in again",
        );
        return;
      }

      if (row.expected_list_state === "empty-guidance") {
        const empty = await screen.findByTestId("settings-orgs-empty");
        expect(text(empty)).toContain("sign out and sign in again");
        // No switch to press, and nothing sent: an empty list is not an action.
        expect(orgSwitches()).toHaveLength(0);
        expect(backend.writes).toEqual([]);
        return;
      }

      const current = row.orgs.find((org) => org.org_login === row.target);
      expect(current, `case ${row.name} names a target it does not list`).toBeDefined();
      const control = await screen.findByRole("switch", { name: row.target });
      expect(control).toHaveAttribute("aria-checked", String(current!.visible));

      if (row.action !== "toggle") return;

      await act(async () => {
        fireEvent.click(control);
      });
      await waitFor(() =>
        expect(backend.writes).toEqual([
          `PATCH /auth/orgs/${row.target}/visibility {"visible":${!current!.visible}}`,
        ]),
      );

      if (row.write === "failed") {
        expect(await screen.findByRole("alert")).toHaveTextContent(row.expected_alert!);
      } else {
        expect(screen.queryByRole("alert")).toBeNull();
      }
      // Either way the switch shows the stored state: the new one when the write
      // landed, the previous one when it was refused.
      await waitFor(() =>
        expect(control).toHaveAttribute("aria-checked", String(row.expected_visible)),
      );
    });
  }
});
