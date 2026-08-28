import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
  type MountedRouteTranscriptMetadata,
} from "@/test/mountedProductionRoute";
import { loadMountedGraphFixtures, type MountedGraphCase } from "@/test/mountedGraphFixtures";

// Mounts the REAL production route (TranscriptDetailPage -> SessionDetailV2 ->
// fairtrade's TranscriptViewer -> TrajectoryGraph) with REST mocked and NO
// mock of @peasant-labs/fairtrade — the only way to prove the trajectory
// graph engine village-75 rewired onto fairtrade's `/graph` entry actually
// mounts in the live DOM, rather than trusting a screenshot alone (village#75
// review round 1, reviewer B).
//
// The bundled/hand-typed SessionDetailPayload fixtures elsewhere in this repo
// carry no tool calls, so the graph would render turn nodes only; this
// fixture's assistant turn carries one real tool call so the graph engine's
// tool-node path is exercised too.

const fixtures = loadMountedGraphFixtures();

function buildSessionDetail(c: MountedGraphCase): SessionDetailPayload {
  const userTurn: NonNullable<SessionDetailPayload["turns"]>[number] = {
    index: 0,
    role: "user",
    content: c.userTurnContent,
    timestamp: "2026-08-28T09:00:00.000Z",
    depth: 0,
  };
  const assistantTurn: NonNullable<SessionDetailPayload["turns"]>[number] = {
    index: 1,
    role: "assistant",
    content: c.assistantTurnContent,
    timestamp: "2026-08-28T09:01:00.000Z",
    depth: 0,
    toolCalls: [
      {
        id: "tool-1",
        name: c.toolName,
        arguments: JSON.stringify({ file_path: c.toolFilePath }),
        result: JSON.stringify("ok"),
        toolKind: "read",
        filePath: c.toolFilePath,
      },
    ],
  };
  return {
    id: `session-${c.name}`,
    harness: "claude-code",
    startTime: "2026-08-28T09:00:00.000Z",
    endTime: "2026-08-28T09:02:00.000Z",
    durationMins: 2,
    totalTokens: 200,
    tokensIn: 120,
    tokensOut: 80,
    turnCount: 2,
    toolCallCount: 1,
    project: c.project,
    model: "anthropic/claude-fable-5",
    turns: [userTurn, assistantTurn],
  };
}

installMountedRouteTeardown();

// @xyflow/react measures nodes via ResizeObserver, which jsdom does not
// implement; a bare stub is enough for it to lay out and mount real DOM
// nodes (the same stub the production route's own tests never needed
// because they don't reach the graph view).
beforeEach(() => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
});

describe("mounted production transcript route: real fairtrade trajectory graph", () => {
  for (const c of fixtures.cases) {
    it(c.name, async () => {
      const user = userEvent.setup();
      const transcriptID = `transcript-${c.name}`;
      const detail = buildSessionDetail(c);
      const metadata: MountedRouteTranscriptMetadata = {
        transcript: {
          id: transcriptID,
          local_id: detail.id,
          visibility: "public",
          title: "Mounted graph evidence",
          description: null,
          project_name: c.project,
        },
        owner: { id: "fixture-owner" },
        enriched_shares: [],
      };
      installRESTFixture(transcriptID, metadata, detail, "mounted-graph");
      await renderProductionRoute(transcriptID);
      await waitFor(() => expect(document.querySelector(".txn-app")).not.toBeNull());

      // Switch to the graph view exactly as a viewer does: the segmented
      // list/graph toggle rendered by fairtrade's TranscriptViewer.
      const graphToggle = [...document.querySelectorAll("button.bs-seg-opt")].find(
        (b) => b.textContent?.trim() === "graph",
      );
      if (!graphToggle) {
        throw new Error(
          `${c.name}: could not find the "graph" segmented toggle (button.bs-seg-opt) in the mounted TranscriptViewer; the view-switch markup may have changed`,
        );
      }
      await user.click(graphToggle);

      // The real fairtrade graph engine mounts .tb-graph containing a real
      // @xyflow/react instance (.react-flow), not a stub or a mock.
      const graph = await waitFor(() => {
        const mounted = document.querySelector(".tb-graph");
        if (!mounted) {
          throw new Error(
            `${c.name}: .tb-graph did not mount in SessionDetailV2.mountedGraph.test.tsx after the user selected graph mode; the real fairtrade trajectory-graph engine is not being exercised — verify graphSlot still wires TrajectoryGraph from @peasant-labs/fairtrade/graph and that fairtrade's packed graph.css/graph.js still export it`,
          );
        }
        return mounted;
      });
      expect(graph.querySelector(".react-flow")).toBeInTheDocument();

      const turnNodes = graph.querySelectorAll(".react-flow__node-turn");
      expect(turnNodes).toHaveLength(c.expectedTurnNodeCount);

      const toolNodes = graph.querySelectorAll(".react-flow__node-toolCall");
      expect(toolNodes).toHaveLength(c.expectedToolNodeCount);
      expect(toolNodes[0]?.textContent).toContain(c.toolName);
      expect(toolNodes[0]?.textContent).toContain(c.toolFilePath);
    });
  }
});
