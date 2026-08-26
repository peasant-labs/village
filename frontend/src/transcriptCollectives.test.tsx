import { waitFor } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { describe, expect, it } from "vitest";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
  type MountedRouteTranscriptMetadata,
} from "@/test/mountedProductionRoute";
import { loadProfileCollectivesFixtures } from "@/test/profileCollectivesFixtures";

// Mounts the REAL transcript route and asserts the per-transcript collectives
// list the server serves to THIS viewer.
//
// The endpoint is auth-optional and answers 200 with a list in every case,
// including the ones where the collective-visibility rule or the transcript
// owner's contributor opt-in withholds everything. So the two empty cases
// below must render identically to each other and to a transcript that is in
// no collective at all: any wording that reads as "there is something here you
// may not see" re-creates the exact disclosure the empty list exists to avoid.
const fixtures = loadProfileCollectivesFixtures();

function buildSessionDetail(id: string): SessionDetailPayload {
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
      {
        index: 0,
        role: "user",
        content: "why is ingest dropping commits?",
        timestamp: "2026-08-21T09:00:00.000Z",
        depth: 0,
      },
      {
        index: 1,
        role: "assistant",
        content: "Looking at the detector now.",
        timestamp: "2026-08-21T09:01:00.000Z",
        depth: 0,
      },
    ],
  };
}

installMountedRouteTeardown();

describe("mounted production transcript route: collectives holding this transcript", () => {
  for (const c of fixtures.transcriptCollectivesCases) {
    it(c.name, async () => {
      const transcriptID = `transcript-${c.name}`;
      const metadata: MountedRouteTranscriptMetadata = {
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
        viewer_collectives: c.collectives,
      };
      const fetchMock = installRESTFixture(
        transcriptID,
        metadata,
        buildSessionDetail(`session-${c.name}`),
        "transcript-collectives",
      );
      await renderProductionRoute(transcriptID);
      await waitFor(() => expect(document.querySelector(".txn-title")).not.toBeNull());

      // The request is always made and always answered 200 — the viewer gate
      // lives in the server's query, not in the client declining to ask.
      const collectivesCalls = fetchMock.mock.calls.filter((call) =>
        String(call[0]).endsWith(`/transcripts/${transcriptID}/collectives`),
      );
      expect(collectivesCalls.length).toBeGreaterThan(0);

      if (c.expectedCollectiveNames.length === 0) {
        // Empty renders as nothing at all: no container, no label, and no
        // hint that anything was withheld.
        await waitFor(() =>
          expect(document.querySelector('[data-testid="transcript-collectives"]')).toBeNull(),
        );
        // Scoped to the action row the chips would occupy: the viewer's own
        // view controls legitimately carry unrelated words like "hidden".
        const actions = document.querySelector<HTMLElement>(".txn-actions");
        expect(actions).not.toBeNull();
        expect(actions!.textContent).not.toMatch(
          /hidden|withheld|not shown|cannot see|forbidden|collective/i,
        );
        return;
      }

      await waitFor(() =>
        expect(document.querySelector('[data-testid="transcript-collectives"]')).not.toBeNull(),
      );
      const chips = [
        ...document.querySelectorAll<HTMLElement>('[data-testid="transcript-collective"]'),
      ];
      expect(chips.map((chip) => chip.textContent?.trim())).toEqual(c.expectedCollectiveNames);
      expect(chips.map((chip) => chip.dataset.collectiveId)).toEqual(
        c.collectives.map((g) => g.id),
      );
    });
  }
});
