import { fireEvent, screen, waitFor } from "@testing-library/react";
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
      await renderGroupSettingsRoute(GROUP_ID);

      const select = (await screen.findByLabelText("data access")) as HTMLSelectElement;
      expect(Array.from(select.options).map((o) => o.value)).toEqual(c.expectOptions);
      // The collective's own value is shown, with its own label: a select with
      // no option for its value would silently display another one.
      expect(select.value).toBe(c.savedDataAccess);
      expect(select.selectedOptions[0]?.value).toBe(c.savedDataAccess);
      expect(select.selectedOptions[0]?.textContent?.startsWith(c.savedDataAccess.replace("_", " "))).toBe(
        true,
      );

      // Saving without touching the control keeps the value it had.
      fireEvent.click(screen.getByRole("button", { name: "Save" }));
      await waitFor(() => {
        expect(requests.some((r) => r.method === "PATCH")).toBe(true);
      });
      expect(requests.find((r) => r.method === "PATCH")?.body).toMatchObject({
        data_access: c.savedDataAccess,
      });
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

      // The header action row has drawn for a signed-in viewer: the collectives
      // holding the transcript are listed in it, which is where the control sat.
      await screen.findByText("Acme Platform");
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
