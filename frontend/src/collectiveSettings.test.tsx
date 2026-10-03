import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { replacedRoutes } from "@/test/nextNavigationMock";
import { loadCollectiveSettingsFixtures, type SettingsCase } from "@/test/collectiveSettingsFixtures";
import {
  installCollectivePagesTeardown,
  installCollectiveREST,
  renderCollectiveSettings,
  textOf,
} from "@/test/mountedCollectivePages";

/**
 * A collective's settings, mounted through the real route and rendered by
 * fairtrade's real `CollectiveSettingsView`, driven by
 * `src/testdata/collective-settings.yaml`. Each case asserts the writes the
 * page sent (route and exact body) and what the owner is shown.
 */

installCollectivePagesTeardown();

const fixtures = loadCollectiveSettingsFixtures();

async function perform(settingsCase: SettingsCase): Promise<void> {
  const action = settingsCase.action;
  switch (action.kind) {
    case "edit-text": {
      fireEvent.click(screen.getByRole("button", { name: `edit ${action.label}` }));
      const field = await screen.findByRole("textbox", { name: action.label });
      fireEvent.change(field, { target: { value: action.value } });
      fireEvent.click(screen.getByRole("button", { name: "save" }));
      return;
    }
    case "toggle":
      fireEvent.click(screen.getByRole("switch", { name: action.label }));
      return;
    case "role":
      fireEvent.change(screen.getByRole("combobox", { name: `role for @${action.member}` }), {
        target: { value: action.role },
      });
      return;
    case "remove": {
      fireEvent.click(screen.getByRole("button", { name: `remove @${action.member}` }));
      const dialog = await screen.findByRole("dialog");
      fireEvent.click(within(dialog).getByRole("button", { name: "remove" }));
      return;
    }
    case "none":
      return;
  }
}

describe("the collective settings", () => {
  it.each(fixtures.cases.map((c) => [c.name, c] as const))("%s", async (_name, settingsCase) => {
    const world = fixtures.worldFor(settingsCase);
    window.history.replaceState(null, "", `/groups/${world.group.id}/settings${settingsCase.search}`);
    const requests = installCollectiveREST(world);
    await renderCollectiveSettings(world);
    await perform(settingsCase);
    const expected = settingsCase.expect;

    const wantWrites = expected.writes.map((write) => ({
      method: write.method,
      path: fixtures.pathFor(settingsCase, write.path),
      body: write.body,
    }));
    await waitFor(() => expect(requests.filter((r) => r.method !== "GET"), settingsCase.why).toEqual(wantWrites));
    if (wantWrites.length === 0) {
      // Let every read the page makes on arrival settle, then check nothing was written.
      await act(async () => new Promise((done) => setTimeout(done, 50)));
      expect(requests.filter((r) => r.method !== "GET")).toEqual([]);
    }

    for (const text of expected.shows) {
      await waitFor(() => expect(document.body.textContent).toContain(text));
    }
    if (expected.alert) {
      await waitFor(() => expect(screen.getAllByRole("alert").map((el) => textOf(el))).toContain(expected.alert));
    }
    if (expected.switchOn !== null) {
      await waitFor(() =>
        expect(screen.getByRole("switch", { name: "show transcripts on pull requests" })).toHaveAttribute(
          "aria-checked",
          String(expected.switchOn),
        ),
      );
    }
    if (expected.notice) {
      const notice = await screen.findByTestId("collective-notice");
      expect(textOf(notice.querySelector(".fb-toast-title"))).toBe(expected.notice);
    }
    if (expected.replaced) {
      await waitFor(() => expect(replacedRoutes).toContain(fixtures.pathFor(settingsCase, expected.replaced as string)));
    } else {
      expect(replacedRoutes).toEqual([]);
    }

    // On every case: the check-mode radios are not drawn, and no write claims
    // the linked org or touches the stored check mode.
    expect(screen.queryAllByRole("radio")).toEqual([]);
    expect(document.body.textContent).not.toMatch(/check mode|informational|required/i);
    for (const write of requests.filter((r) => r.method !== "GET")) {
      expect(write.body ?? {}).not.toHaveProperty("linked_github_org");
      expect(write.body ?? {}).not.toHaveProperty("prompts_check_mode");
    }
  });
});
