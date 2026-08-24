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
  loadAgentSessionGroupingFixtures,
  type AgentSessionDetailCase,
} from "@/test/agentSessionGroupingFixtures";

/**
 * Mounted evidence that the session-origin scope is DISCOVERY only.
 *
 * The collapsed group keeps agent-driven sessions out of a root-level list. It
 * must never keep anyone out of a transcript. This mounts the REAL detail route
 * (TranscriptDetailPage -> SessionDetailV2 -> the published viewer) for one
 * transcript of each origin and asserts the whole session renders every time,
 * with the agent-session label present for exactly the agent-driven one.
 *
 * The permutations live in src/testdata/agent-session-grouping.yaml.
 */

const fixtures = loadAgentSessionGroupingFixtures();

/** Two turns, so "the page rendered the session" is a countable claim rather
 *  than the mere absence of an error surface. */
const TURN_CONTENTS = [
  "Take the discovery slice and report back.",
  "Reading the discovery handler.",
] as const;

function buildSessionDetail(name: string): SessionDetailPayload {
  return {
    id: `session-${name}`,
    harness: "claude-code",
    startTime: "2026-08-24T09:00:00.000Z",
    endTime: "2026-08-24T09:04:00.000Z",
    durationMins: 4,
    totalTokens: 400,
    tokensIn: 240,
    tokensOut: 160,
    turnCount: TURN_CONTENTS.length,
    toolCallCount: 0,
    project: "commons-grouping",
    model: "Claude",
    turns: TURN_CONTENTS.map((content, index) => ({
      index,
      role: index === 0 ? ("user" as const) : ("assistant" as const),
      content,
      timestamp: new Date(Date.UTC(2026, 7, 24, 9, index)).toISOString(),
      depth: 0,
    })),
  };
}

function installFixture(testCase: AgentSessionDetailCase, transcriptID: string, detail: SessionDetailPayload) {
  const metadata: MountedRouteTranscriptMetadata = {
    transcript: {
      id: transcriptID,
      local_id: detail.id,
      visibility: "public",
      title: "Discovery scope deep link",
      description: "Mounted production-route session-origin fixture.",
      project_name: detail.project ?? "commons-grouping",
      session_origin: testCase.sessionOrigin,
    },
    owner: { id: "fixture-owner" },
    enriched_shares: [],
  };
  return installRESTFixture(transcriptID, metadata, detail, "mounted agent-session deep link");
}

installMountedRouteTeardown();

describe("mounted transcript route: a direct link opens every session origin", () => {
  for (const testCase of fixtures.detailCases) {
    it(testCase.name, async () => {
      const transcriptID = `transcript-${testCase.name}`;
      const detail = buildSessionDetail(testCase.name);
      installFixture(testCase, transcriptID, detail);

      await renderProductionRoute(transcriptID);

      // The whole session renders: the scope hides a row from a list, never a
      // transcript from the person holding its link.
      await waitFor(() =>
        expect(
          document.querySelectorAll(".txn-turnwrap"),
          `${testCase.name}: the deep-linked session must render its turns`,
        ).toHaveLength(TURN_CONTENTS.length),
      );
      expect(document.body.textContent).toContain("Discovery scope deep link");
      for (const content of TURN_CONTENTS) {
        expect(document.body.textContent, `${testCase.name}: turn content`).toContain(content);
      }

      const chip = document.querySelector('[data-testid="agent-session-chip"]');
      if (testCase.expectChip) {
        expect(chip, `${testCase.name}: an agent-driven session says so on its own page`).not.toBeNull();
        expect(chip!.textContent).toBe("agent session");
      } else {
        expect(
          chip,
          `${testCase.name}: only agent-driven sessions are labelled, so an unclassified one reads like a user session`,
        ).toBeNull();
      }
    });
  }
});
