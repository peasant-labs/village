import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import {
  collectiveNameFor,
  collectiveRows,
  installCollectivesRouteREST,
  installCollectivesRouteTeardown,
  loadCollectiveBadgeFixtures,
  renderCollectivesRoute,
  standingTextFor,
} from "@/test/mountedCollectivesRoute";

/**
 * Mounts the REAL `/groups` collectives route, with the design system's real
 * `CollectivesView` rendering the table, to prove three things a person on that
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
 * shaping, and the table rendering are all the production path.
 */

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn() }),
}));

installCollectivesRouteTeardown();

const { rows } = loadCollectiveBadgeFixtures();

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
    // pass the per-row checks above, so the row count is asserted against the
    // fixture the page was served, not against a fixed number.
    expect(collectiveRows()).toHaveLength(rows.length);

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

      const standing = standingTextFor(row);

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

  it("says nothing at all about a collective the caller only sees", async () => {
    installCollectivesRouteREST(rows);
    await renderCollectivesRoute();

    const bare = rows.find((r) => r.role === null && !r.expect.contributed_badge);
    if (!bare) throw new Error("the corpus no longer carries a row with neither badge");

    expect(
      standingTextFor(bare),
      "a collective the caller can merely see must make no claim about them",
    ).toBe("");
  });
});
