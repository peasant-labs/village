import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import GroupContributePage from "@/app/groups/[id]/contribute/page";
import GroupDetailPage from "@/app/groups/[id]/page";
import GroupReviewPage from "@/app/groups/[id]/review/page";
import { AuthProvider } from "@/providers/AuthProvider";
import type { Group, User } from "@/lib/types";
import {
  continuationPage,
  fallbackFlatBrowseRows,
  fallbackFlatContributeRows,
  fallbackFlatPendingRows,
  fallbackGroupedItems,
  groupedDetailPage,
  loadCollectiveGroupedActionFixtures,
  memberPage,
  type ActionSurface,
  type CollectiveFallbackCase,
  type GroupedPageShape,
} from "@/test/collectiveGroupedActionFixtures";
import { memberUUID } from "@/test/groupedHelperMountFixtures";

/**
 * Mounted evidence for the grouped exits a collective's flat rendering does not
 * carry, driven by `src/testdata/collective-grouped-actions.yaml`.
 *
 * The REAL `/groups/{id}`, `/groups/{id}/contribute` and `/groups/{id}/review`
 * routes render, each through its REAL grouped query hook; only HTTP is
 * controlled. Two states are proved here:
 *
 *  • a flat result that is empty, or does not carry the grouped owner, still
 *    mounts the grouped disclosure (the helper-only context, or the owner row
 *    the flat result omitted), while an owner the flat result DOES carry mounts
 *    its group once and never again;
 *  • a grouped read is a paginated read: a group the continuation reaches on a
 *    later page mounts once when it is read, and is unreachable before that.
 */

const fixtures = loadCollectiveGroupedActionFixtures();
const GROUP_ID = fixtures.groupId;
/** The grouped page size the collective routes request. */
const GROUPED_PAGE_SIZE = fixtures.continuationCases[0].pageSize;
const VIEWER: User = {
  id: "user-viewer",
  github_id: 1,
  github_username: "viewer",
  display_name: "viewer",
  avatar_url: null,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  is_discoverable: true,
  username_chosen: true,
  provider_username: "viewer",
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  globalThis.localStorage?.clear();
});

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function groupFixture(role: "member" | "owner"): Group {
  return {
    id: GROUP_ID,
    name: "grouped actions collective",
    description: null,
    linked_github_org: null,
    display_members: true,
    transcript_deletion_policy: "user_choice",
    created_by: "someone-else",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    acceptance_mode: "curated",
    data_access: "members_only",
    role,
    member_since: "2026-01-01T00:00:00Z",
  };
}

interface Calls {
  grouped: string[];
  members: string[];
}

interface FlatPayloads {
  /** `transcripts` on the collective detail response. */
  browse: unknown[];
  /** The flat contributable response body. */
  contribute: unknown;
  /** The flat pending queue. */
  review: unknown;
}

/** The grouped response body for one route's own decoder. */
function groupedResponse(surface: ActionSurface, page: GroupedPageShape): unknown {
  if (surface === "browse") return groupedDetailPage(fixtures, page);
  if (surface === "contribute") return { groupId: GROUP_ID, transcriptList: page };
  return page;
}

function installREST(
  surface: ActionSurface,
  groupedPage: (page: number) => GroupedPageShape,
  flat: FlatPayloads,
): Calls {
  const calls: Calls = { grouped: [], members: [] };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input));
      const path = url.pathname;
      const grouped = url.searchParams.get("view") === "grouped";
      const page = Number(url.searchParams.get("page") ?? 1);

      if (path.endsWith("/auth/me")) return json(VIEWER);
      if (path.endsWith("/auth/orgs")) return json([]);
      if (path === `/api/v1/groups/${GROUP_ID}`) {
        if (grouped) {
          calls.grouped.push(url.search);
          return json(groupedResponse(surface, groupedPage(page)));
        }
        const role = surface === "review" ? "owner" : "member";
        return json({
          group: groupFixture(role),
          members: [],
          transcripts: flat.browse,
          stats: {},
          models: [],
          contributors: [],
          can_read: true,
          your_role: role,
          pending_members: [],
        });
      }
      if (path === `/api/v1/groups/${GROUP_ID}/contributable`) {
        if (grouped) {
          calls.grouped.push(url.search);
          return json(groupedResponse(surface, groupedPage(page)));
        }
        return json(flat.contribute);
      }
      if (path === `/api/v1/groups/${GROUP_ID}/pending`) {
        if (grouped) {
          calls.grouped.push(url.search);
          return json(groupedResponse(surface, groupedPage(page)));
        }
        return json(flat.review);
      }
      if (path.includes("/transcript-groups/")) {
        calls.members.push(url.search);
        const scope = url.searchParams.get("scope");
        const key = (Object.keys(fixtures.groups) as (keyof typeof fixtures.groups)[]).find(
          (candidate) => fixtures.groups[candidate].memberScope === scope,
        );
        if (key == null) return json({ error: "unknown scope" }, 409);
        return json(memberPage(fixtures, key, surface));
      }
      throw new Error(`collective grouped fallback fixture received an unexpected ${path}${url.search}`);
    }),
  );
  return calls;
}

