import { Suspense, createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AppRouterContext } from "next/dist/shared/lib/app-router-context.shared-runtime";
import type { AppRouterInstance } from "next/dist/shared/lib/app-router-context.shared-runtime";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import ExplorePage from "@/app/explore/ExplorePage";
import UserProfilePage from "@/app/users/[username]/page";
import { AGENT_ORIGIN, type SessionOrigin } from "@/lib/sessionOrigin";
import type { TranscriptListItem, TranscriptListResponse } from "@/lib/types";
import {
  loadAgentSessionGroupingFixtures,
  type AgentSessionGroupingCase,
} from "@/test/agentSessionGroupingFixtures";

/**
 * Mounted evidence for the collapsed group of agent-driven sessions.
 *
 * Both routes are the REAL production components (src/app/page.tsx and
 * src/app/users/[username]/page.tsx) with the REAL discovery hook and the REAL
 * published Fairtrade Explore surface. Only HTTP is controlled: the mock
 * answers the default list and the `origin=agent` list separately, exactly as
 * the server does, so the assertions describe what a person sees rather than
 * what a component was handed.
 *
 * The permutations live in src/testdata/agent-session-grouping.yaml.
 */

const PROFILE_USERNAME = "octocat";

const fixtures = loadAgentSessionGroupingFixtures();

function wireItem(id: string, sessionOrigin: SessionOrigin): TranscriptListItem {
  return {
    transcript: {
      id,
      owner_id: "owner-1",
      local_id: id,
      title: `Session ${id}`,
      description: null,
      visibility: "public",
      model_provider: "claude-code",
      model_name: "Claude",
      harness_version: "1.0",
      session_start: "2026-08-24T09:00:00Z",
      session_end: "2026-08-24T09:05:00Z",
      published_at: "2026-08-24T09:05:00Z",
      turn_count: 4,
      token_count: 100,
      tool_call_count: 1,
      duration_ms: 300000,
      git_branch: null,
      git_remote: null,
      project_name: "commons-grouping",
      // Fixed across every wired item so they collapse into the SAME
      // project group under groupByProject's hash-keyed re-key — matching
      // this fixture's earlier, name-derived behavior, where every item
      // shared the derived name "commons-grouping" and landed in one group.
      project_hash: "1".repeat(64),
      project_display_name: "commons-grouping",
      project_name_source: "consented",
      project_remote_label: "",
      parent_session_id: null,
      title_generated: null,
      license_id: null,
      session_origin: sessionOrigin,
    } as unknown as TranscriptListItem["transcript"],
    tags: [],
    owner: {
      id: "owner-1",
      github_username: PROFILE_USERNAME,
      display_name: "Octo Cat",
      avatar_url: null,
    } as unknown as TranscriptListItem["owner"],
  };
}

