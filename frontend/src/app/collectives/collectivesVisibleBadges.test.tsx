import { describe, expect, it } from "vitest";
import { cleanup } from "@testing-library/react";
import {
  collectiveNameFor,
  installCollectivesRouteREST,
  installCollectivesRouteTeardown,
  listedNames,
  loadCollectiveBadgeFixtures,
  pageFor,
  renderCollectivePage,
  standingTextFor,
} from "@/test/mountedCollectivesRoute";

/**
 * Mounts the REAL `/collectives` and `/discover` routes to prove four things a
 * person depends on:
 *
 *  1. Every collective they may SEE is reachable: the list shows the ones they
 *     belong to, discover shows the rest, and no collective is on both or on
 *     neither. The list used to hold every visible collective; splitting it
 *     must not lose one.
 *  2. A collective they belong to says so on the list, with their role.
 *  3. A collective that currently holds or is still reviewing something of
 *     theirs says "contributed", on whichever page lists it.
 *  4. A collective they merely see claims nothing about them.
 *
 * Nothing here is stubbed above the network: the pages, their hooks and the
 * rendering are all the production path.
 */

installCollectivesRouteTeardown();

const { rows } = loadCollectiveBadgeFixtures();

describe("the mounted collectives and discover routes", () => {
  it("split every collective the caller may see between them, each exactly once", async () => {
    installCollectivesRouteREST(rows);
    await renderCollectivePage("collectives");
    const onList = listedNames("collectives");
    cleanup();

    installCollectivesRouteREST(rows);
    await renderCollectivePage("discover");
    const onDiscover = listedNames("discover");

    for (const row of rows) {
      const name = collectiveNameFor(row);
      const expected = pageFor(row);
      expect(onList.includes(name), `${row.name} on the list: ${row.why}`).toBe(expected === "collectives");
      expect(onDiscover.includes(name), `${row.name} on discover: ${row.why}`).toBe(expected === "discover");
    }
    // Between them, the two pages show exactly the visible set, once each.
    expect([...onList, ...onDiscover].sort()).toEqual(rows.map(collectiveNameFor).sort());
    // The split is the point: the corpus must exercise both sides of it.
    expect(rows.some((r) => r.role === null)).toBe(true);
    expect(rows.some((r) => r.role !== null)).toBe(true);
  });

  it.each(rows.map((row) => [row.name, row] as const))("shows the right badges on the %s row", async (_name, row) => {
    installCollectivesRouteREST(rows);
    await renderCollectivePage(pageFor(row));

    const standing = standingTextFor(row);

    if (row.expect.member_badge === null) {
      expect(standing, `${row.name} must claim no membership: ${row.why}`).not.toContain("member");
      expect(standing, `${row.name} must claim no membership: ${row.why}`).not.toContain("owner");
    } else {
      expect(standing, `${row.name} must show the caller's role: ${row.why}`).toContain(row.expect.member_badge);
    }

    if (row.expect.contributed_badge) {
      expect(standing, `${row.name} must say it holds a contribution: ${row.why}`).toContain("contributed");
    } else {
      expect(standing, `${row.name} must not claim a contribution: ${row.why}`).not.toContain("contributed");
    }
  });

  it("says nothing at all about a collective the caller only sees", async () => {
    installCollectivesRouteREST(rows);
    await renderCollectivePage("discover");

    const bare = rows.find((r) => r.role === null && !r.expect.contributed_badge);
    if (!bare) throw new Error("the corpus no longer carries a row with neither badge");

    expect(standingTextFor(bare), "a collective the caller can merely see must make no claim about them").toBe("");
  });
});
