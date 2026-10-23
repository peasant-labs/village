import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { pushedRoutes, replacedRoutes } from "@/test/nextNavigationMock";
import { loadCollectiveSettingsFixtures, type SettingsCase } from "@/test/collectiveSettingsFixtures";
import {
  installCollectivePagesTeardown,
  installCollectiveREST,
  mountCollectiveSettings,
  renderCollectiveSettings,
  textOf,
} from "@/test/mountedCollectivePages";

/**
 * A collective's settings, mounted through the real route one section at a
 * time (`?section=`), driven by `src/testdata/collective-settings.yaml`. Each
 * case asserts the writes the page sent (route and exact body) and what the
 * owner is shown.
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
    case "choose": {
      const group = screen.getByRole("group", { name: action.group });
      fireEvent.click(within(group).getByRole("button", { name: new RegExp(`^${action.label}`) }));
      return;
    }
    case "select":
      fireEvent.change(screen.getByRole("combobox", { name: action.label }), { target: { value: action.value } });
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
    case "invite":
      fireEvent.change(screen.getByRole("textbox", { name: "invite by github username" }), { target: { value: action.value } });
      fireEvent.click(screen.getByRole("button", { name: "invite" }));
      return;
    case "delete": {
      fireEvent.click(screen.getByRole("button", { name: "delete collective" }));
      const dialog = await screen.findByRole("dialog");
      const confirm = within(dialog).getByRole("button", { name: "delete collective" });
      expect(confirm, "the delete waits for consent").toBeDisabled();
      fireEvent.click(within(dialog).getByRole("checkbox", { name: "i understand this cannot be undone" }));
      expect(confirm).toBeEnabled();
      fireEvent.click(confirm);
      return;
    }
    case "none":
      return;
  }
}

describe("the collective settings", () => {
  it.each(fixtures.cases.map((c) => [c.name, c] as const))("%s", async (_name, settingsCase) => {
    const world = fixtures.worldFor(settingsCase);
    window.history.replaceState(null, "", `/collectives/${world.group.id}/settings${settingsCase.search}`);
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
    if (expected.pressed !== null && settingsCase.action.kind === "choose") {
      const group = screen.getByRole("group", { name: settingsCase.action.group });
      await waitFor(() =>
        expect(
          within(group)
            .getAllByRole("button")
            .filter((card) => card.getAttribute("aria-pressed") === "true")
            .map((card) => textOf(card.querySelector("b"))),
        ).toEqual([expected.pressed]),
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
    if (expected.pushed) {
      await waitFor(() => expect(pushedRoutes).toEqual([expected.pushed]));
    } else {
      expect(pushedRoutes).toEqual([]);
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

describe("the collective settings sections", () => {
  it.each(fixtures.sectionCases.map((c) => [c.name, c] as const))("%s", async (_name, sectionCase) => {
    const world = fixtures.worldFor(fixtures.cases[0]);
    window.history.replaceState(null, "", `/collectives/${world.group.id}/settings${sectionCase.search}`);
    installCollectiveREST(world);
    await renderCollectiveSettings(world);

    expect(screen.getByRole("heading", { level: 1 }), sectionCase.why).toHaveTextContent(sectionCase.expectTitle);
    // The title stands alone: no line explains the section under it.
    expect(document.querySelector("header.on-head p")).toBeNull();
    const nav = screen.getByRole("navigation", { name: "settings sections" });
    const current = within(nav)
      .getAllByRole("link")
      .filter((link) => link.getAttribute("aria-current") === "page");
    expect(current.map((link) => textOf(link))).toEqual([sectionCase.expectTitle]);
    expect(screen.getByTestId(`settings-${sectionCase.expectSection}`)).toBeInTheDocument();

    // Every section is a link in fairtrade's section bar, inside the page body;
    // the breadcrumb is the way back to the collective.
    const base = `/collectives/${world.group.id}/settings`;
    expect(within(nav).getAllByRole("link").map((link) => link.getAttribute("href"))).toEqual([
      base,
      `${base}?section=access`,
      `${base}?section=members`,
      `${base}?section=github`,
      `${base}?section=pull-requests`,
      `${base}?section=danger`,
    ]);
    expect(nav.closest(".snav-layout")).not.toBeNull();
    const crumb = screen.getByRole("navigation", { name: "breadcrumb" });
    expect(within(crumb).getByRole("link", { name: world.group.name })).toHaveAttribute("href", `/collectives/${world.group.id}`);
  });

  it("tells somebody who is not the owner that only the owner can change these settings", async () => {
    const world = { ...fixtures.worldFor(fixtures.cases[0]), role: "member" };
    window.history.replaceState(null, "", `/collectives/${world.group.id}/settings?section=access`);
    const requests = installCollectiveREST(world);
    await mountCollectiveSettings(world);
    await waitFor(() => expect(screen.getByRole("heading", { name: "only the owner can change these settings" })).toBeInTheDocument());
    expect(screen.getByRole("link", { name: "back to the collective" })).toHaveAttribute("href", `/collectives/${world.group.id}`);
    expect(screen.queryByRole("navigation", { name: "settings sections" })).toBeNull();
    expect(requests.filter((r) => r.path.includes("/repositories"))).toEqual([]);
  });

  it("says when no collective answers at the address", async () => {
    const world = fixtures.worldFor(fixtures.cases[0]);
    world.failures = [{ method: "GET", path: `/collectives/${world.group.id}`, status: 404, error: "collective not found" }];
    installCollectiveREST(world);
    await mountCollectiveSettings(world);
    // The read settles once the viewer is known; the state it lands on is the one to hold.
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "collective not found" })).toBeInTheDocument();
      expect(screen.getByRole("link", { name: "back to collectives" })).toHaveAttribute("href", "/collectives");
    });
  });
});