function listResponse(ids: string[], origin: SessionOrigin, agentTotal: number): TranscriptListResponse {
  return {
    transcripts: ids.map((id) => wireItem(id, origin)),
    total: ids.length,
    agent_total: agentTotal,
    page: 1,
    limit: 24,
  };
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

/** Serves the two list scopes the way the server does: the default list never
 *  carries agent rows, and `origin=agent` carries only them. */
function installREST(testCase: AgentSessionGroupingCase): ReturnType<typeof vi.fn> {
  const fetchMock = vi.fn(async (input: RequestInfo | URL): Promise<Response> => {
    const url = String(input);
    if (url.includes("/tags/popular")) return jsonResponse([]);
    if (url.includes("/groups/search")) return jsonResponse({ collectives: [] });
    if (url.includes(`/users/${PROFILE_USERNAME}`)) {
      return jsonResponse({
        id: "owner-1",
        github_username: PROFILE_USERNAME,
        display_name: "Octo Cat",
        avatar_url: null,
        is_discoverable: true,
      });
    }
    if (url.includes("/transcripts")) {
      if (url.includes("origin=agent")) {
        return jsonResponse(listResponse(testCase.agentSessions, AGENT_ORIGIN, testCase.agentTotal));
      }
      return jsonResponse(listResponse(testCase.listedSessions, "user", testCase.agentTotal));
    }
    return jsonResponse({});
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function makeRouter(): AppRouterInstance {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    refresh: vi.fn(),
    back: vi.fn(),
    forward: vi.fn(),
    prefetch: vi.fn(),
  } as unknown as AppRouterInstance;
}

async function renderSurface(testCase: AgentSessionGroupingCase): Promise<void> {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const page =
    testCase.surface === "explore"
      ? createElement(ExplorePage)
      : createElement(UserProfilePage, { params: Promise.resolve({ username: PROFILE_USERNAME }) });
  await act(async () => {
    render(
      createElement(
        AppRouterContext.Provider,
        { value: makeRouter() },
        createElement(
          QueryClientProvider,
          { client },
          createElement(Suspense, { fallback: createElement("div", null, "loading route") }, page as ReactNode),
        ),
      ),
    );
  });
  await flush();
}

async function flush(): Promise<void> {
  await act(async () => {
    for (let i = 0; i < 4; i += 1) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
  });
}

/** Every transcript id the mounted surface currently links to. */
function visibleIds(): string[] {
  return [...document.querySelectorAll<HTMLAnchorElement>('a[href^="/transcripts/"]')].map((anchor) =>
    anchor.getAttribute("href")!.replace("/transcripts/", ""),
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("mounted agent-session grouping", () => {
  for (const testCase of fixtures.cases) {
    it(testCase.name, async () => {
      installREST(testCase);
      await renderSurface(testCase);

      // The rows the surface shows first are exactly the ones the server listed.
      await waitFor(() => {
        for (const id of testCase.listedSessions) {
          expect(visibleIds(), `${testCase.name}: the listed sessions must be on screen`).toContain(id);
        }
      });
      for (const id of testCase.agentSessions) {
        expect(
          visibleIds(),
          `${testCase.name}: agent session ${id} must not occupy the root-level list`,
        ).not.toContain(id);
      }

      if (testCase.expectedToggleLabel == null) {
        expect(screen.queryByTestId("agent-session-group")).toBeNull();
        expect(screen.queryByTestId("agent-session-group-toggle")).toBeNull();
        return;
      }

      const toggle = screen.getByTestId("agent-session-group-toggle");
      expect(toggle.textContent, `${testCase.name}: collapsed label`).toContain(testCase.expectedToggleLabel);
      expect(toggle.getAttribute("aria-expanded")).toBe("false");
      expect(screen.queryByTestId("agent-session-group-rows")).toBeNull();

      if (testCase.surface === "profile" && testCase.listedSessions.length > 0) {
        // The group belongs after the project groups, never mixed into them.
        const group = screen.getByTestId("agent-session-group");
        const projectRow = document.querySelector(`a[href="/transcripts/${testCase.listedSessions[0]}"]`);
        expect(projectRow, "the profile library must render its project rows").not.toBeNull();
        expect(
          projectRow!.compareDocumentPosition(group) & Node.DOCUMENT_POSITION_FOLLOWING,
          `${testCase.name}: the agent group must follow the project groups`,
        ).toBeTruthy();
      }

      await act(async () => {
        await userEvent.click(toggle);
      });
      await flush();

      await waitFor(() => expect(screen.getByTestId("agent-session-group-rows")).toBeTruthy());
      expect(screen.getByTestId("agent-session-group-toggle").getAttribute("aria-expanded")).toBe("true");

      const rows = screen.getByTestId("agent-session-group-rows");
      const expandedIds = [...rows.querySelectorAll<HTMLAnchorElement>('a[href^="/transcripts/"]')].map((anchor) =>
        anchor.getAttribute("href")!.replace("/transcripts/", ""),
      );
      expect(expandedIds.sort(), `${testCase.name}: expanded rows`).toEqual([...testCase.agentSessions].sort());
      expect(
        rows.querySelectorAll('[data-testid="agent-session-badge"]').length,
        `${testCase.name}: every expanded row carries the agent-session label`,
      ).toBe(testCase.agentSessions.length);

      // The listed rows never gained a label; only the group's rows are agent work.
      const listedBadges = [...document.querySelectorAll('[data-testid="agent-session-badge"]')].length;
      expect(listedBadges).toBe(testCase.agentSessions.length);
    });
  }
});
