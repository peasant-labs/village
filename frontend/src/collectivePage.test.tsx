import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { pullRequestCells } from "@/lib/adapters/collective";
import type { GroupTranscript } from "@/lib/types";
import { pushedRoutes } from "@/test/nextNavigationMock";
import { loadCollectivePageFixtures, loadCollectiveRecoveryFixtures } from "@/test/collectivePageFixtures";
import {
  installCollectivePagesTeardown,
  installCollectiveREST,
  mountCollectivePage,
  renderCollectivePage,
  renderCollectivesList,
  renderCollectiveSettings,
  textOf,
} from "@/test/mountedCollectivePages";

/**
 * The collective page, mounted through its real route, driven by
 * `src/testdata/collective-page.yaml`, and the read and create recovery of the
 * collective pages. The collectives list's own cases are in
 * `src/app/collectives/collectivesList.test.tsx`. Only HTTP is stubbed.
 */

installCollectivePagesTeardown();

const fixtures = loadCollectivePageFixtures();

/** The policy chips, as `rule: words` in the order they are drawn. */
function policyChips(): Record<string, string> {
  const list = screen.getByRole("list", { name: "how this collective works" });
  return Object.fromEntries(
    within(list)
      .getAllByRole("listitem")
      .map((item) => {
        const text = textOf(item);
        const split = text.indexOf(": ");
        return [text.slice(0, split), text.slice(split + 2)];
      }),
  );
}

/** The head's action row, in order, by accessible name. */
function headerActions(): string[] {
  const bar = document.querySelector('[data-testid="collective-header-actions"]');
  return bar ? [...bar.querySelectorAll("button, a")].map((control) => control.getAttribute("aria-label") ?? textOf(control)) : [];
}

/** Selects a tab by its label and returns its panel, which must then be shown. */
function openTab(label: string): HTMLElement {
  const tab = screen.getByRole("tab", { name: new RegExp(`^${label}`) });
  fireEvent.click(tab);
  expect(tab).toHaveAttribute("aria-selected", "true");
  const panel = document.getElementById(tab.getAttribute("aria-controls") ?? "");
  if (!panel) throw new Error(`the ${label} tab controls no panel`);
  expect(panel).not.toHaveAttribute("hidden");
  return panel;
}

function transcriptRows(): number {
  const table = screen.queryByRole("table", { name: /^transcripts/ });
  return table ? table.querySelectorAll("tbody tr a.is-title").length : 0;
}

