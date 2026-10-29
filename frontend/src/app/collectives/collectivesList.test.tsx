import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import {
  assertCaseNames,
  installSurfacesREST,
  installSurfacesTeardown,
  readFixture,
  renderCollectivesPage,
  tableRows,
  textOf,
  worldFrom,
  type TableRow,
  type WorldCollective,
} from "@/test/mountedCollectivesSurfaces";

/**
 * The real `/collectives` route, driven by `src/testdata/collectives-list.yaml`:
 * the collectives a person belongs to, the empty state, and the way in for a
 * signed-out visitor. Only HTTP is stubbed.
 */

interface ListCase {
  name: string;
  why: string;
  signedIn: boolean;
  show?: string[];
  expect: {
    rows?: TableRow[];
    empty?: { title: string; newCollective: boolean; discoverHref: string };
    signIn?: string;
  };
}

const fixture = readFixture("collectives-list.yaml");
const visible = fixture.visible as WorldCollective[];
const contributed = fixture.contributed as string[];
const cases = fixture.cases as ListCase[];
assertCaseNames("collectives-list.yaml", cases.map((c) => c.name), [
  "the-table-lists-only-the-collectives-you-belong-to",
  "a-person-in-no-collective-sees-the-empty-state",
  "a-signed-out-visitor-is-offered-sign-in",
]);

installSurfacesTeardown();

describe("the collectives list", () => {
  it.each(cases.map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    const shown = c.show ? visible.filter((row) => c.show!.includes(row.id)) : visible;
    installSurfacesREST(worldFrom({ signedIn: c.signedIn, visible: shown.map((row) => ({ ...row })), contributed }));
    await renderCollectivesPage();

    if (c.expect.rows) {
      // Wait for the real rows; role remains visible without contribution tags.
      await waitFor(() => expect(tableRows(), c.why).toEqual(c.expect.rows!.map((row) => ({ ...row, href: expect.any(String) }))));
      for (const row of tableRows()) {
        const id = visible.find((v) => v.name === row.name)?.id;
        expect(row.href).toBe(`/collectives/${id}`);
      }
      // Chrome is lowercase: the column headers and the head's actions.
      const headers = within(screen.getByRole("table", { name: "collectives you belong to" }))
        .getAllByRole("columnheader")
        .map(textOf);
      expect(headers).toEqual(["collective", "your role", "who can publish", "members", "transcripts"]);
      expect(screen.getByRole("link", { name: "discover" })).toHaveAttribute("href", "/discover");
      expect(screen.getByRole("button", { name: "new collective" })).toBeInTheDocument();
    }

    if (c.expect.empty) {
      const empty = await screen.findByTestId("collectives-empty");
      expect(within(empty).getByRole("heading", { name: c.expect.empty.title })).toBeInTheDocument();
      expect(within(empty).getByRole("button", { name: "new collective" })).toBeInTheDocument();
      expect(within(empty).getByRole("link", { name: "discover collectives" })).toHaveAttribute(
        "href",
        c.expect.empty.discoverHref,
      );
      expect(screen.queryByRole("table")).not.toBeInTheDocument();
    }

    if (c.expect.signIn) {
      const signedOut = await screen.findByTestId("collectives-signed-out");
      expect(within(signedOut).getByRole("link", { name: "sign in" })).toHaveAttribute("href", c.expect.signIn);
      expect(screen.queryByRole("button", { name: "new collective" })).not.toBeInTheDocument();
    }
  });
});
