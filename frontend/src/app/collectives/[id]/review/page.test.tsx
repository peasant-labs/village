import { afterEach, describe, expect, it, vi } from "vitest";
import { Suspense } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthProvider } from "@/providers/AuthProvider";
import GroupReviewPage from "@/app/collectives/[id]/review/page";
import {
  loadGroupsReviewPageFixtures,
  reviewCaseByName,
  toPendingShare,
  type ReviewPageCase,
} from "@/test/groupsReviewPageFixtures";
import { profileOwner } from "@/test/publicOwnerFixture";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";

/**
 * The REAL `/collectives/{id}/review` route, mounted inside the real
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
  // A case may ask the queue to CHANGE under the reviewer: the first read
  // answers the full list, every later read answers only the named rows.
  const afterRefetch =
    testCase.pendingAfterRefetch == null
      ? null
      : pending.filter((row) => testCase.pendingAfterRefetch!.includes(row.transcript_id));
  let pendingReads = 0;
  let decisionWrites = 0;

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
    if (url.includes(`/collectives/${GROUP_ID}/pending`)) {
      if (!url.endsWith(`/collectives/${GROUP_ID}/pending`)) return json([]);
      pendingReads += 1;
      const failedStatus = testCase.readFailures?.[pendingReads - 1];
      if (failedStatus) return json({ error: "the queue could not be read" }, failedStatus);
      return json(afterRefetch != null && pendingReads > 1 ? afterRefetch : pending);
    }
    if (url.endsWith(`/collectives/${GROUP_ID}/shares`) && method === "PATCH") {
      const failure = testCase.decisionFailures?.[decisionWrites++];
      if (failure) return json({ error: "the decision could not be applied" }, failure);
      return json({ decided: testCase.decided, already_decided: testCase.alreadyDecided });
    }
    const transcriptMatch = url.match(/\/transcripts\/([^/?]+)(\/[^?]+)?$/);
    if (transcriptMatch) {
      const row = pending.find((item) => item.transcript_id === transcriptMatch[1]);
      if (transcriptMatch[2] === "/annotations") return json([]);
      if (transcriptMatch[2] === "/collectives") return json({ collectives: [] });
      if (transcriptMatch[2] === "/pull-requests") return json({ pull_requests: [], unverified_pull_requests: [] });
      if (transcriptMatch[2] === "/content") return json({ turns: [], harness: "claude-code" });
      if (row) return json({ transcript: makeTranscriptFixture({ id: row.transcript_id, title: row.title, local_id: row.local_id }), owner: row.owner, tags: [], shares: [] });
    }
    if (new RegExp(`/collectives/${GROUP_ID}$`).test(url)) {
      return json({
        group: {
          id: GROUP_ID,
          name: "review collective",
          description: null,
          linked_github_org: null,
          display_members: true,
          transcript_deletion_policy: "user_choice",
          creator: profileOwner({ id: "user-mod", github_username: "collective-owner" }),
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

/** Mounts the real route and hands back BOTH the recorded requests (so a test
 *  asserts a parsed body) and the query client (so a test can force the
 *  refetch that a real queue does on its own). */