describe("the collective page", () => {
  it.each(fixtures.cases.map((c) => [c.name, c] as const))("%s", async (_name, pageCase) => {
    const world = fixtures.worldFor(pageCase);
    const requests = installCollectiveREST(world);
    await renderCollectivePage(world);
    const expected = pageCase.expect;

    expect(policyChips(), pageCase.why).toEqual({
      "who can publish": expected.whoCanPublish,
      "who can read": expected.whoCanRead,
      "your role": expected.yourRole,
    });
    // The actions wait on the viewer and the repository reads, which land after the page.
    await waitFor(() => expect(headerActions()).toEqual(expected.actions));
    // A curated collective is joined only by an owner's invitation; anybody
    // without a role there is told so where a join button would be.
    expect(screen.queryByTestId("collective-joins-by-invitation") !== null, pageCase.why).toBe(
      world.group.acceptance_mode === "curated" && pageCase.role === "",
    );
    expect(screen.getAllByRole("tab").map((tab) => textOf(tab).replace(/\s*[\d,]+$/, ""))).toEqual(expected.tabs);
    expect(screen.getByRole("tab", { name: /^transcripts/ })).toHaveAttribute("aria-selected", "true");

    // The transcripts tab: the table (with a status column only where approval
    // is a step), or the restricted state when the server withholds the rows.
    expect(transcriptRows()).toBe(expected.transcriptRows);
    const table = screen.queryByRole("table", { name: /^transcripts/ });
    if (table) {
      const headers = within(table).getAllByRole("columnheader").map((th) => textOf(th));
      // No status column: the list holds only approved transcripts.
      expect(headers).toEqual(["transcript", "contributor", "agent", "turns", "when"]);
      // Each row also states its contributor, agent and turns under its title,
      // the line a phone shows in place of those three columns.
      for (const row of within(table).getAllByRole("row").slice(1)) {
        const cells = row.querySelectorAll("td");
        const facts = row.querySelector('[data-testid="collective-row-facts"]');
        expect(facts, "a row states its facts under its title").not.toBeNull();
        expect(textOf(facts!)).toContain(textOf(cells[1]));
        expect(textOf(facts!)).toContain(textOf(cells[2]));
        if (textOf(cells[3])) expect(textOf(facts!)).toContain(`${textOf(cells[3])} turn`);
      }
    }
    if (!pageCase.canRead) expect(screen.getByRole("heading", { name: "data access restricted" })).toBeInTheDocument();

    if (expected.actions.includes("sign in to join")) {
      const signIn = screen.getByRole("link", { name: "sign in to join" });
      expect(signIn).toHaveAttribute("href", `/login?next=${encodeURIComponent(`/collectives/${world.group.id}`)}`);
      // Nothing the page reads with no account needs one.
      expect(requests.filter((r) => r.path.includes("/repositories") || r.path.includes("/my-shares") || r.path.includes("/pending"))).toEqual([]);
    }
    if (expected.actions.includes("settings")) {
      expect(screen.getByRole("link", { name: "settings" })).toHaveAttribute("href", `/collectives/${world.group.id}/settings`);
      expect(screen.getByRole("link", { name: "invite people" })).toHaveAttribute(
        "href",
        `/collectives/${world.group.id}/settings?section=members`,
      );
    }

    // The members tab.
    const members = openTab("members");
    if (expected.memberBreakdown === null) {
      expect(within(members).getByText("the owner keeps the member list private.")).toBeInTheDocument();
      expect(within(members).queryByRole("list", { name: "members" })).toBeNull();
    } else {
      expect(textOf(within(members).getByTestId("collective-member-breakdown"))).toBe(expected.memberBreakdown);
      expect(within(members).getAllByRole("listitem")).toHaveLength(expected.memberCount);
      expect(textOf(screen.getByRole("tab", { name: /^members/ }))).toBe(`members ${expected.memberCount}`);
    }

    // The stats tab: the numbers, and the github org line.
    const stats = openTab("stats");
    const numbers = within(stats).getByLabelText(`${world.group.name} in numbers`);
    const stat = Object.fromEntries([...numbers.querySelectorAll(".on-stat")].map((row) => [textOf(row.querySelector("dt")), textOf(row.querySelector("dd"))]));
    expect(stat).toEqual({
      transcripts: String(world.totalTranscripts ?? world.transcripts.length),
      members: String(expected.memberCount),
      tokens: "1.2M",
      "pull requests linked": "7",
    });
    await waitFor(() => expect(textOf(within(stats).getByTestId("collective-org-line").querySelector(".on-orgline-text"))).toBe(expected.orgLine));
    expect(within(stats).queryByRole("button", { name: "manage repositories" }) !== null, "manage").toBe(expected.manage);

    const more = screen.queryByRole("button", { name: "more" });
    if (expected.overflow.length === 0) {
      expect(more).toBeNull();
      return;
    }
    expect(more).not.toBeNull();
    for (const item of expected.overflow) {
      fireEvent.click(screen.getByRole("button", { name: "more" }));
      fireEvent.click(await screen.findByRole("menuitem", { name: item.label }));
      expect(pushedRoutes.at(-1)).toBe(item.route.replace("{id}", world.group.id));
    }
    fireEvent.click(screen.getByRole("button", { name: "more" }));
    expect(screen.getAllByRole("menuitem").map((row) => textOf(row))).toEqual(expected.overflow.map((item) => item.label));
  });

  it("joins an open collective with the join route", async () => {
    const pageCase = fixtures.cases.find((c) => c.name === "a-non-member-of-an-open-collective-can-join");
    if (!pageCase) throw new Error("the corpus no longer carries the open collective a non-member can join");
    const world = fixtures.worldFor(pageCase);
    const requests = installCollectiveREST(world);
    await renderCollectivePage(world);

    fireEvent.click(await screen.findByRole("button", { name: "join" }));

    await waitFor(() => {
      expect(requests.filter((r) => r.method !== "GET")).toEqual([
        { method: "POST", path: `/collectives/${world.group.id}/join`, body: null },
      ]);
    });
  });

  it("says why a join failed", async () => {
    const pageCase = fixtures.cases.find((c) => c.name === "a-non-member-of-an-open-collective-can-join");
    if (!pageCase) throw new Error("the corpus no longer carries the open collective a non-member can join");
    const world = fixtures.worldFor(pageCase);
    world.failures = [{ method: "POST", path: `/collectives/${world.group.id}/join`, status: 403, error: "joining is closed" }];
    installCollectiveREST(world);
    await renderCollectivePage(world);

    fireEvent.click(await screen.findByRole("button", { name: "join" }));

    const notice = await screen.findByTestId("collective-notice");
    expect(textOf(notice.querySelector(".fb-toast-title"))).toBe("could not join");
    expect(textOf(notice.querySelector(".fb-toast-msg"))).toBe("joining is closed");
  });

  it("shows more transcripts by asking for a longer first page", async () => {
    const pageCase = fixtures.cases.find((c) => c.role === "owner" && c.canRead);
    if (!pageCase) throw new Error("the corpus no longer carries an owner who can read");
    const world = { ...fixtures.worldFor(pageCase), totalTranscripts: 30 };
    const requests = installCollectiveREST(world);
    await renderCollectivePage(world);

    expect(textOf(document.querySelector(".on-pager > span"))).toBe(`showing ${world.transcripts.length} of 30`);
    expect(textOf(screen.getByRole("tab", { name: /^transcripts/ }))).toBe("transcripts 30");
    fireEvent.click(screen.getByRole("button", { name: "show more" }));

    await waitFor(() => {
      expect(requests.map((r) => r.path)).toContain(`/collectives/${world.group.id}?limit=40&offset=0`);
    });
  });

  it("waits for the session before it says anything about the collective", async () => {
    const pageCase = fixtures.cases.find((c) => c.role === "owner" && c.canRead);
    if (!pageCase) throw new Error("the corpus no longer carries an owner who can read");
    const world = fixtures.worldFor(pageCase);
    installCollectiveREST(world);
    // Hold the session read: the collective is not asked for until the viewer is known.
    const delegate = globalThis.fetch;
    let release!: () => void;
    const held = new Promise<void>((resolve) => { release = resolve; });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith("/auth/me")) await held;
      return delegate(input, init);
    }));
    await mountCollectivePage(world);

    expect(screen.getByTestId("collective-loading")).toBeInTheDocument();
    expect(screen.queryByText("collective not found")).toBeNull();
    await act(async () => { release(); });
    const title = await screen.findByRole("heading", { level: 1, name: world.group.name });
    expect(title).toBeInTheDocument();
    // The title is the collective's name alone; its colour lives in the sidebar.
    expect(title.querySelector(".on-swatch")).toBeNull();
    expect(screen.queryByText("collective not found")).toBeNull();
  });

  it("links each pull request a transcript is attached to", async () => {
    const pageCase = fixtures.cases.find((c) => c.role === "owner" && c.canRead);
    if (!pageCase) throw new Error("the corpus no longer carries an owner who can read");
    const world = fixtures.worldFor(pageCase);
    installCollectiveREST(world);
    await renderCollectivePage(world);

    const table = screen.getByRole("table", { name: /^transcripts/ });
    const [owner, name] = world.transcripts[0].pullRequests[0].split("#")[0].split("/");
    for (const label of world.transcripts[0].pullRequests) {
      const number = label.split("#")[1];
      expect(within(table).getByRole("link", { name: `#${number}` })).toHaveAttribute("href", `/pulls/${owner}/${name}/${number}`);
    }
    expect(within(table).getByRole("link", { name: world.transcripts[0].title })).toHaveAttribute(
      "href",
      `/transcripts/${world.transcripts[0].id}?in=collective:${world.group.id}`,
    );
  });
});

