import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import {
  collectiveNameFor,
  emptyStandingSelectors,
  installCollectivesRouteREST,
  installCollectivesRouteTeardown,
  loadCollectiveBadgeFixtures,
  renderCollectivesRoute,
  type CollectiveBadgeRow,
} from "@/test/mountedCollectivesRoute";

/**
 * Mounts the REAL `/groups` collectives route, with the design system's real
 * `CollectivesView` rendering the cards, to prove three things a person on that
 * page depends on:
 *
 *  1. Every collective they may SEE is listed, not only the ones they belong
 *     to. Before this change the page asked the membership-only list, so a
 *     person in one collective saw exactly one row however many were open to
 *     them.
 *  2. A row they belong to says so, and says which role they hold.
 *  3. A row this collective currently holds or is still reviewing something of
 *     theirs says "contributed", whether or not they are a member.
 *
 * Nothing here is stubbed above the network: the page, its hooks, the payload
 * shaping, and the card rendering are all the production path.
 */

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn() }),
}));

installCollectivesRouteTeardown();

const { rows } = loadCollectiveBadgeFixtures();

/** The card the design system renders for one fixture row. */
function cardFor(row: CollectiveBadgeRow): HTMLElement {
  const name = screen.getByText(collectiveNameFor(row));
  const card = name.closest("button");
  if (!card) {
    throw new Error(`the collective "${collectiveNameFor(row)}" is on the page but not inside a card`);
  }
  return card;
}

/**
 * The text of the card's standing slot: what the row claims about the caller.
 * Read from the slot rather than from the whole card, because the card's
 * member COUNT ("4 members") would otherwise be mistaken for a member badge.
 */
function standingText(card: HTMLElement): string {
  return (card.querySelector(".cmg-col-role")?.textContent ?? "").trim();
}

describe("the mounted collectives route", () => {
  it("lists every collective the caller may see, not only their memberships", async () => {
    installCollectivesRouteREST(rows);
    await renderCollectivesRoute();

    for (const row of rows) {
      expect(
        screen.getByText(collectiveNameFor(row)),
        `${row.name} must be listed: ${row.why}`,
      ).toBeInTheDocument();
    }

    // The page shows ONE list. A split into "yours" and "others" would still
    // pass the per-row checks above, so the card count is asserted against the
    // fixture the page was served, not against a fixed number.
    expect(document.querySelectorAll(".cmg-col-card")).toHaveLength(rows.length);

    // Non-membership is the whole point of the change: at least one listed row
    // must be a collective the caller does not belong to, or this test could
    // pass against the old membership-only list.
    expect(rows.some((r) => r.role === null)).toBe(true);
  });

  it.each(rows.map((row) => [row.name, row] as const))(
    "shows the right badges on the %s row",
    async (_name, row) => {
      installCollectivesRouteREST(rows);
      await renderCollectivesRoute();

      const standing = standingText(cardFor(row));

      if (row.expect.member_badge === null) {
        expect(standing, `${row.name} must claim no membership: ${row.why}`).not.toContain("member");
        expect(standing, `${row.name} must claim no membership: ${row.why}`).not.toContain("owner");
      } else {
        expect(standing, `${row.name} must show the caller's role: ${row.why}`).toContain(
          row.expect.member_badge,
        );
      }

      if (row.expect.contributed_badge) {
        expect(standing, `${row.name} must say it holds a contribution: ${row.why}`).toContain(
          "contributed",
        );
      } else {
        expect(standing, `${row.name} must not claim a contribution: ${row.why}`).not.toContain(
          "contributed",
        );
      }
    },
  );

  it("keeps the stylesheet's collapse rule pointed at the real card", async () => {
    installCollectivesRouteREST(rows);
    await renderCollectivesRoute();

    const bare = rows.find((r) => r.expect.member_badge === null && !r.expect.contributed_badge);
    if (!bare) throw new Error("the corpus no longer carries a row with neither badge");
    const card = cardFor(bare);

    // The shipped rule hides the empty standing slot AND the separator after
    // it. Both selectors must still find their element on this card, or the
    // rule has gone inert against a changed design system and the row has its
    // stray leading separator back.
    for (const selector of emptyStandingSelectors()) {
      expect(
        card.querySelector(selector),
        `the stylesheet collapses "${selector}", but nothing on a card with no standing matches it any ` +
          "more, so the stray leading separator is back",
      ).not.toBeNull();
    }
  });

  it("says nothing at all about a collective the caller only sees", async () => {
    installCollectivesRouteREST(rows);
    await renderCollectivesRoute();

    const bare = rows.find((r) => r.role === null && !r.expect.contributed_badge);
    if (!bare) throw new Error("the corpus no longer carries a row with neither badge");

    expect(
      standingText(cardFor(bare)),
      "a collective the caller can merely see must make no claim about them",
    ).toBe("");
  });
});