function flatPayloads(surface: ActionSurface, rows: unknown[]): FlatPayloads {
  if (surface === "browse") {
    return { browse: rows, contribute: { group_id: GROUP_ID, transcripts: [] }, review: [] };
  }
  if (surface === "contribute") {
    return { browse: [], contribute: { group_id: GROUP_ID, transcripts: rows }, review: [] };
  }
  return { browse: [], contribute: { group_id: GROUP_ID, transcripts: [] }, review: rows };
}

function fallbackFlat(surface: ActionSurface, testCase: CollectiveFallbackCase): FlatPayloads {
  switch (surface) {
    case "browse":
      return flatPayloads(surface, fallbackFlatBrowseRows(fixtures, testCase));
    case "contribute":
      return flatPayloads(surface, fallbackFlatContributeRows(fixtures, testCase));
    default:
      return flatPayloads(surface, fallbackFlatPendingRows(fixtures, testCase));
  }
}

function renderRoute(surface: ActionSurface) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <AuthProvider>
        {surface === "contribute" ? (
          <GroupContributePage params={Promise.resolve({ id: GROUP_ID })} />
        ) : surface === "review" ? (
          <GroupReviewPage params={Promise.resolve({ id: GROUP_ID })} />
        ) : (
          <GroupDetailPage params={Promise.resolve({ id: GROUP_ID })} />
        )}
      </AuthProvider>
    </QueryClientProvider>,
  );
}

function groupRoots(groupId: string): NodeListOf<HTMLElement> {
  return document.querySelectorAll<HTMLElement>(`.helper-group[data-group-id="${groupId}"]`);
}

for (const testCase of fixtures.fallbackCases) {
  it(testCase.name, async () => {
    const row = fixtures.rows.find((candidate) => candidate.name === testCase.row)!;
    const group = fixtures.groups[testCase.group];
    const items = fallbackGroupedItems(fixtures, testCase);
    const page: GroupedPageShape = {
      items,
      page: 1,
      limit: 100,
      totalItems: items.length,
      ordinarySessionTotal: testCase.flat === "rendered" ? 1 : 0,
      helperThreadTotal: 1,
    };
    const calls = installREST(testCase.surface, () => page, fallbackFlat(testCase.surface, testCase));

    await act(async () => {
      renderRoute(testCase.surface);
    });

    // One grouped page read, and the group mounts exactly once wherever the
    // surface can draw it.
    await waitFor(() => expect(calls.grouped).toHaveLength(1));
    const request = new URLSearchParams(calls.grouped[0]);
    expect(request.get("page")).toBe("1");
    expect(request.get("limit")).toBe(String(GROUPED_PAGE_SIZE));
    const roots = await waitFor(() => {
      const found = groupRoots(group.groupId);
      expect(found).toHaveLength(1);
      return found;
    });

    if (testCase.grouped === "context") {
      // A helper-only context container is read context: it states the
      // authorized owner status and carries no selectable row. The empty flat
      // result must be replaced by the grouped content, not by an empty state.
      await waitFor(() =>
        expect(document.body.textContent).toContain("owner is unavailable"),
      );
      expect(roots[0].querySelectorAll('input[type="checkbox"]')).toHaveLength(0);
      expect(roots[0].querySelectorAll("a.helper-thread-open")).toHaveLength(0);
      const fallbackWrapper = document.querySelector('[data-testid="grouped-helper-fallback"]');
      expect(fallbackWrapper, "the grouped context mounts outside the flat branch").not.toBeNull();
      expect(fallbackWrapper!.contains(roots[0])).toBe(true);
      expectEmptyStateConsideringGrouped(testCase.surface);
      return;
    }

    if (testCase.flat === "rendered") {
      // The flat rendering carries the owner, so the group hangs under that
      // row and the fallback must not draw a second owner row.
      const ownerSlot =
        testCase.surface === "browse"
          ? document.querySelector('[data-testid="owner-helper-groups"]')
          : document.querySelector(`[data-helper-group-owner="${row.id}"]`);
      expect(ownerSlot, "the flat rendering mounts the owner row").not.toBeNull();
      expect(ownerSlot!.contains(roots[0])).toBe(true);
      const fallbackWrapper = document.querySelector('[data-testid="grouped-helper-fallback"]');
      const duplicatedOwner =
        fallbackWrapper?.querySelectorAll(`a.helper-thread-open[href="/transcripts/${row.id}"]`) ?? [];
      expect(duplicatedOwner, "the fallback draws no second owner row").toHaveLength(0);
    } else {
      // The grouped owner is absent from the flat result, so its row and group
      // must mount in the fallback instead.
      const fallbackWrapper = document.querySelector('[data-testid="grouped-helper-fallback"]');
      expect(fallbackWrapper, "the grouped owner mounts outside the flat branch").not.toBeNull();
      expect(fallbackWrapper!.contains(roots[0])).toBe(true);
      const ownerLink = fallbackWrapper!.querySelector(
        `a.helper-thread-open[href="/transcripts/${row.id}"]`,
      );
      expect(ownerLink, "the fallback owner row links to its own transcript").not.toBeNull();
      expectEmptyStateConsideringGrouped(testCase.surface);
    }

    // Expanding the group mounts exactly one individually linked member, the
    // same per-member semantics the flat-carried group keeps.
    const member = fixtures.members[testCase.group][0];
    const trigger = roots[0].querySelector<HTMLButtonElement>("button.helper-group-trigger");
    expect(trigger).not.toBeNull();
    act(() => {
      fireEvent.click(trigger!);
    });
    const memberLink = await waitFor(() => {
      const found = document.querySelector<HTMLAnchorElement>(
        `a.helper-thread-open[href="/transcripts/${memberUUID(member.name)}"]`,
      );
      expect(found).not.toBeNull();
      return found!;
    });
    expect(memberLink).toBeInTheDocument();
  });
}

