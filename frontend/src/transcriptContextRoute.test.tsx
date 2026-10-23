import { waitFor } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { beforeEach, describe, expect, it } from "vitest";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
  type MountedRouteTranscriptMetadata,
} from "@/test/mountedProductionRoute";
import { replacedRoutes, resetNextNavigation } from "@/test/nextNavigationMock";
import { loadTranscriptContextFixtures, type RouteCase } from "@/test/transcriptContextFixtures";

// Mounts the REAL transcript route (TranscriptDetailPage -> SessionDetailV2 ->
// fairtrade's TranscriptViewer) with REST mocked, and reads the trail it draws
// for the place the transcript was opened from, and the address it writes for
// a link that named none.

const fx = loadTranscriptContextFixtures();
const TRANSCRIPT_ID = "7c0a17e2-5b2e-4c1d-9a8f-2d6e7b40e21a";
const TITLE = "Fix flaky ingest test";

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });

function detail(): SessionDetailPayload {
  return {
    id: "session-context",
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

function metadata(c: RouteCase): MountedRouteTranscriptMetadata {
  return {
    transcript: {
      id: TRANSCRIPT_ID,
      local_id: "session-context",
      visibility: "shared",
      title: TITLE,
      description: null,
      project_name: "village",
    },
    owner: { id: "user-alice" },
    enriched_shares: c.shares.map((s) => ({
      transcript_id: TRANSCRIPT_ID,
      acceptance_mode: "open",
      shared_at: "2026-08-01T00:00:00.000Z",
      ...s,
    })),
  };
}

async function mount(c: RouteCase): Promise<void> {
  installRESTFixture(
    TRANSCRIPT_ID,
    metadata(c),
    detail(),
    "transcript-context",
    c.signedIn ? { id: "user-bob", github_username: "bob-reads", orgs: [] } : undefined,
    (url) =>
      url.endsWith("/collectives")
        ? json(c.myCollectives.map((g) => ({ ...g, role: "member", acceptance_mode: "open", member_count: 3 })))
        : undefined,
  );
  await renderProductionRoute(TRANSCRIPT_ID, c.search, { signedIn: c.signedIn });
  await waitFor(() => expect(document.querySelector(".txn-title")).not.toBeNull());
}

function crumbs(): string[] {
  const nav = document.querySelector('nav[aria-label="breadcrumb"]');
  return [...(nav?.querySelectorAll(".link, .cur") ?? [])].map((crumb) => crumb.textContent ?? "");
}

installMountedRouteTeardown();

beforeEach(() => {
  resetNextNavigation();
});

describe("mounted transcript route: the trail names where it was opened from", () => {
  for (const c of fx.routeCases) {
    it(c.name, async () => {
      await mount(c);
      await waitFor(() => expect(crumbs()).toEqual(c.expectCrumbs));
      // The title shows once, in the hero, and never in the trail.
      expect(document.querySelector('nav[aria-label="breadcrumb"]')?.textContent).not.toContain(TITLE);

      const expected = c.expectReplace?.replace("{transcriptId}", TRANSCRIPT_ID);
      if (expected) {
        await waitFor(() => expect(replacedRoutes).toContain(expected));
      } else {
        expect(replacedRoutes).toEqual([]);
      }
    });
  }
});
