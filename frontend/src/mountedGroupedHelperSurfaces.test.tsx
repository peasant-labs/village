import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  AppRouterContext,
  type AppRouterInstance,
} from "next/dist/shared/lib/app-router-context.shared-runtime";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import ExplorePage from "@/app/explore/ExplorePage";
import { AuthProvider } from "@/providers/AuthProvider";
import { renderProfileRoute } from "@/test/mountedProfileRoute";
import { renderProjectRoute } from "@/test/mountedProjectRoute";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { memberUUID } from "@/test/groupedHelperMountFixtures";
import {
  exploreGroupedPayload,
  groupedSurfacePage,
  loadGroupedHelperSurfaceFixtures,
  ordinaryOwnerUUID,
  surfaceMemberPayload,
  type ContinuationCase,
  type ExploreSurfaceCase,
} from "@/test/groupedHelperSurfaceFixtures";
import type { TranscriptListItem, User, UserProjectPageResponse } from "@/lib/types";

/**
 * Mounted evidence for the grouped-helper surfaces, driven by
 * `src/testdata/grouped-helper-surfaces.yaml`.
 *
 * The REAL routes render: the discovery page, the profile route and the project
 * route, each through the REAL grouped query hook. Only HTTP is controlled. The
 * cases assert what a reader sees — one context disclosure per helper-only
 * group, and a reachable continuation that carries the grouped read to an owner
 * or context container past its first server page.
 */

const fixtures = loadGroupedHelperSurfaceFixtures();

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  globalThis.localStorage?.clear();
  document.documentElement.setAttribute("data-theme", "dark");
});

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
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

function userFixture(username: string): User {
  return {
    id: `user-${username}`,
    github_id: 1,
    github_username: username,
    display_name: username,
    avatar_url: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    is_discoverable: true,
    username_chosen: true,
    provider_username: username,
  };
}

function groupedRequestUrls(calls: string[]): string[] {
  return calls.filter((url) => url.includes("view=grouped"));
}

/** The member request a surface's disclosure issues, if any. */
function memberRequestUrls(calls: string[]): string[] {
  return calls.filter((url) => url.includes("/transcript-groups/"));
}

async function renderDiscovery(testCase: ExploreSurfaceCase): Promise<string[]> {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      calls.push(url);
      if (url.includes("/auth/me")) return json({ error: "signed out" }, 401);
      if (url.includes("/tags/popular")) return json([]);
      if (url.includes("/groups/search")) return json({ collectives: [] });
      if (url.includes("/transcript-groups/")) return json(surfaceMemberPayload(testCase.member));
      if (url.includes("/transcripts")) {
        if (url.includes("view=grouped")) return json(exploreGroupedPayload(testCase));
        return json({ transcripts: [], total: 0, agent_total: 0, page: 1, limit: 24 });
      }
      return json({});
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <AppRouterContext.Provider value={makeRouter()}>
          <AuthProvider>
            <ExplorePage />
          </AuthProvider>
        </AppRouterContext.Provider>
      </QueryClientProvider>,
    );
  });
  return calls;
}

function flatRow(testCase: ContinuationCase, index: number): TranscriptListItem {
  return {
    transcript: makeTranscriptFixture({
      id: ordinaryOwnerUUID(index),
      local_id: `ses_flat_${index}`,
      owner_id: `user-${testCase.owner}`,
      title: `flat session ${index}`,
      project_hash: testCase.projectHash ?? "0".repeat(64),
      project_name: "village",
      project_display_name: "village",
    }),
    tags: [],
    owner: userFixture(testCase.owner),
  };
}

function projectPayload(testCase: ContinuationCase): UserProjectPageResponse {
  return {
    project: {
      project_hash: testCase.projectHash ?? "0".repeat(64),
      project_display_name: "village",
      project_name_source: "consented",
      project_remote_label: "github.com:peasant-labs/village",
    },
    owner: userFixture(testCase.owner),
    transcripts: Array.from({ length: testCase.flatTranscripts }, (_, index) =>
      flatRow(testCase, index).transcript,
    ),
    collectives: [],
  };
}

function installContinuationREST(testCase: ContinuationCase): string[] {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      calls.push(url);
      if (url.includes("/auth/me")) return json({ error: "signed out" }, 401);
      if (url.includes("/transcript-groups/")) return json(surfaceMemberPayload(testCase.laterMember));
      if (url.includes("/transcripts")) {
        const params = new URL(url).searchParams;
        if (params.get("view") === "grouped") {
          return json(groupedSurfacePage(testCase, Number(params.get("page") ?? 1)));
        }
        return json({
          transcripts: Array.from({ length: testCase.flatTranscripts }, (_, index) =>
            flatRow(testCase, index),
          ),
          total: testCase.flatTranscripts,
          agent_total: 0,
          page: 1,
          limit: 24,
        });
      }
      if (url.includes("/projects/")) return json(projectPayload(testCase));
      if (/\/users\/[^/?]+$/.test(url)) return json(userFixture(testCase.owner));
      return json({});
    }),
  );
  return calls;
}