async function mountReviewRoute(
  testCase: ReviewPageCase,
): Promise<{ client: QueryClient; requests: RecordedRequest[] }> {
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
  return { client, requests };
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

describe("/collectives/{id}/review", () => {
  it("shows submissions directly with inline project context", async () => {
    const testCase = reviewCaseByName(cases, "owner_reads_sessions_with_inline_project_context");
    await mountReviewRoute(testCase);

    await screen.findByTestId("review-panel");
    expect(screen.getByRole("textbox", { name: "search sessions" })).toHaveClass("input", "is-input");
    for (const label of testCase.expect.projectLabels as string[]) {
      expect(screen.getByText(label)).toBeTruthy();
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

    // Selecting all reaches the folded child, which stays off screen.
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "select all" }));
    });
    expect(control.getAttribute("aria-expanded")).toBe("false");

    // The bar counts the hidden row, so what it offers to decide is what a
    // decision would actually reach.
    expect(screen.getByTestId("review-selection-count").textContent).toBe(
      `${testCase.expect.selectedAfterSelectingAll as number} selected`,
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

  it("drops a ticked row from the count once the queue no longer holds it", async () => {
    const testCase = reviewCaseByName(cases, "a_row_that_leaves_the_queue_leaves_the_selection");
    const { client } = await mountReviewRoute(testCase);
    await screen.findByTestId("review-panel");

    await select(testCase.select);
    expect(screen.getByTestId("review-selection-count").textContent).toBe(
      `${testCase.expect.selectedBeforeRefetch as number} selected`,
    );

    // Someone else decides one of them. The queue is refetched and no longer
    // holds that row, so it is gone from the tree — but its id is still in
    // the reviewer's selection, where a naive count would go on counting a
    // row that is nowhere on screen.
    await act(async () => {
      await client.invalidateQueries({ queryKey: ["group-pending", GROUP_ID] });
    });
    await waitFor(() =>
      expect(screen.queryByTestId(`contribute-session-row-${testCase.select[1]}`)).toBeNull(),
    );

    expect(screen.getByTestId("review-selection-count").textContent).toBe(
      `${testCase.expect.selectedAfterRefetch as number} selected`,
    );
    // The row that survived is still ticked: reconciling the count must not
    // quietly clear a selection the reviewer still holds.
    const survivor = screen.getByTestId(`contribute-session-row-${testCase.select[0]}`);
    expect((within(survivor).getByRole("checkbox") as HTMLInputElement).checked).toBe(true);
  });

  it("approves a selection spanning two projects in ONE request", async () => {
    const testCase = reviewCaseByName(cases, "approve_selection_sends_one_request");
    const { requests } = await mountReviewRoute(testCase);

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
    const { requests } = await mountReviewRoute(testCase);

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
    await waitFor(() => expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1));
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


describe("review queue read failures", () => {
  it.each(cases.filter((c) => c.readFailures != null))("$name", async (testCase) => {
    const { requests } = await mountReviewRoute(testCase);
    const retry = await screen.findByRole("button", { name: "retry review queue" });
    await waitFor(() => expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1));
    for (let read = 1; read <= testCase.readFailures!.length; read += 1) {
      expect(screen.getByRole("alert")).toHaveTextContent("the review queue could not load");
      expect(screen.queryByTestId("review-empty-queue")).not.toBeInTheDocument();
      retry.focus();
      fireEvent.click(retry);
      await waitFor(() => expect(requests.filter((r) => r.url.endsWith("/pending"))).toHaveLength(read + 1));
      if (read < testCase.readFailures!.length) {
        await waitFor(() => expect(retry).not.toHaveAttribute("aria-disabled", "true"));
        expect(retry).toHaveFocus();
      }
    }
    for (const id of testCase.expect.sessionIds as string[]) {
      expect(await screen.findByTestId(`contribute-session-row-${id}`)).toBeInTheDocument();
    }
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});


describe("direct review decisions", () => {
  it.each(cases.filter((c) => c.name.startsWith("quick_")))("$name", async (testCase) => {
    const { requests } = await mountReviewRoute(testCase);
    await screen.findByTestId("review-panel");
    const clickedID = testCase.expect.clickedId as string;
    const row = screen.getByTestId(`contribute-session-row-${clickedID}`);
    // A preview never grants selection or sends a decision.
    expect(within(row).getByRole("button", { name: clickedID })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByTestId("review-selection-count")).toHaveTextContent("0 selected");
    expect(requests.filter((request) => request.method === "PATCH")).toHaveLength(0);
    await select(testCase.select);
    const action = within(row).getByRole("button", { name: `${testCase.expect.quickStatus === "approved" ? "approve" : "reject"} ${clickedID}` });
    await act(async () => { fireEvent.click(action); });
    if (testCase.decisionFailures) {
      expect(await within(row).findByRole("alert")).toHaveTextContent(testCase.expect.error as string);
      expect(screen.getByTestId("review-selection-count")).toHaveTextContent(`${testCase.select.length} selected`);
      await act(async () => { fireEvent.click(action); });
    }
    await waitFor(() => expect(screen.getByTestId("review-selection-count")).toHaveTextContent(`${testCase.expect.selectedAfter} selected`));
    expect(requests.filter((request) => request.method === "PATCH").at(-1)?.body).toEqual({
      transcript_ids: testCase.expect.requestIds,
      status: testCase.expect.quickStatus,
    });
  });
});
