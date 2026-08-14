import { Suspense } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { afterEach, describe, expect, it, vi } from "vitest";
import TranscriptDetailPage from "@/app/transcripts/[id]/page";
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

function installRESTFixture(transcriptID: string, detail: SessionDetailPayload) {
  const metadata = {
    transcript: {
      id: transcriptID,
      local_id: detail.id,
      visibility: "public",
      title: "Observed model contract",
      description: "Mounted production-route compatibility fixture.",
      project_name: detail.project,
    },
    owner: { id: "fixture-owner" },
    enriched_shares: [],
  };
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith(`/transcripts/${transcriptID}/content`)) {
      return new Response(JSON.stringify(detail), { status: 200, headers: { "content-type": "application/json" } });
    }
    if (url.endsWith(`/transcripts/${transcriptID}/annotations`)) {
      return new Response(JSON.stringify({ annotations: [] }), { status: 200, headers: { "content-type": "application/json" } });
    }
    if (url.endsWith(`/transcripts/${transcriptID}`)) {
      return new Response(JSON.stringify(metadata), { status: 200, headers: { "content-type": "application/json" } });
    }
    if (url.endsWith("/groups")) {
      return new Response(JSON.stringify([]), { status: 200, headers: { "content-type": "application/json" } });
    }
    throw new Error(`mounted observed-model route fixture received an unexpected request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

async function renderProductionRoute(transcriptID: string): Promise<void> {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <Suspense fallback={<div>loading mounted transcript</div>}>
          <TranscriptDetailPage params={Promise.resolve({ id: transcriptID })} />
        </Suspense>
      </QueryClientProvider>,
    );
  });
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
  document.documentElement.setAttribute("data-theme", "dark");
});

describe("mounted production transcript route observed-model compatibility", () => {
  for (const fixture of fixtures.observedModelSessions) {
    it(fixture.name, async () => {
      const transcriptID = `transcript-${fixture.name}`;
      const detail = buildSessionDetail(fixture);
      const fetchMock = installRESTFixture(transcriptID, detail);

      await renderProductionRoute(transcriptID);

      await waitFor(() => expect(document.querySelectorAll(".txn-turnwrap")).toHaveLength(fixture.turns.length));
      const renderedTurns = [...document.querySelectorAll<HTMLElement>(".txn-turnwrap")];
      expect(renderedTurns.map((turn) => turn.querySelector(".txn-turnmodel")?.textContent)).toEqual(
        fixture.turns.map(({ expectedEffectiveModel }) => expectedEffectiveModel),
      );
      expect(renderedTurns.map((turn) => turn.querySelector(".txn-modelchange")?.textContent ?? null)).toEqual(
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
