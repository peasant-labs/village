import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { pullRequestCells } from "@/lib/adapters/collective";
import type { GroupTranscript } from "@/lib/types";
import { pushedRoutes } from "@/test/nextNavigationMock";
import { loadCollectivePageFixtures } from "@/test/collectivePageFixtures";
import {
  installCollectivePagesTeardown,
  installCollectiveREST,
  renderCollectivePage,
  renderCollectivesList,
  textOf,
} from "@/test/mountedCollectivePages";

/**
 * The collective page and the collectives list, each mounted through its real
 * route and rendered by fairtrade's real views, driven by
 * `src/testdata/collective-page.yaml`. Only HTTP is stubbed.
 */

installCollectivePagesTeardown();

const fixtures = loadCollectivePageFixtures();

/** The policy boxes, as `title: text` in the order they are drawn. */
function policyBoxes(): Record<string, string> {
  const list = screen.getByRole("list", { name: "how this collective works" });
  return Object.fromEntries(
    within(list)
      .getAllByRole("listitem")
      .map((item) => [textOf(item.querySelector("h3")), textOf(item.querySelector("p"))]),
  );
}

/** The github org line in the rail, without the manage button's own label. */
function orgLine(): string {
  const rail = screen.getByRole("complementary", { name: "github orgs and members" });
  const line = rail.querySelector(".cmg-rail-box .cmg-rail-line");
  if (!line) return "";
  const copy = line.cloneNode(true) as Element;
  copy.querySelectorAll("button").forEach((button) => button.remove());
  return textOf(copy);
}

function headerActions(): string[] {
  const bar = document.querySelector('[data-testid="collective-header-actions"]');
  return bar ? [...bar.querySelectorAll("button")].map((button) => textOf(button)) : [];
}

function transcriptRows(): number {
  const table = screen.getByRole("table", { name: /^transcripts/ });
  return table.querySelectorAll("tbody tr .iu-session-title").length;
}

describe("the collective page", () => {
  it.each(fixtures.cases.map((c) => [c.name, c] as const))("%s", async (_name, pageCase) => {
    const world = fixtures.worldFor(pageCase);
    installCollectiveREST(world);
    await renderCollectivePage(world);
    const expected = pageCase.expect;

    expect(policyBoxes(), pageCase.why).toEqual({
      "who can read": expected.whoCanRead,
      "who can publish": expected.whoCanPublish,
      "your role": expected.yourRole,
    });

    // The org line waits on the repository reads, which land after the page.
    await waitFor(() => expect(orgLine()).toBe(expected.orgLine));
    await waitFor(() => expect(headerActions()).toEqual(expected.headerActions));
    const rail = screen.getByRole("complementary", { name: "github orgs and members" });
    expect(within(rail).queryByRole("button", { name: "manage" }) !== null, "manage").toBe(expected.manage);
    const membersBox = rail.querySelectorAll(".cmg-rail-box")[1];
    expect(textOf(membersBox?.querySelector(".cmg-rail-title"))).toBe(`members ${expected.memberCount}`);
    expect(textOf(membersBox?.querySelector(".cmg-note"))).toBe(expected.memberBreakdown);
    const stats = screen.getByRole("list", { name: `${world.group.name} in numbers` });
    expect(textOf(stats)).toContain(`${expected.memberCount} members`);

    expect(screen.queryByRole("button", { name: "settings" }) !== null, "settings").toBe(expected.settings);
    expect(transcriptRows()).toBe(expected.transcriptRows);

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
        { method: "POST", path: `/groups/${world.group.id}/join`, body: null },
      ]);
    });
  });

  it("shows more transcripts by asking for a longer first page", async () => {
    const pageCase = fixtures.cases.find((c) => c.role === "owner" && c.canRead);
    if (!pageCase) throw new Error("the corpus no longer carries an owner who can read");
    const world = { ...fixtures.worldFor(pageCase), totalTranscripts: 30 };
    const requests = installCollectiveREST(world);
    await renderCollectivePage(world);

    expect(textOf(document.querySelector(".iu-page-count"))).toBe(`showing ${world.transcripts.length} of 30`);
    fireEvent.click(screen.getByRole("button", { name: "show more" }));

    await waitFor(() => {
      expect(requests.map((r) => r.path)).toContain(`/groups/${world.group.id}?limit=40&offset=0`);
    });
  });
});

describe("the collectives list", () => {
  it.each(fixtures.listCases.map((c) => [c.name, c] as const))("%s", async (_name, listCase) => {
    const world = fixtures.listWorld();
    const requests = installCollectiveREST(world);
    await renderCollectivesList();

    if (listCase.query) {
      fireEvent.change(screen.getByRole("searchbox", { name: "collective or github org" }), {
        target: { value: listCase.query },
      });
      fireEvent.click(screen.getByRole("button", { name: "search" }));
    }

    const table = await screen.findByRole("table", { name: listCase.expectCaption });
    await waitFor(() => {
      const rows = [...table.querySelectorAll("tbody tr")].map((tr) => {
        const cells = tr.querySelectorAll("td");
        return {
          name: textOf(cells[0]?.querySelector(".cmg-table-link")),
          role: textOf(cells[1]?.querySelector(".cmg-cell-stack > span:first-child")),
          org: textOf(cells[4]),
          joining: textOf(cells[5]),
        };
      });
      expect(rows, listCase.why).toEqual(listCase.expectRows);
    });
    if (listCase.query) {
      expect(requests.map((r) => r.path)).toContain(`/groups/search?q=${listCase.query}`);
    }
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