describe("the collective page's tabs", () => {
  it.each(fixtures.tabCases.map((c) => [c.name, c] as const))("%s", async (_name, tabCase) => {
    const world = fixtures.tabWorld(tabCase);
    window.history.replaceState(null, "", `/collectives/${world.group.id}${tabCase.search}`);
    const requests = installCollectiveREST(world);
    await renderCollectivePage(world);

    for (const key of tabCase.keys) {
      const current = screen.getAllByRole("tab").find((tab) => tab.getAttribute("aria-selected") === "true")!;
      current.focus();
      fireEvent.keyDown(current, { key });
    }

    const selected = screen.getAllByRole("tab").filter((tab) => tab.getAttribute("aria-selected") === "true");
    expect(selected, tabCase.why).toHaveLength(1);
    expect(selected[0].id).toContain(`-tab-${tabCase.expectSelected}`);
    expect(selected[0]).toHaveAttribute("tabindex", "0");
    if (tabCase.keys.length > 0) expect(selected[0]).toHaveFocus();
    // Exactly one panel is shown, and it is the selected tab's.
    const shown = screen.getAllByRole("tabpanel");
    expect(shown).toHaveLength(1);
    expect(shown[0]).toHaveAttribute("aria-labelledby", selected[0].id);
    expect(window.location.search).toBe(tabCase.expectSearch);

    const asksForQueue = requests.some((r) => r.path === `/collectives/${world.group.id}/pending`);
    expect(asksForQueue, "only a curated collective's owner reads the queue").toBe(
      world.role === "owner" && world.group.acceptance_mode === "curated",
    );
    if (tabCase.expectSelected === "waiting") {
      await waitFor(() => expect(textOf(screen.getByRole("tab", { name: /^waiting/ }))).toBe(`waiting for approval ${world.pending!.length}`));
      const queue = screen.getByRole("table", { name: /^waiting for approval/ });
      const rows = within(queue).getAllByRole("row").slice(1);
      expect(rows.map((row) => textOf(row.querySelector("a.is-title")))).toEqual(world.pending!.map((row) => row.title));
      expect(rows.map((row) => textOf(row.querySelector(".on-handle-cell")))).toEqual(world.pending!.map((row) => (row.owner.kind === "profile" ? `@${row.owner.username}` : "anon")));
      for (const row of rows) expect(textOf(row.querySelector(".on-tag"))).toBe("waiting");
      expect(screen.getByRole("link", { name: "review transcripts" })).toHaveAttribute("href", `/collectives/${world.group.id}/review`);
    }
  });

  it("says when nothing waits for approval", async () => {
    const tabCase = fixtures.tabCases.find((c) => c.name === "the-curated-owner-opens-waiting-from-the-address");
    if (!tabCase) throw new Error("the corpus no longer opens the waiting tab");
    const world = { ...fixtures.tabWorld(tabCase), pending: [] };
    window.history.replaceState(null, "", `/collectives/${world.group.id}${tabCase.search}`);
    installCollectiveREST(world);
    await renderCollectivePage(world);

    expect(await screen.findByRole("heading", { name: "nothing is waiting for approval" })).toBeInTheDocument();
    expect(screen.queryByRole("table", { name: /^waiting for approval/ })).toBeNull();
  });
});

