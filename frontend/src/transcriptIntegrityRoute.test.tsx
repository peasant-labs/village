import { Suspense } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, waitFor } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { describe, expect, it } from "vitest";
import TranscriptPreview from "@/components/contribute/TranscriptPreview";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
  type MountedRouteViewer,
} from "@/test/mountedProductionRoute";
import { loadTranscriptIntegrityFixtures, type IntegrityCase } from "@/test/transcriptIntegrityFixtures";

// The production route (TranscriptDetailPage -> SessionDetailV2 -> fairtrade's
// TranscriptViewer) with REST mocked. Village hands the viewer the served
// integrity value and both hashes; fairtrade decides what to draw, so the
// assertions are on the badge and the flagged-turn marks in the DOM.

const fixtures = loadTranscriptIntegrityFixtures();
const TRANSCRIPT_ID = "7c0a17e2-5b2e-4c1d-9a8f-2d6e7b40e21a";
const OWNER = { id: "user-owner", github_username: "owner" };

installMountedRouteTeardown();

function sessionDetail(): SessionDetailPayload {
  return {
    id: "session-integrity",
    harness: "claude-code",
    startTime: "2026-08-21T09:00:00.000Z",
    endTime: "2026-08-21T09:02:00.000Z",
    durationMins: 2,
    totalTokens: 200,
    tokensIn: 120,
    tokensOut: 80,
    turnCount: 3,
    toolCallCount: 0,
    project: "village",
    model: "anthropic/claude-fable-5",
    turns: [
      { index: 0, role: "user", content: "why is ingest dropping commits?", timestamp: "2026-08-21T09:00:00.000Z", depth: 0 },
      { index: 1, role: "assistant", content: "Looking at the detector now.", timestamp: "2026-08-21T09:01:00.000Z", depth: 0 },
      { index: 2, role: "user", content: "thanks, ship it", timestamp: "2026-08-21T09:02:00.000Z", depth: 0 },
    ],
  };
}

function viewerFor(c: IntegrityCase): MountedRouteViewer | undefined {
  if (c.viewer === "owner") return { ...OWNER, orgs: [] };
  if (c.viewer === "member") return { id: "user-member", github_username: "member", orgs: [] };
  return undefined;
}

async function mount(c: IntegrityCase) {
  const headerValue = c.header === "same" ? c.rowHash : c.header === "none" ? null : c.header;
  installRESTFixture(
    TRANSCRIPT_ID,
    {
      transcript: {
        id: TRANSCRIPT_ID,
        local_id: "session-integrity",
        visibility: c.visibility,
        title: "Fix flaky ingest test",
        description: null,
        project_name: "village",
        content_hash: c.rowHash,
      },
      owner: OWNER,
      enriched_shares: [],
      ...(c.integrity ? { integrity: { flagged_turns: c.integrity } } : {}),
    } as Parameters<typeof installRESTFixture>[1],
    sessionDetail(),
    "transcript-integrity",
    viewerFor(c),
    (url) => {
      if (!url.endsWith(`/transcripts/${TRANSCRIPT_ID}/content`)) return undefined;
      const headers: Record<string, string> = { "content-type": "application/json" };
      if (headerValue != null) {
        headers["Village-Content-Hash"] = headerValue;
        headers["Access-Control-Expose-Headers"] = "Village-Content-Hash";
      }
      return new Response(JSON.stringify(sessionDetail()), { status: 200, headers });
    },
  );
  if (c.surface === "preview") {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => {
      render(
        <QueryClientProvider client={client}>
          <Suspense fallback={null}>
            <TranscriptPreview transcriptId={TRANSCRIPT_ID} />
          </Suspense>
        </QueryClientProvider>,
      );
    });
  } else {
    await renderProductionRoute(TRANSCRIPT_ID, "", { signedIn: c.viewer !== "signed-out" });
  }
  // The turns have rendered, so the content read (and its header) has landed.
  await waitFor(() => expect(document.body.textContent).toContain("Looking at the detector now."));
}

describe("the transcript page and the integrity flag", () => {
  for (const c of fixtures) {
    it(c.name, async () => {
      await mount(c);
      await waitFor(() =>
        expect(document.querySelectorAll(".txn-integrity-mark").length, c.why).toBe(c.expectMarks),
      );
      expect(document.querySelectorAll(".txn-integrity-badge").length, c.why).toBe(c.expectBadge ? 1 : 0);
    });
  }
});
