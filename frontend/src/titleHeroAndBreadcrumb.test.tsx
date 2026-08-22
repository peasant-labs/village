import { Suspense } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, waitFor } from "@testing-library/react";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { afterEach, describe, expect, it, vi } from "vitest";
import TranscriptDetailPage from "@/app/transcripts/[id]/page";
import {
  loadTitleHeroAndBreadcrumbFixtures,
  type TitleHeroAndBreadcrumbCase,
} from "@/test/titleHeroAndBreadcrumbFixtures";

// Mounts the REAL production route (TranscriptDetailPage -> SessionDetailV2 ->
// fairtrade's TranscriptViewer) with REST mocked, following the same pattern
// as mountedObservedModelRoute.test.tsx. Covers village#32 (the detail hero
// deriving its own title instead of showing the stored one) and village#33
// (the breadcrumb's hardcoded, 404ing /projects hrefs).

const fixtures = loadTitleHeroAndBreadcrumbFixtures();

function buildSessionDetail(c: TitleHeroAndBreadcrumbCase): SessionDetailPayload {
  const userTurn: NonNullable<SessionDetailPayload["turns"]>[number] = {
    index: 0,
    role: "user",
    content: c.firstUserTurnContent,
    timestamp: "2026-08-21T09:00:00.000Z",
    depth: 0,
  };
  const assistantTurn: NonNullable<SessionDetailPayload["turns"]>[number] = {
    index: 1,
    role: "assistant",
    content: "Understood — investigating now.",
    timestamp: "2026-08-21T09:01:00.000Z",
    depth: 0,
  };
  return {
    id: `session-${c.name}`,
    harness: "claude-code",
    startTime: "2026-08-21T09:00:00.000Z",
    endTime: "2026-08-21T09:02:00.000Z",
    durationMins: 2,
    totalTokens: 200,
    tokensIn: 120,
    tokensOut: 80,
    turnCount: 2,
    toolCallCount: 0,
    project: c.project,
    model: "anthropic/claude-fable-5",
    turns: [userTurn, assistantTurn],
  };
}

function installRESTFixture(transcriptID: string, c: TitleHeroAndBreadcrumbCase, detail: SessionDetailPayload) {
  const metadata = {
    transcript: {
      id: transcriptID,
      local_id: detail.id,
      visibility: "public",
      title: c.storedTitle,
      description: null,
      project_name: c.project,
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
    throw new Error(`title-hero-and-breadcrumb fixture received an unexpected request to ${url}`);
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

describe("mounted production transcript route: stored title hero + routable breadcrumb", () => {
  for (const c of fixtures.cases) {
    it(c.name, async () => {
      const transcriptID = `transcript-${c.name}`;
      const detail = buildSessionDetail(c);
      installRESTFixture(transcriptID, c, detail);

      await renderProductionRoute(transcriptID);

      await waitFor(() => expect(document.querySelector(".txn-title")).not.toBeNull());

      // --- hero title: the composite must show the STORED title (or the
      // explore card's own untitled label), never a title it derived from
      // the first user turn. Independent of every breadcrumb assertion
      // below — reverting only the title overlay must fail only this. ---
      const hero = document.querySelector(".txn-title");
      expect(hero?.textContent).toBe(c.expectedHeroTitle);

      // --- breadcrumb: every href must resolve to a real village route,
      // and specifically none may start with /projects (village#33).
      // Independent of the title assertion above — reverting only the
      // hardcoded /projects hrefs must fail only these. ---
      const nav = document.querySelector('nav[aria-label="breadcrumb"]');
      expect(nav).not.toBeNull();

      const allHrefs = [...document.querySelectorAll("a")].map((a) => a.getAttribute("href"));
      for (const href of allHrefs) {
        expect(href?.startsWith("/projects")).toBe(false);
      }

      const crumbAnchors = [...nav!.querySelectorAll("a")];
      expect(crumbAnchors).toHaveLength(1);
      expect(crumbAnchors[0]?.getAttribute("href")).toBe("/");
      expect(crumbAnchors[0]?.textContent).toBe("explore");

      // Project label carries meaning (Peasant's privacy-safe project
      // label) with no village route to link it to, so it renders as text,
      // not a link.
      const projectCrumbs = [...nav!.querySelectorAll("span.link")];
      expect(projectCrumbs).toHaveLength(1);
      expect(projectCrumbs[0]?.textContent).toBe(c.expectedProjectCrumbLabel);

      // Last crumb reads the RAW stored title (trimmed, truncated) — not
      // the hero's overlaid "Untitled transcript" placeholder — falling
      // back to the short VILLAGE transcript id only when the stored title
      // is empty.
      const lastCrumbs = [...nav!.querySelectorAll("span.cur")];
      expect(lastCrumbs).toHaveLength(1);
      const expectedLast = c.expectedCrumbUsesShortId ? transcriptID.slice(0, 8) : c.expectedCrumbLastLabel;
      expect(lastCrumbs[0]?.textContent).toBe(expectedLast);
    });
  }
});