describe("the pull request cells", () => {
  it.each(fixtures.pullRequestCases.map((c) => [c.name, c] as const))("%s", (_name, prCase) => {
    const transcripts = prCase.rows.map(
      (row) =>
        ({
          id: row.id,
          pull_requests: {
            count: row.count,
            recent: row.pullRequests.map((label) => {
              const [repo, number] = label.split("#");
              const [owner, name] = repo.split("/");
              return { owner, name, number: Number(number) };
            }),
          },
        }) as GroupTranscript,
    );
    const cells = pullRequestCells(transcripts);

    expect(
      prCase.expect.map((row) => ({ id: row.id, ...cells.byTranscript.get(row.id) })),
      prCase.why,
    ).toEqual(prCase.expect);
    expect(Object.fromEntries(cells.hrefs)).toEqual(prCase.hrefs);
  });
});


describe("collective read and create recovery", () => {
  it.each(loadCollectiveRecoveryFixtures().map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    const world = fixtures.listWorld();
    world.role = "owner";
    world.canRead = true;
    const path = c.operation === "read"
      ? c.surface === "list" ? "/collectives/visible" : `/collectives/${world.group.id}`
      : "/collectives";
    world.failures = c.error ? [{ method: c.operation === "read" ? "GET" : "POST", path, status: 503, error: c.error }] : [];
    const requests = installCollectiveREST(world);
    if (c.surface === "detail") await renderCollectivePage(world, true);
    else if (c.surface === "settings") await renderCollectiveSettings(world, true);
    else await renderCollectivesList();

    if (c.operation === "read") {
      expect(await screen.findByRole("alert")).toHaveTextContent(c.error!);
      expect(screen.queryByText("collective not found")).not.toBeInTheDocument();
      // The failed read keeps one h1, the page's own: the collective's name
      // when the app already holds it ("collective" when the failed read was
      // the one that would have named it), or a settings page's section.
      if (c.surface !== "list") {
        const h1s = screen.getAllByRole("heading", { level: 1 }).map((h) => h.textContent);
        expect(h1s).toEqual([c.surface === "settings" ? "general" : "collective"]);
      }
      const before = requests.filter((r) => r.path === path && r.method === "GET").length;
      expect(before).toBeGreaterThan(0);
      world.failures = [];
      fireEvent.click(screen.getByRole("button", { name: "try again" }));
      await waitFor(() => {
        expect(requests.filter((r) => r.path === path && r.method === "GET").length).toBeGreaterThan(before);
        expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      });
      if (c.surface === "list") expect(screen.getByRole("table", { name: /^collectives/ })).toBeInTheDocument();
      else if (c.surface === "detail") expect(screen.getByRole("heading", { level: 1, name: world.group.name })).toBeInTheDocument();
      else expect(screen.getByRole("navigation", { name: "settings sections" })).toBeInTheDocument();
      return;
    }

    // The new-collective dialog: name, then access, then create on the invite step.
    fireEvent.click(screen.getByRole("button", { name: "new collective" }));
    const dialog = await screen.findByRole("dialog", { name: "new collective" });
    fireEvent.change(within(dialog).getByRole("textbox", { name: "name" }), { target: { value: world.group.name } });
    fireEvent.click(within(dialog).getByRole("button", { name: "next" }));
    fireEvent.click(await within(dialog).findByRole("button", { name: "next" }));
    fireEvent.click(await within(dialog).findByRole("button", { name: "create collective" }));
    if (c.operation === "create-retry") {
      expect(await within(dialog).findByRole("alert")).toHaveTextContent(c.error!);
      expect(within(dialog).queryByRole("link", { name: `open ${world.group.name}` })).not.toBeInTheDocument();
      expect(requests.filter((r) => r.method === "POST" && r.path === "/collectives")).toHaveLength(1);
      // The form is kept: back to the first step, the name is still there.
      fireEvent.click(within(dialog).getByRole("button", { name: "back" }));
      fireEvent.click(await within(dialog).findByRole("button", { name: "back" }));
      expect(await within(dialog).findByRole("textbox", { name: "name" })).toHaveValue(world.group.name);
      fireEvent.click(within(dialog).getByRole("button", { name: "next" }));
      fireEvent.click(await within(dialog).findByRole("button", { name: "next" }));
      world.failures = [];
      fireEvent.click(await within(dialog).findByRole("button", { name: "create collective" }));
    }
    // The returned identifier is where "open" leads; nothing navigates on its own.
    expect(await within(dialog).findByRole("link", { name: `open ${world.group.name}` })).toHaveAttribute(
      "href",
      `/collectives/${world.group.id}`,
    );
    expect(pushedRoutes).toEqual([]);
    const writes = requests.filter((r) => r.method === "POST" && r.path === "/collectives");
    expect(writes).toHaveLength(c.operation === "create-retry" ? 2 : 1);
    expect(writes[0].body).toMatchObject({ name: world.group.name });
  });
});
