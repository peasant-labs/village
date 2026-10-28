import { waitFor } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { describe, expect, it } from "vitest";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
  type MountedRouteTranscriptMetadata,
} from "@/test/mountedProductionRoute";
import {
  loadFinalContractCompatibilityFixtures,
  type ObservedModelSessionFixture,
} from "@/test/finalContractCompatibilityFixtures";

const fixtures = loadFinalContractCompatibilityFixtures();

function buildSessionDetail(fixture: ObservedModelSessionFixture): SessionDetailPayload {
  const turns = fixture.turns.map((turn, index) => {
    const wireTurn: NonNullable<SessionDetailPayload["turns"]>[number] = {
      index,
      role: "assistant",
      content: turn.content,
      timestamp: new Date(Date.UTC(2026, 7, 14, 9, index)).toISOString(),
      depth: 0,
    };
    if (turn.sourceObservation != null) wireTurn.observedModel = turn.sourceObservation;
    return wireTurn;
  });
  return {
    id: `session-${fixture.name}`,
    harness: "claude-code",
    startTime: "2026-08-14T09:00:00.000Z",
    endTime: "2026-08-14T09:04:00.000Z",
    durationMins: 4,
    totalTokens: 400,
    tokensIn: 240,
    tokensOut: 160,
    turnCount: turns.length,
    toolCallCount: 0,
    project: "observed-model-contract",
    model: fixture.sessionModel,
    turns,
  };
}

function installFixture(transcriptID: string, detail: SessionDetailPayload) {
  const metadata: MountedRouteTranscriptMetadata = {
    transcript: {
      id: transcriptID,
      local_id: detail.id,
      visibility: "public",
      title: "Observed model contract",
      description: "Mounted production-route compatibility fixture.",
      project_name: detail.project ?? "observed-model-contract",
    },
    owner: { id: "fixture-owner" },
    enriched_shares: [],
  };
  return installRESTFixture(transcriptID, metadata, detail, "mounted observed-model route");
}

/** An element's text as assistive technology reads it: hidden parts left out, spaces collapsed. */
function spokenText(element: Element | null): string | null {
  if (!element) return null;
  const copy = element.cloneNode(true) as Element;
  for (const hidden of copy.querySelectorAll('[aria-hidden="true"]')) hidden.remove();
  return (copy.textContent ?? "").replace(/\s+/g, " ").trim();
}

installMountedRouteTeardown();

describe("mounted production transcript route observed-model compatibility", () => {
  for (const fixture of fixtures.observedModelSessions) {
    it(fixture.name, async () => {
      const transcriptID = `transcript-${fixture.name}`;
      const detail = buildSessionDetail(fixture);
      const fetchMock = installFixture(transcriptID, detail);

      await renderProductionRoute(transcriptID);

      await waitFor(() => expect(document.querySelectorAll(".txn-turnwrap")).toHaveLength(fixture.turns.length));
      const renderedTurns = [...document.querySelectorAll<HTMLElement>(".txn-turnwrap")];
      expect(document.querySelector(".txn-meta")?.textContent).toContain(fixture.sessionModel);
      expect(document.querySelectorAll(".txn-turnmodel")).toHaveLength(0);
      // The transition as a reader hears it: the arrow is drawn for the eye and
      // spoken as "to".
      expect(renderedTurns.map((turn) => spokenText(turn.querySelector(".txn-modelchange")))).toEqual(
        fixture.turns.map(({ expectedTransition }) => expectedTransition),
      );
      expect(document.querySelectorAll(".txn-modelchange")).toHaveLength(fixture.expectedTransitionCount);
      for (const [index, turn] of fixture.turns.entries()) {
        const wireTurn = detail.turns?.[index];
        expect(wireTurn != null && Object.hasOwn(wireTurn, "observedModel")).toBe(turn.sourceObservation != null);
      }
      const requested = fetchMock.mock.calls.map(([input]) => String(input));
      expect(requested.some((url) => url.endsWith(`/transcripts/${transcriptID}`))).toBe(true);
      expect(requested.some((url) => url.endsWith(`/transcripts/${transcriptID}/content`))).toBe(true);
    });
  }
});