it(fixtures.explore.name, async () => {
  const testCase = fixtures.explore;
  await renderDiscovery(testCase);

  const section = await screen.findByTestId("grouped-helper-section");
  // A helper-only result renders ONE owner-unavailable group: one context
  // notice, one disclosure control, one group root. A second renderer for the
  // same context would double every one of them.
  const roots = document.querySelectorAll<HTMLElement>(
    `.helper-group[data-group-id="${testCase.context.groupId}"]`,
  );
  expect(roots).toHaveLength(1);
  expect(document.querySelectorAll(".helper-group-item")).toHaveLength(1);
  expect(section.querySelectorAll("[data-testid='helper-group-label']")).toHaveLength(1);
  expect((section.textContent ?? "").split("owner is unavailable").length - 1).toBe(1);
  expect(section.querySelector("button.helper-group-trigger")?.textContent).toContain(
    `${testCase.group.helperThreadCount} helper thread`,
  );

  // Expanding the single control mounts exactly one individually linked member.
  fireEvent.click(roots[0].querySelector<HTMLButtonElement>("button.helper-group-trigger")!);
  await waitFor(() =>
    expect(document.querySelectorAll("a.helper-thread-open")).toHaveLength(1),
  );
  expect(document.querySelector("a.helper-thread-open")?.getAttribute("href")).toBe(
    `/transcripts/${memberUUID(testCase.member.id)}`,
  );
});

for (const testCase of fixtures.continuationCases) {
  it(testCase.name, async () => {
    const calls = installContinuationREST(testCase);
    if (testCase.route === "profile") {
      await renderProfileRoute(testCase.owner);
    } else {
      await renderProjectRoute(testCase.owner, testCase.projectHash!);
    }

    const remaining = testCase.totalItems - testCase.firstPageOrdinary;
    // The route's own list still renders its row; the grouped read supplements
    // it and removes nothing.
    await waitFor(() =>
      expect(document.querySelector(`a[href="/transcripts/${ordinaryOwnerUUID(0)}"]`)).not.toBeNull(),
    );

    // One grouped page was read, on the stated page size, for this owner.
    await waitFor(() => expect(groupedRequestUrls(calls)).toHaveLength(1));
    const first = new URL(groupedRequestUrls(calls)[0]);
    expect(first.searchParams.get("page")).toBe("1");
    expect(first.searchParams.get("limit")).toBe(String(testCase.pageSize));
    expect(first.searchParams.get("owner")).toBe(testCase.owner);
    expect(first.searchParams.get("project_hash")).toBe(testCase.projectHash);

    // The later helper-only container is NOT reachable until the continuation is
    // used, and the continuation states the server's own remaining count.
    expect(
      document.querySelector(`.helper-group[data-group-id="${testCase.laterContext.groupId}"]`),
    ).toBeNull();
    const continuation = await screen.findByTestId("grouped-helper-continuation");
    expect(continuation.textContent).toContain(
      `${remaining} more grouped ${remaining === 1 ? "result" : "results"}`,
    );

    fireEvent.click(screen.getByRole("button", { name: "load more" }));
    await waitFor(() => expect(groupedRequestUrls(calls)).toHaveLength(2));
    const second = new URL(groupedRequestUrls(calls)[1]);
    expect(second.searchParams.get("page")).toBe("2");
    expect(second.searchParams.get("limit")).toBe(String(testCase.pageSize));
    expect(second.searchParams.get("owner")).toBe(testCase.owner);
    expect(second.searchParams.get("project_hash")).toBe(testCase.projectHash);

    // The later group mounts once, and the continuation is gone because the
    // server's total is now exhausted.
    const laterRoot = await waitFor(() => {
      const found = document.querySelector<HTMLElement>(
        `.helper-group[data-group-id="${testCase.laterContext.groupId}"]`,
      );
      expect(found).not.toBeNull();
      return found;
    });
    expect(document.querySelectorAll(".helper-group-item")).toHaveLength(1);
    expect(screen.queryByTestId("grouped-helper-continuation")).toBeNull();

    fireEvent.click(laterRoot!.querySelector<HTMLButtonElement>("button.helper-group-trigger")!);
    await waitFor(() => expect(document.querySelectorAll("a.helper-thread-open")).toHaveLength(1));
    expect(document.querySelector("a.helper-thread-open")?.getAttribute("href")).toBe(
      `/transcripts/${memberUUID(testCase.laterMember.id)}`,
    );
    expect(memberRequestUrls(calls)).toHaveLength(1);
  });
}
