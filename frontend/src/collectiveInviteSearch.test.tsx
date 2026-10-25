import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { loadCollectiveInviteSearchFixtures } from "@/test/collectiveInviteSearchFixtures";
import { loadCollectiveSettingsFixtures } from "@/test/collectiveSettingsFixtures";
import { installCollectivePagesTeardown, installCollectiveREST, renderCollectiveSettings } from "@/test/mountedCollectivePages";
installCollectivePagesTeardown();
const settings = loadCollectiveSettingsFixtures();
for (const testCase of loadCollectiveInviteSearchFixtures()) {
  it(testCase.name, async () => {
    const world = settings.worldFor(settings.cases[0]);
    const requests = installCollectiveREST(world);
    const delegate = globalThis.fetch;
    const searches: string[] = [];
    let attempts = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input));
      if (url.origin === "https://api.github.com") {
        searches.push(url.searchParams.get("q") ?? "");
        return new Response(JSON.stringify({ items: [{ login: testCase.handle, avatar_url: "" }] }), { headers: { "content-type": "application/json" } });
      }
      if (init?.method === "POST" && url.pathname.endsWith("/invitations")) {
        attempts += 1;
        if (testCase.fail && attempts === 1) return new Response(JSON.stringify({ error: "invitation unavailable" }), { status: 500, headers: { "content-type": "application/json" } });
      }
      return delegate(input, init);
    }));
    // The search lives in the members section, beside the invite field.
    window.history.replaceState(null, "", `/collectives/${world.group.id}/settings?section=members`);
    await renderCollectiveSettings(world);
    fireEvent.click(screen.getByRole("button", { name: "find a github user to invite" }));
    const dialog = await screen.findByRole("dialog", { name: "invite by username" });
    fireEvent.change(within(dialog).getByRole("combobox"), { target: { value: testCase.query } });
    const result = await within(dialog).findByRole("menuitem", { name: testCase.handle });
    expect(searches).toEqual([testCase.query]);
    expect(attempts).toBe(0);
    fireEvent.click(result);
    expect(within(dialog).getByRole("combobox")).toHaveValue(testCase.handle);
    fireEvent.click(within(dialog).getByRole("button", { name: "invite" }));
    if (testCase.fail) {
      await waitFor(() => expect(within(dialog).getByRole("alert")).toHaveTextContent("invitation unavailable"));
      fireEvent.click(within(dialog).getByRole("button", { name: "invite" }));
    }
    await waitFor(() => expect(requests.filter((request) => request.method === "POST")).toEqual([{ method: "POST", path: `/collectives/${world.group.id}/invitations`, body: { username: testCase.handle } }]));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "invite by username" })).toBeNull());
    expect(screen.getByTestId("collective-notice")).toHaveTextContent(`if @${testCase.handle} has a village account they'll see it. we don't say whether they do.`);
  });
}
