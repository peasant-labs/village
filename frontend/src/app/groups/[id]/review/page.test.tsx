import { afterEach, describe, expect, it, vi } from "vitest";
import { Suspense } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthProvider } from "@/providers/AuthProvider";
import GroupReviewPage from "@/app/groups/[id]/review/page";
import {
  loadGroupsReviewPageFixtures,
  reviewCaseByName,
  toPendingShare,
  type ReviewPageCase,
} from "@/test/groupsReviewPageFixtures";

/**
 * The REAL `/groups/{id}/review` route, mounted inside the real
 * `AuthProvider` with REST stubbed at `fetch`. Every assertion is on what a
 * signed-in owner (or a non-owner) actually sees and on the PARSED body of
 * the request a click sends — never on a call count alone.
 */

const cases = loadGroupsReviewPageFixtures();
const GROUP_ID = "collective-1";

interface RecordedRequest {
  method: string;
  url: string;
  body: unknown;
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

function installReviewRouteREST(testCase: ReviewPageCase): RecordedRequest[] {
  const requests: RecordedRequest[] = [];
  const pending = testCase.pending.map(toPendingShare);

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : null;
    requests.push({ method, url, body });

    if (url.endsWith("/auth/me")) {
      return json({
        id: `user-${testCase.viewer}`,
        github_id: 1,
        github_username: testCase.viewer,
        display_name: testCase.viewer,
        avatar_url: null,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
        is_discoverable: true,
        username_chosen: true,
        provider_username: testCase.viewer,
      });
    }
    if (url.includes(`/groups/${GROUP_ID}/pending`)) {
      return json(pending);
    }
    if (url.endsWith(`/groups/${GROUP_ID}/shares`) && method === "PATCH") {
      return json({ decided: testCase.decided, already_decided: testCase.alreadyDecided });
    }
    if (new RegExp(`/groups/${GROUP_ID}$`).test(url)) {
      return json({
        group: {
          id: GROUP_ID,
          name: "review collective",
          description: null,
          linked_github_org: null,
          display_members: true,
          transcript_deletion_policy: "user_choice",
          created_by: "user-mod",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
          acceptance_mode: "curated",
          data_access: "members_only",
          role: testCase.role,
          member_since: "2026-01-02T00:00:00Z",
        },
        members: [{ role: testCase.role, joined_at: "2026-01-02T00:00:00Z", id: `user-${testCase.viewer}`, github_username: testCase.viewer, display_name: null, avatar_url: null }],
        transcripts: [],
        stats: {},
        models: [],
        contributors: [],
        can_read: true,
        your_role: testCase.role,
        pending_members: [],
      });
    }
    throw new Error(`the review-route fixture received an unexpected ${method} request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return requests;
}

async function mountReviewRoute(testCase: ReviewPageCase): Promise<RecordedRequest[]> {
  const requests = installReviewRouteREST(testCase);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <AuthProvider>
          <Suspense fallback={<div>loading</div>}>
            <GroupReviewPage params={Promise.resolve({ id: GROUP_ID })} />
          </Suspense>
        </AuthProvider>
      </QueryClientProvider>,
    );
  });
  return requests;
}

/** Ticks the checkbox of each named session row, through the row's own
 *  control rather than through page state. */
async function select(ids: string[]): Promise<void> {
  for (const id of ids) {
    const row = await screen.findByTestId(`contribute-session-row-${id}`);
    const box = within(row).getByRole("checkbox");
    await act(async () => {
      fireEvent.click(box);
    });
  }
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("/groups/{id}/review", () => {
  it("reads the pending queue as a project > branch > session tree", async () => {
    const testCase = reviewCaseByName(cases, "owner_reads_the_queue_grouped_by_project");
    await mountReviewRoute(testCase);

    await screen.findByTestId("review-panel");
    for (const label of testCase.expect.projectLabels as string[]) {
      expect(screen.getByRole("region", { name: `project ${label}` })).toBeTruthy();
    }
    for (const id of testCase.expect.sessionIds as string[]) {
      expect(screen.getByTestId(`contribute-session-row-${id}`)).toBeTruthy();
    }
  });

  it("folds a submission under the submission that started it", async () => {
    const testCase = reviewCaseByName(cases, "child_session_folds_under_its_parent");
    await mountReviewRoute(testCase);

    await screen.findByTestId("review-panel");
    const childrenOf = testCase.expect.childrenOf as Record<string, string[]>;
    for (const [parent, children] of Object.entries(childrenOf)) {
      // The child is not a top-level row of the tree; it lives under the
      // disclosure marked with its parent's id.
      const disclosure = document.querySelector(`[data-parent-transcript-id="${parent}"]`);
      expect(disclosure).not.toBeNull();
      await act(async () => {
        fireEvent.click(within(disclosure as HTMLElement).getByTestId("child-session-disclosure-toggle"));
      });
      const rows = within(disclosure as HTMLElement).getByTestId("child-session-disclosure-rows");
      for (const child of children) {
        expect(within(rows).getByTestId(`contribute-session-row-${child}`)).toBeTruthy();
      }
    }
  });

  it("keeps two publishers apart when they used the same harness session id", async () => {
    const testCase = reviewCaseByName(cases, "two_publishers_sharing_a_session_id_do_not_fold");
    await mountReviewRoute(testCase);

    await screen.findByTestId("review-panel");
    // Both rows are TOP-LEVEL: neither is nested inside the other's child
    // disclosure, which is what would happen if the fold matched on the
    // session id without scoping to the publisher.
    for (const id of testCase.expect.topLevelSessionIds as string[]) {
      const row = screen.getByTestId(`contribute-session-row-${id}`);
      expect(row.closest("[data-testid=child-session-disclosure-rows]")).toBeNull();
    }
    expect(document.querySelector("[data-parent-transcript-id]")).toBeNull();
  });

  it("never lets a selection consist of rows nobody can see", async () => {
    const testCase = reviewCaseByName(cases, "hidden_child_selection_is_never_invisible");
    await mountReviewRoute(testCase);
    await screen.findByTestId("review-panel");

    const disclosure = document.querySelector("[data-parent-transcript-id]") as HTMLElement;
    expect(disclosure).not.toBeNull();
    const control = within(disclosure).getByTestId("child-session-disclosure-toggle");
    // The LABEL element, not the whole control: the control also carries its
    // show/hide affordance, so a substring match against it cannot tell
    // "1 child session" from "1 child session, 0 selected".
    const label = within(disclosure).getByTestId("child-session-disclosure-label");

    // Before anything is ticked the control says only what it HIDES: a
    // "0 selected" on every unselected fold would be noise.
    expect(label.textContent).toBe(testCase.expect.collapsedControlLabelWhenNoneSelected as string);

    // Ticking the project reaches the folded child, which stays off screen.
    await act(async () => {
      fireEvent.click(screen.getByRole("checkbox", { name: /^select project / }));
    });
    expect(control.getAttribute("aria-expanded")).toBe("false");

    // The bar counts the hidden row, so what it offers to decide is what a
    // decision would actually reach.
    expect(screen.getByTestId("review-selection-count").textContent).toBe(
      `${testCase.expect.selectedAfterSelectingProject as number} selected`,
    );
    // The control names the hidden selected row rather than leaving a viewer
    // to infer it from an indeterminate mark somewhere else.
    expect(label.textContent).toBe(testCase.expect.collapsedControlLabel as string);
    // And the hidden row's own visible ancestor is ticked, so the selection
    // is legible even with the fold shut.
    const parentRow = screen.getByTestId(
      `contribute-session-row-${testCase.expect.untickVisible as string}`,
    );
    expect(within(parentRow).getByRole("checkbox").getAttribute("data-state")).toBe(
      testCase.expect.visibleParentRowState,
    );

    // Unticking that visible row takes its hidden child with it: a session's
    // checkbox governs its whole subtree, so no selection survives off screen.
    await act(async () => {
      fireEvent.click(within(parentRow).getByRole("checkbox"));
    });
    expect(screen.getByTestId("review-selection-count").textContent).toBe(
      `${testCase.expect.selectedAfterUntickingVisible as number} selected`,
    );
    expect(label.textContent).toBe(testCase.expect.collapsedControlLabelWhenNoneSelected as string);
  });

  it("approves a selection spanning two projects in ONE request", async () => {
    const testCase = reviewCaseByName(cases, "approve_selection_sends_one_request");
    const requests = await mountReviewRoute(testCase);

    await screen.findByTestId("review-panel");
    await select(testCase.select);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "approve selected" }));
    });

    const patches = requests.filter((r) => r.method === "PATCH");
    await waitFor(() => expect(patches.length).toBe(testCase.expect.requestCount as number));
    const body = patches[0].body as { transcript_ids: string[]; status: string };
    expect([...body.transcript_ids].sort()).toEqual([...(testCase.expect.requestIds as string[])].sort());
    expect(body.status).toBe(testCase.expect.requestStatus);
  });

  it("sends the reject decision when the reject action is used", async () => {
    const testCase = reviewCaseByName(cases, "reject_selection_sends_the_reject_decision");
    const requests = await mountReviewRoute(testCase);

    await screen.findByTestId("review-panel");
    await select(testCase.select);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "reject selected" }));
    });

    const patches = requests.filter((r) => r.method === "PATCH");
    await waitFor(() => expect(patches.length).toBe(testCase.expect.requestCount as number));
    const body = patches[0].body as { transcript_ids: string[]; status: string };
    expect(body.transcript_ids).toEqual(testCase.expect.requestIds);
    expect(body.status).toBe(testCase.expect.requestStatus);
  });

  it("marks a row the server reports as already decided, and drops it from the selection", async () => {
    const testCase = reviewCaseByName(cases, "already_decided_row_is_marked_stale");
    await mountReviewRoute(testCase);

    await screen.findByTestId("review-panel");
    await select(testCase.select);
    expect(screen.getByTestId("review-selection-count").textContent).toBe(`${testCase.select.length} selected`);

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "approve selected" }));
    });

    await waitFor(() =>
      expect(screen.getByTestId("review-stale-notice").textContent).toBe(testCase.expect.staleNotice),
    );
    // The decided row left the selection; the stale one is not silently
    // re-counted as still selected either.
    expect(screen.getByTestId("review-selection-count").textContent).toBe(
      `${testCase.expect.selectedAfter as number} selected`,
    );
    // The stale row is still on screen, marked, and no longer selectable.
    const staleRow = screen.getByTestId(`contribute-session-row-${testCase.alreadyDecided[0]}`);
    expect(within(staleRow).getByText("already decided")).toBeTruthy();
    expect((within(staleRow).getByRole("checkbox") as HTMLInputElement).disabled).toBe(true);
  });

  it("tells a non-owner the route is owner-only instead of rendering a reviewer's controls", async () => {
    const testCase = reviewCaseByName(cases, "non_owner_sees_the_owner_only_notice");
    await mountReviewRoute(testCase);

    await screen.findByTestId("review-non-owner-notice");
    expect(screen.queryByTestId("review-panel")).toBeNull();
    expect(screen.queryByRole("button", { name: "approve selected" })).toBeNull();
  });

  it("says nothing is waiting when the queue is empty", async () => {
    const testCase = reviewCaseByName(cases, "empty_queue_says_nothing_is_waiting");
    await mountReviewRoute(testCase);

    await screen.findByTestId("review-empty-queue");
    expect(screen.queryByTestId("review-panel")).toBeNull();
  });
});
