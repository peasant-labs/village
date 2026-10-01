import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { loadAccountSettingsFixtures } from "@/test/accountSettingsFixtures";
import { loadAccountProfileRecoveryFixtures } from "@/test/accountProfileRecoveryFixtures";
import { installSettingsRouteREST, installSettingsRouteTeardown, renderSettingsRoute } from "@/test/mountedSettingsRoute";
const base = loadAccountSettingsFixtures().cases.find((row) => row.name === "no-peasant-keys-says-peasant-is-not-signed-in")!;
const fixtures = loadAccountProfileRecoveryFixtures();
installSettingsRouteTeardown();
async function editHandle(handle: string) {
  fireEvent.click(screen.getByRole("button", { name: "edit handle" }));
  fireEvent.change(screen.getByRole("textbox", { name: "handle" }), { target: { value: handle } });
  fireEvent.click(within(screen.getByTestId("settings-handle")).getByRole("button", { name: "save" }));
}
for (const row of fixtures.cases) {
  it(row.name, async () => {
    const backend = installSettingsRouteREST({ ...base, discoverable: true }, undefined, { deferProfile: row.first });
    try {
      await renderSettingsRoute();
      await screen.findByTestId("settings-page");
      if (row.first === "handle") await editHandle(row.handle);
      else fireEvent.click(screen.getByRole("switch", { name: "discoverable profile" }));
      await waitFor(() => expect(backend.writes).toHaveLength(1));
      if (row.first === "handle") fireEvent.click(screen.getByRole("switch", { name: "discoverable profile" }));
      else await editHandle(row.handle);
      // The second control can queue a save, but cannot write while the first
      // route's whole-profile response is outstanding.
      await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
      expect(backend.writes).toHaveLength(1);
      await act(async () => backend.releaseProfile());
      await waitFor(() => expect(backend.writes).toHaveLength(2));
      await waitFor(() => {
        expect(screen.getByTestId("settings-handle").textContent).toContain(row.handle);
        expect(screen.getByRole("switch", { name: "discoverable profile" })).toHaveAttribute("aria-checked", String(row.discoverable));
        expect(screen.getByTestId("settings-handle").textContent).not.toContain("saving");
      });
      expect(backend.writes).toEqual(row.first === "handle"
        ? [`PATCH /auth/me/username ${JSON.stringify({ username: row.handle })}`, `PATCH /auth/me/settings ${JSON.stringify({ is_discoverable: row.discoverable })}`]
        : [`PATCH /auth/me/settings ${JSON.stringify({ is_discoverable: row.discoverable })}`, `PATCH /auth/me/username ${JSON.stringify({ username: row.handle })}`]);
    } finally { backend.releaseProfile(); }
  });
}
it(fixtures.logout.name, async () => {
  const backend = installSettingsRouteREST(base, undefined, { failLogout: true });
  await renderSettingsRoute();
  const connection = await screen.findByTestId("settings-sign-in-account");
  fireEvent.click(within(connection).getByRole("button", { name: "sign out" }));
  expect(await within(connection).findByRole("alert")).toHaveTextContent(fixtures.logout.error);
  expect(within(connection).getByRole("alert")).toHaveTextContent("try again");
  expect(screen.getByTestId("settings-page")).toBeInTheDocument();
  fireEvent.click(within(connection).getByRole("button", { name: "sign out" }));
  await waitFor(() => expect(backend.writes).toEqual(["POST /auth/logout", "POST /auth/logout"]));
});