/** The route must not claim an empty flat state while grouped content is shown. */
function expectEmptyStateConsideringGrouped(surface: ActionSurface): void {
  if (surface === "browse") {
    expect(screen.queryByText("No transcripts shared yet.")).toBeNull();
  } else if (surface === "contribute") {
    expect(screen.queryByTestId("contribute-member-panel")).toBeNull();
  } else {
    expect(screen.queryByTestId("review-empty-queue")).toBeNull();
  }
}

for (const testCase of fixtures.continuationCases) {
  it(testCase.name, async () => {
    const laterRow = fixtures.rows.find((candidate) => candidate.name === testCase.laterRow)!;
    const laterGroup = fixtures.groups[testCase.laterGroup];
    const laterMember = fixtures.members[testCase.laterGroup].find(
      (member) => member.name === testCase.laterMember,
    )!;
    const calls = installREST(
      testCase.surface,
      (page) => continuationPage(fixtures, testCase, page),
      flatPayloads(testCase.surface, []),
    );

    await act(async () => {
      renderRoute(testCase.surface);
    });

    // Page one is read at the route's own page size, and the later group is
    // unreachable until the continuation asks for page two.
    await waitFor(() => expect(calls.grouped).toHaveLength(1));
    const first = new URLSearchParams(calls.grouped[0]);
    expect(first.get("page")).toBe("1");
    expect(first.get("limit")).toBe(String(testCase.pageSize));
    expect(groupRoots(laterGroup.groupId)).toHaveLength(0);

    const remaining = testCase.totalItems - testCase.firstPageItems;
    const continuation = await screen.findByTestId("grouped-helper-continuation");
    expect(continuation.textContent).toContain(
      `${remaining} more grouped ${remaining === 1 ? "result" : "results"}`,
    );

    fireEvent.click(screen.getByRole("button", { name: "load more" }));
    await waitFor(() => expect(calls.grouped).toHaveLength(2));
    const second = new URLSearchParams(calls.grouped[1]);
    expect(second.get("page")).toBe("2");
    expect(second.get("limit")).toBe(String(testCase.pageSize));

    // The later owner group mounts once, inside the fallback, with its own
    // owner link, and the continuation is gone once the total is exhausted.
    const roots = await waitFor(() => {
      const found = groupRoots(laterGroup.groupId);
      expect(found).toHaveLength(1);
      return found;
    });
    const fallbackWrapper = document.querySelector('[data-testid="grouped-helper-fallback"]');
    expect(fallbackWrapper).not.toBeNull();
    expect(fallbackWrapper!.contains(roots[0])).toBe(true);
    expect(
      fallbackWrapper!.querySelector(`a.helper-thread-open[href="/transcripts/${laterRow.id}"]`),
    ).not.toBeNull();
    expect(screen.queryByTestId("grouped-helper-continuation")).toBeNull();

    // Expanding the later group reaches its one individually linked member.
    act(() => {
      fireEvent.click(roots[0].querySelector<HTMLButtonElement>("button.helper-group-trigger")!);
    });
    await waitFor(() =>
      expect(
        document.querySelector(
          `a.helper-thread-open[href="/transcripts/${memberUUID(laterMember.name)}"]`,
        ),
      ).not.toBeNull(),
    );
  });
}
