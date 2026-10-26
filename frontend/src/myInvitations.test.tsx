import { describe, expect, it } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import type { VillageCollectiveInvitation } from "@peasant-labs/schema";
import {
  assertCaseNames,
  installSurfacesREST,
  installSurfacesTeardown,
  readFixture,
  renderCollectivesPage,
  tableRows,
  textOf,
  worldFrom,
} from "@/test/mountedCollectivesSurfaces";
import { profileOwner } from "@/test/publicOwnerFixture";
import { replacedRoutes } from "@/test/nextNavigationMock";

/**
 * The invitations block on the real `/collectives` route, driven by
 * `src/testdata/my-invitations.yaml`: what each invitation shows, the consent
 * an accept asks for, decline, and the invitation link an email carries. Every
 * write the page sends is asserted by route, so a dialog that only looked
 * right could not pass.
 */

interface FixtureInvitation {
  id: string;
  collective: string;
  mode: VillageCollectiveInvitation["collective"]["acceptance_mode"];
  by: string;
}

interface InvitationCase {
  name: string;
  why: string;
  discoverable: boolean;
  open: string[];
  search: string;
  action: { kind: "none" } | { kind: "accept" | "accept-gone" | "decline"; invitation: string };
  expect: {
    rows: string[];
    dialog: { title: string; intro: string; consent: boolean } | null;
    writes: string[];
    notice: string | null;
    member: string | null;
  };
}

const fixture = readFixture("my-invitations.yaml");
const known = fixture.invitations as FixtureInvitation[];
const cases = fixture.cases as InvitationCase[];
assertCaseNames("my-invitations.yaml cases", cases.map((c) => c.name), [
  "no-invitations-draw-nothing",
  "each-invitation-names-its-collective-and-sender",
  "a-hidden-person-consents-before-accepting",
  "a-discoverable-person-skips-the-reveal",
  "a-hidden-inviter-is-an-owner-of-the-collective",
  "declining-is-one-click-and-tells-no-one",
  "an-invitation-link-opens-its-dialog",
  "a-link-to-an-invitation-no-longer-open-says-so",
  "an-invitation-gone-meanwhile-is-refreshed-away",
]);

function wire(row: FixtureInvitation): VillageCollectiveInvitation {
  return {
    id: row.id,
    collective: { id: `c-${row.id}`, name: row.collective, description: null, acceptance_mode: row.mode },
    invited_by: row.by === "anonymous" ? { kind: "anonymous" } : profileOwner({ id: `user-${row.by}`, github_username: row.by }),
    created_at: "2026-10-20T12:00:00Z",
    expires_at: "2099-11-19T12:00:00Z",
  };
}

function invitationRows(): string[] {
  const block = screen.queryByTestId("my-invitations");
  if (!block) return [];
  return within(block)
    .getAllByRole("row")
    .slice(1)
    .map((tr) => {
      const cells = tr.querySelectorAll("td");
      return `${textOf(cells[0])} | ${textOf(cells[1])}`;
    });
}

installSurfacesTeardown();

describe("the invitations on /collectives", () => {
  it.each(cases.map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    window.history.replaceState(null, "", `/collectives${c.search}`);
    const world = worldFrom(
      { invitations: known.filter((row) => c.open.includes(row.id)).map(wire) },
      { viewer: { id: "user-viewer", username: "alice-dev", discoverable: c.discoverable } },
    );
    const requests = installSurfacesREST(world);
    await renderCollectivesPage();
    // The list has arrived once the page has read it.
    await waitFor(() => expect(requests.some((r) => r.path === "/users/me/invitations")).toBe(true));

    const action = c.action;
    if (action.kind === "decline") {
      const name = known.find((row) => row.id === action.invitation)!.collective;
      fireEvent.click(await screen.findByRole("button", { name: `decline the invitation to ${name}` }));
    }
    if (action.kind === "accept" || action.kind === "accept-gone") {
      const name = known.find((row) => row.id === action.invitation)!.collective;
      fireEvent.click(await screen.findByRole("button", { name: `accept the invitation to ${name}` }));
    }

    if (c.expect.dialog) {
      const dialog = await screen.findByRole("dialog", { name: c.expect.dialog.title });
      expect(textOf(dialog), c.why).toContain(c.expect.dialog.intro);
      const consent = within(dialog).queryByRole("checkbox");
      expect(consent !== null, `${c.name}: whether the reveal needs consent`).toBe(c.expect.dialog.consent);
      // Opening the dialog sends nothing.
      expect(requests.filter((r) => r.method !== "GET")).toEqual([]);
      if (action.kind === "accept" || action.kind === "accept-gone") {
        if (action.kind === "accept-gone") world.invitations = [];
        if (consent) fireEvent.click(consent);
        await act(async () => {
          fireEvent.click(within(dialog).getByRole("button", { name: c.expect.dialog!.consent ? "reveal profile & join" : "join" }));
        });
      }
    } else {
      expect(screen.queryByRole("dialog")).toBeNull();
    }

    await waitFor(() =>
      expect(requests.filter((r) => r.method !== "GET").map((r) => `${r.method} ${r.path}`), c.why).toEqual(c.expect.writes),
    );
    if (c.expect.notice) {
      await waitFor(() => expect(textOf(screen.getByTestId("invitations-notice"))).toContain(c.expect.notice));
    } else {
      expect(screen.queryByTestId("invitations-notice")).toBeNull();
    }
    await waitFor(() => expect(invitationRows(), c.why).toEqual(c.expect.rows));
    if (c.expect.member) {
      await waitFor(() => expect(tableRows().map((row) => row.name)).toContain(c.expect.member));
    }
    if (c.search.includes("invitation=")) {
      // The link's parameter stays while its dialog or notice is open, and is
      // dropped once the person acts on it.
      expect(replacedRoutes).toEqual([]);
      if (c.expect.dialog) {
        fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: /cancel/i }));
      } else {
        fireEvent.click(within(screen.getByTestId("invitations-notice")).getByRole("button", { name: /close|dismiss/i }));
      }
      expect(replacedRoutes).toEqual(["/collectives"]);
    }
  });
});
