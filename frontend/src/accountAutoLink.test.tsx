import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { installSettingsRouteREST, installSettingsRouteTeardown, renderSettingsRoute } from "@/test/mountedSettingsRoute";
import { loadAccountSettingsFixtures } from "@/test/accountSettingsFixtures";
import { loadAccountAutoLinkFixtures } from "@/test/accountAutoLinkFixtures";
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn(), replace: vi.fn() }), usePathname: () => "/settings" }));
installSettingsRouteTeardown();
const profile = loadAccountSettingsFixtures().cases[0];
describe("the mounted automatic pull request setting", () => {
  for (const row of loadAccountAutoLinkFixtures()) {
    it(row.name, async () => {
      const backend = installSettingsRouteREST(profile, row);
      await renderSettingsRoute();
      const control = await screen.findByRole("switch", { name: "link my transcripts to my pull requests automatically" });
      if (row.read === "failed") {
        const error = await screen.findByTestId("settings-auto-link-read-error");
        expect(within(error).getByRole("alert")).toHaveTextContent("could not be read");
        expect(control).toBeDisabled();
        await act(async () => { fireEvent.click(control); });
        expect(backend.writes).toEqual([]);
        const reads = backend.promptReads;
        await act(async () => { fireEvent.click(screen.getByTestId("settings-auto-link-retry")); });
        await waitFor(() => expect(backend.promptReads).toBeGreaterThan(reads));
        await waitFor(() => expect(screen.queryByTestId("settings-auto-link-read-error")).not.toBeInTheDocument());
      }
      await waitFor(() => expect(control).not.toBeDisabled());
      expect(control).toHaveAttribute("aria-checked", String(row.initial));
      if (row.action === "toggle") {
        await act(async () => { fireEvent.click(control); });
        await waitFor(() => expect(backend.writes).toEqual([`PATCH /users/me/settings ${JSON.stringify({ auto_attach_pull_requests: !row.initial })}`]));
        if (row.write === "failed") expect(await screen.findByRole("alert")).toHaveTextContent("the settings service is unavailable");
      } else expect(backend.writes).toEqual([]);
      await waitFor(() => expect(control).toHaveAttribute("aria-checked", String(row.expected)));
      await waitFor(() => expect(screen.queryByText("saving")).not.toBeInTheDocument());
      expect(screen.getByTestId("settings-auto-link")).not.toHaveTextContent("not available yet");
    });
  }
});
