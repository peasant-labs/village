import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { describe, expect, it } from "vitest";
import {
  installAttachmentSurfacesTeardown,
  installGroupSettingsREST,
  renderGroupSettingsRoute,
} from "@/test/mountedAttachmentSurfaces";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
} from "@/test/mountedProductionRoute";
import { loadHiddenSurfacesFixtures } from "@/test/hiddenSurfacesFixtures";

// Controls hidden while publishing is for collectives only, each asserted on
// the REAL route that used to draw it. Hidden is not deleted: the value a
// collective already holds survives, and nothing here removes a route.

const fixtures = loadHiddenSurfacesFixtures();

installAttachmentSurfacesTeardown();
installMountedRouteTeardown();

const GROUP_ID = "55555555-5555-5555-5555-555555555555";

/** The settings cards' own names, from the page's `whoCanReadChoices`. */
const READ_LABELS: Record<string, string> = {
  members_only: "members",
  contributors: "members and contributors",
  public: "anyone",
};

describe("collective settings: the public data-access option", () => {
  for (const c of fixtures.dataAccessCases) {
    it(c.name, async () => {
      const requests = installGroupSettingsREST({
        id: GROUP_ID,
        ownerUsername: "owner",
        postPromptsCheck: true,
        promptsCheckMode: "informational",
        dataAccess: c.savedDataAccess,
      });
      // Who can read is chosen on the access section.
      window.history.replaceState(null, "", `/collectives/${GROUP_ID}/settings?section=access`);
      await renderGroupSettingsRoute(GROUP_ID);

      const group = await screen.findByRole("group", { name: "who can read" });
      const cards = within(group).getAllByRole("button");
      const name = (card: HTMLElement) => card.querySelector("b")?.textContent?.trim();
      expect(cards.map(name)).toEqual(c.expectOptions.map((value) => READ_LABELS[value]));
      // The collective's own value is shown, with its own name: a set of cards
      // with no card for its value would silently show another one pressed.
      const pressed = cards.filter((card) => card.getAttribute("aria-pressed") === "true");
      expect(pressed.map(name)).toEqual([READ_LABELS[c.savedDataAccess]]);

      // One change writes one field; the server keeps every field the body
      // leaves out.
      const next = c.expectOptions.find((value) => value !== c.savedDataAccess)!;
      fireEvent.click(cards.find((card) => name(card) === READ_LABELS[next])!);
      await waitFor(() => {
        expect(requests.some((r) => r.method === "PATCH")).toBe(true);
      });
      expect(requests.find((r) => r.method === "PATCH")?.body).toMatchObject({
        data_access: next,
      });
      window.history.replaceState(null, "", "/");
    });
  }
});

function sessionDetail(id: string): SessionDetailPayload {
  return {
    id,
    harness: "claude-code",
    startTime: "2026-08-21T09:00:00.000Z",
    endTime: "2026-08-21T09:02:00.000Z",
    durationMins: 2,
    totalTokens: 200,
    tokensIn: 120,
    tokensOut: 80,
    turnCount: 2,
    toolCallCount: 0,
    project: "village",
    model: "anthropic/claude-fable-5",
    turns: [
      { index: 0, role: "user", content: "why is ingest dropping commits?", timestamp: "2026-08-21T09:00:00.000Z", depth: 0 },
      { index: 1, role: "assistant", content: "Looking at the detector now.", timestamp: "2026-08-21T09:01:00.000Z", depth: 0 },
    ],
  };
}

describe("transcript page: the attestation control", () => {
  for (const c of fixtures.attestCases) {
    it(c.name, async () => {
      const transcriptID = `transcript-${c.name}`;
      const viewerID = c.viewerIsOwner ? "fixture-owner" : "fixture-reader";
      const fetchMock = installRESTFixture(
        transcriptID,
        {
          transcript: {
            id: transcriptID,
            local_id: `session-${c.name}`,
            visibility: "public",
            title: "Ingest commit detection",
            description: null,
            project_name: "village",
          },
          owner: { id: "fixture-owner" },
          enriched_shares: [],
          viewer_collectives: [{ id: "c1", name: "Acme Platform" }],
        },
        sessionDetail(`session-${c.name}`),
        "hidden-surfaces",
        {
          id: viewerID,
          github_username: c.viewerIsOwner ? "fixture-owner" : "fixture-reader",
          // A visible org: the one condition under which the control drew.
          orgs: [{ org_login: "acme", org_id: 1, avatar_url: null, visible: true }],
        },
      );
      await renderProductionRoute(transcriptID, "", { signedIn: true });

      // The header action row has drawn for a signed-in viewer, which is where
      // the control sat before it was hidden.
      await screen.findByTestId("transcript-header-actions");
      await waitFor(() => {
        expect(fetchMock.mock.calls.some(([u]) => String(u).endsWith("/auth/me"))).toBe(true);
      });

      // `attest` is the trigger's own name (`new attestation` names the popover
      // it opens, which is never reached without the trigger).
      expect(screen.queryByRole("button", { name: "attest" })).toBeNull();
      // Not drawn, and not asked for: the org read existed only to decide it.
      expect(fetchMock.mock.calls.some(([u]) => String(u).endsWith("/auth/orgs"))).toBe(false);
    });
  }
});
