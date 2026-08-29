import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Group } from "@/lib/types";
import type { ContributableResponse } from "@/lib/contribute/types";
import {
  caseByName,
  loadGroupsContributeTreeFixtures,
  toContributableTranscript,
} from "@/test/groupsContributeTreeFixtures";
import GroupContributePage from "@/app/groups/[id]/contribute/page";

const cases = loadGroupsContributeTreeFixtures();

interface RecordedRequest {
  method: string;
  url: string;
  body: unknown;
}

function makeGroup(id: string): Group {
  return {
    id,
    name: "confirm collective",
    description: null,
    linked_github_org: null,
    display_members: true,
    transcript_deletion_policy: "user_choice",
    created_by: "someone-else",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    acceptance_mode: "open",
    data_access: "members_only",
    role: "member",
    member_since: "2026-01-01T00:00:00Z",
  };
}

/** Stubs `fetch` for the real mounted `/groups/{id}/contribute` route: the
 *  group detail, the contributable listing, and the batch-share POST. Records
 *  every request so a test asserts the parsed BODY, never a call count
 *  alone. */
function installContributeRouteREST(groupId: string, contributable: ContributableResponse): RecordedRequest[] {
  const requests: RecordedRequest[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method ?? "GET").toUpperCase();
    const body = typeof init?.body === "string" ? JSON.parse(init.body) : null;
    requests.push({ method, url, body });

    if (url.endsWith(`/groups/${groupId}`)) {
      return new Response(
        JSON.stringify({
          group: makeGroup(groupId),
          members: [{ user_id: "someone-else", role: "owner" }],
          transcripts: [],
          stats: {},
          models: [],
          contributors: [],
          can_read: true,
          your_role: "member",
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    }
    if (url.endsWith(`/groups/${groupId}/contributable`)) {
      return new Response(JSON.stringify(contributable), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }
    if (url.endsWith(`/groups/${groupId}/shares`)) {
      return new Response(
        JSON.stringify({
          project_hash: body.project_hash,
          shared: (body.transcript_ids as string[]).map((id) => ({ transcript_id: id, status: "approved" })),
          already_shared: [],
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    }
    throw new Error(`contribute-route fixture received an unexpected ${method} request to ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return requests;
}

function renderRoute(groupId: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <GroupContributePage params={Promise.resolve({ id: groupId })} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("the real /groups/{id}/contribute route (private_selection_opens_confirm)", () => {
  it("opens the confirm dialog before any request, then sends exactly the confirmed batch", async () => {
    const c = caseByName(cases, "page", "private_selection_opens_confirm");
    const rows = c.rows.map(toContributableTranscript);
    const groupId = "group-confirm";
    const requests = installContributeRouteREST(groupId, { group_id: groupId, transcripts: rows });

    await act(async () => {
      renderRoute(groupId);
    });

    await waitFor(() => expect(screen.getByTestId(`contribute-session-row-${c.expect.privateSelectionId}`)).toBeInTheDocument());

    const privateRow = screen.getByTestId(`contribute-session-row-${c.expect.privateSelectionId}`);
    const checkbox = privateRow.querySelector('input[type="checkbox"]')!;
    act(() => {
      fireEvent.click(checkbox);
    });

    const contributeButton = screen.getByRole("button", { name: /contribute 1 transcript/ });
    act(() => {
      fireEvent.click(contributeButton);
    });

    // The dialog must open BEFORE any request fires.
    expect(await screen.findByText(/make 1 transcript visible/)).toBeInTheDocument();
    const shareRequestsBeforeConfirm = requests.filter((r) => r.url.endsWith("/shares"));
    expect(shareRequestsBeforeConfirm).toHaveLength(c.expect.requestsBeforeConfirm as number);

    const consentCheckbox = screen.getByRole("checkbox", { name: /i understand and consent/i });
    act(() => {
      fireEvent.click(consentCheckbox);
    });
    const confirmButton = screen.getByRole("button", { name: /contribute & make visible/i });
    await act(async () => {
      fireEvent.click(confirmButton);
    });

    await waitFor(() => {
      const shareRequests = requests.filter((r) => r.url.endsWith("/shares"));
      expect(shareRequests).toHaveLength(1);
    });
    const shareRequest = requests.find((r) => r.url.endsWith("/shares"))!;
    expect(shareRequest.body).toEqual(c.expect.expectedBodyAfterConfirm);
  });
});

/** The tally is one element whose text React splits across several nodes;
 *  compare its collapsed textContent so the assertion reads as the sentence a
 *  viewer sees. */
function tallyText(): string {
  return screen.getByTestId("contribute-selection-tally").textContent!.replace(/\s+/g, " ").trim();
}

describe("the tree header's selection tally (header_counts_selected_and_sessions)", () => {
  it("counts the selectable sessions, not the already-contributed row, and follows a tick", async () => {
    const c = caseByName(cases, "page", "header_counts_selected_and_sessions");
    const rows = c.rows.map(toContributableTranscript);
    const groupId = "group-tally";
    installContributeRouteREST(groupId, { group_id: groupId, transcripts: rows });

    await act(async () => {
      renderRoute(groupId);
    });

    await waitFor(() => expect(screen.getByTestId("contribute-selection-tally")).toBeInTheDocument());
    expect(tallyText()).toBe(c.expect.initialTally as string);

    const row = screen.getByTestId(`contribute-session-row-${c.expect.tickId}`);
    act(() => {
      fireEvent.click(row.querySelector('input[type="checkbox"]')!);
    });
    expect(tallyText()).toBe(c.expect.tallyAfterTick as string);
  });
});

describe("the tree header's select-all control (select_all_selects_every_leaf)", () => {
  it("ticks every selectable leaf in every project, skips an already-contributed row, and clears again", async () => {
    const c = caseByName(cases, "page", "select_all_selects_every_leaf");
    const rows = c.rows.map(toContributableTranscript);
    const groupId = "group-select-all";
    const requests = installContributeRouteREST(groupId, { group_id: groupId, transcripts: rows });

    await act(async () => {
      renderRoute(groupId);
    });

    await waitFor(() => expect(screen.getByRole("button", { name: "select all" })).toBeInTheDocument());
    act(() => {
      fireEvent.click(screen.getByRole("button", { name: "select all" }));
    });
    expect(tallyText()).toBe(c.expect.tallyAfterSelectAll as string);

    act(() => {
      fireEvent.click(screen.getByRole("button", { name: "deselect all" }));
    });
    expect(tallyText()).toBe(c.expect.tallyAfterDeselectAll as string);

    act(() => {
      fireEvent.click(screen.getByRole("button", { name: "select all" }));
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /contribute 2 transcripts/ }));
    });

    const expectedByProject = c.expect.requestsByProject as Record<string, string[]>;
    await waitFor(() => {
      expect(requests.filter((r) => r.url.endsWith("/shares"))).toHaveLength(
        Object.keys(expectedByProject).length,
      );
    });
    const bodies = requests
      .filter((r) => r.url.endsWith("/shares"))
      .map((r) => r.body as { project_hash: string; transcript_ids: string[] });
    for (const [projectHash, ids] of Object.entries(expectedByProject)) {
      const body = bodies.find((b) => b.project_hash === projectHash);
      expect(body, `no request was sent for project ${projectHash}`).toBeDefined();
      expect(body!.transcript_ids).toEqual(ids);
    }
    // The already-contributed row can never be re-sent by "select all".
    for (const body of bodies) {
      expect(body.transcript_ids).not.toContain(c.expect.forbiddenBodyId as string);
    }
  });
});

describe("a session started by another session (starter_selection_takes_its_started_session)", () => {
  it("reads behind the shared control, and is contributed with the session that started it", async () => {
    const c = caseByName(cases, "page", "starter_selection_takes_its_started_session");
    const rows = c.rows.map(toContributableTranscript);
    const groupId = "group-nesting";
    installContributeRouteREST(groupId, { group_id: groupId, transcripts: rows });

    await act(async () => {
      renderRoute(groupId);
    });

    const starterId = c.expect.starterId as string;
    const startedId = c.expect.startedId as string;
    await waitFor(() =>
      expect(screen.getByTestId(`contribute-session-row-${starterId}`)).toBeInTheDocument(),
    );

    // The started session is inside a collapsed control, so it is not a second
    // flat row of the tree.
    expect(screen.queryByTestId(`contribute-session-row-${startedId}`)).toBeNull();

    // The control hangs off the row that started it, and is the SAME control
    // every other session list in this app draws -- the marker attribute and
    // the test ids are the shared ones, not a second control of the tree's own.
    const chip = document.querySelector<HTMLElement>(
      `[data-parent-transcript-id="${starterId}"]`,
    );
    expect(chip, "the tree draws the shared control under the session that started it").not.toBeNull();
    // The EXACT text. A containment check would still pass on the leading `+`
    // the tree announced while it ran a fold of its own.
    expect(within(chip!).getByTestId("child-session-disclosure-label").textContent).toBe(
      c.expect.disclosureLabel as string,
    );
    expect(tallyText()).toBe(c.expect.initialTally as string);

    act(() => {
      fireEvent.click(within(chip!).getByTestId("child-session-disclosure-toggle"));
    });
    const revealed = within(chip!).getByTestId("child-session-disclosure-rows");
    expect(within(revealed).getByTestId(`contribute-session-row-${startedId}`)).toBeInTheDocument();

    // Ticking the session that started it takes the started session too: a
    // person choosing a session is choosing the work it did.
    const starterRow = screen.getByTestId(`contribute-session-row-${starterId}`);
    act(() => {
      fireEvent.click(starterRow.querySelector('input[type="checkbox"]')!);
    });

    expect(tallyText()).toBe(c.expect.tallyAfterStarterTick as string);
    const startedRow = screen.getByTestId(`contribute-session-row-${startedId}`);
    expect(
      (startedRow.querySelector('input[type="checkbox"]') as HTMLInputElement).checked,
      "the started session is selected with the session that started it",
    ).toBe(true);
  });
});
