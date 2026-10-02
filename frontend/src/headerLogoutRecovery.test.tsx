import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { installHomeRouteREST, installHomeRouteTeardown, renderHeaderAt } from "@/test/mountedHomeRoute";
import { loadHeaderLogoutRecoveryFixtures } from "@/test/headerLogoutRecoveryFixtures";
installHomeRouteTeardown();
for (const row of loadHeaderLogoutRecoveryFixtures().cases) {
  it(row.name, async () => {
    installHomeRouteREST({ viewerUsername: "alice-dev", transcripts: [] });
    const read = globalThis.fetch;
    const writes: string[] = [];
    let release = () => {};
    const held = row.mode === "failed" ? null : new Promise<void>((resolve) => { release = resolve; });
    vi.stubGlobal("fetch", async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith("/auth/logout")) {
        writes.push(`${init?.method} /auth/logout`);
        await held;
        return new Response(JSON.stringify({ error: row.error }), { status: 500, headers: { "content-type": "application/json" } });
      }
      return read(input, init);
    });
    try {
      await renderHeaderAt("/groups");
      const account = await screen.findByRole("button", { name: "account menu for @alice-dev" });
      const frames: FrameRequestCallback[] = [];
      if (row.mode === "rapid") vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => { frames.push(callback); return frames.length; });
      fireEvent.click(account);
      fireEvent.click(screen.getByRole("menuitem", { name: "sign out" }));
      if (row.mode === "rapid") {
        fireEvent.click(account);
        const repeated = screen.getByRole("menuitem", { name: "sign out" });
        expect(repeated).toHaveAttribute("aria-disabled", "true");
        fireEvent.click(repeated);
        await act(async () => { while (frames.length) frames.shift()!(0); });
      }
      if (row.mode !== "failed") {
        const pending = await screen.findByRole("dialog", { name: "signing out" });
        expect(within(pending).getByRole("status")).toHaveTextContent("waiting for the sign-out request");
        expect(within(pending).getByRole("button", { name: "close dialog" })).toBeDisabled();
        expect(within(pending).queryByRole("button", { name: "try again" })).not.toBeInTheDocument();
        expect(writes).toEqual(["POST /auth/logout"]);
        release();
      }
      const dialog = await screen.findByRole("dialog", { name: "could not sign out" });
      expect(within(dialog).getByRole("alert")).toHaveTextContent(row.error);
      expect(account).toBeInTheDocument();
      if (row.mode === "failed") {
        fireEvent.click(within(dialog).getByRole("button", { name: "try again" }));
        await waitFor(() => expect(writes).toEqual(["POST /auth/logout", "POST /auth/logout"]));
      }
    } finally { release(); }
  });
}
