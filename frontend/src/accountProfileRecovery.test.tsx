import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { loadAccountSettingsFixtures } from "@/test/accountSettingsFixtures";
import { loadAccountProfileRecoveryFixtures } from "@/test/accountProfileRecoveryFixtures";
import { installSettingsRouteREST, installSettingsRouteTeardown, renderSettingsRoute, renderHeader } from "@/test/mountedSettingsRoute";
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

it(fixtures.headerLogout.name, async () => {
  const backend = installSettingsRouteREST(base, undefined, { failLogout: true });
  await renderHeader();
  const account = await screen.findByRole("button", { name: "account menu for @alice-dev" });
  fireEvent.click(account);
  fireEvent.click(screen.getByRole("menuitem", { name: "sign out" }));
  const dialog = await screen.findByRole("dialog", { name: "could not sign out" });
  expect(within(dialog).getByRole("alert")).toHaveTextContent(fixtures.headerLogout.error);
  expect(account).toBeInTheDocument();
  fireEvent.click(within(dialog).getByRole("button", { name: "try again" }));
  await waitFor(() => expect(backend.writes).toEqual(["POST /auth/logout", "POST /auth/logout"]));
  expect(await within(dialog).findByRole("alert")).toHaveTextContent(fixtures.headerLogout.error);
});

for (const row of fixtures.accountSwitchCases) {
  it(row.name, async () => {
    document.cookie = "peasant_token=first-account; path=/";
    const backend = installSettingsRouteREST({ ...base, discoverable: true }, undefined, { deferProfile: row.first });
    try {
      const client = await renderSettingsRoute();
      await screen.findByTestId("settings-page");
      if (row.first === "handle") await editHandle(row.handle);
      else fireEvent.click(screen.getByRole("switch", { name: "discoverable profile" }));
      await waitFor(() => expect(backend.writes).toHaveLength(1));
      if (row.first === "handle") fireEvent.click(screen.getByRole("switch", { name: "discoverable profile" }));
      else await editHandle(row.handle);
      await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
      expect(backend.writes).toHaveLength(1);
      // Model a different tab completing sign-in, then execute the real /me
      // read that the app uses when it returns to the foreground.
      backend.switchAccount(row.nextAccountID, row.nextHandle, row.nextDiscoverable);
      if (row.changeCredential) document.cookie = "peasant_token=next-account; path=/";
      await act(async () => { await client.invalidateQueries({ queryKey: ["me"] }); });
      await waitFor(() => expect(client.getQueryData(["me"])).toMatchObject({ id: row.nextAccountID, github_username: row.nextHandle }));
      await act(async () => backend.releaseProfile());
      await waitFor(() => expect(client.isMutating()).toBe(0));
      expect(backend.writes).toHaveLength(1);
      expect(client.getQueryData(["me"])).toMatchObject({ id: row.nextAccountID, github_username: row.nextHandle, is_discoverable: row.nextDiscoverable });

      expect(screen.getByRole("switch", { name: "discoverable profile" })).toHaveAttribute("aria-checked", String(row.nextDiscoverable));
    } finally {
      backend.releaseProfile();
      document.cookie = "peasant_token=; max-age=0; path=/";
    }
  });
}

it(fixtures.headerPending.name, async () => {
  const backend = installSettingsRouteREST(base, undefined, { failLogout: true, deferLogout: true });
  try {
    await renderHeader();
    fireEvent.click(await screen.findByRole("button", { name: "account menu for @alice-dev" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "sign out" }));
    const dialog = await screen.findByRole("dialog", { name: "signing out" });
    expect(within(dialog).getByRole("status")).toHaveTextContent("waiting for the sign-out request");
    expect(within(dialog).getByRole("button", { name: "close dialog" })).toBeDisabled();
    expect(within(dialog).queryByRole("button", { name: "try again" })).not.toBeInTheDocument();
    expect(backend.writes).toEqual(["POST /auth/logout"]);
    backend.releaseLogout();
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(fixtures.headerLogout.error);
    expect(within(dialog).getByRole("button", { name: "try again" })).toBeInTheDocument();
  } finally { backend.releaseLogout(); }
});

it(fixtures.headerRapid.name, async () => {
  const backend = installSettingsRouteREST(base, undefined, { failLogout: true, deferLogout: true });
  const frames: FrameRequestCallback[] = [];
  try {
    await renderHeader();
    const account = await screen.findByRole("button", { name: "account menu for @alice-dev" });
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => { frames.push(callback); return frames.length; });
    fireEvent.click(account);
    fireEvent.click(screen.getByRole("menuitem", { name: "sign out" }));
    fireEvent.click(account);
    const repeated = screen.getByRole("menuitem", { name: "sign out" });
    expect(repeated).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(repeated);
    await act(async () => { while (frames.length) frames.shift()!(0); });
    await waitFor(() => expect(backend.writes).toEqual(["POST /auth/logout"]));
    backend.releaseLogout();
    expect(await screen.findByRole("alert")).toHaveTextContent(fixtures.headerLogout.error);
  } finally { backend.releaseLogout(); }
});
