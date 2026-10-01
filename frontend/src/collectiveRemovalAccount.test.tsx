import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { clearAuthTokenCookie, getAuthHeaders, setAuthTokenCookie } from "@/lib/api";
import type { GroupTranscript, User, UserGroupShare } from "@/lib/types";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { installGroupRouteREST, renderGroupDetailRoute, type GroupRouteFixture } from "@/test/mountedGroupRoute";
import { loadCollectiveRemovalAccountFixtures } from "@/test/collectiveRemovalAccountFixtures";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); clearAuthTokenCookie(); });

for (const testCase of loadCollectiveRemovalAccountFixtures()) {
  it(testCase.name, async () => {
    const first = "private-first";
    const second = "private-second";
    const rows = [first, second].map((id): GroupTranscript => ({ ...makeTranscriptFixture({ id, title: id, owner_id: "user-ada", visibility: "private" }), license_id: null, outcome: null, source_format: null, subagents: null, owner_username: "ada", owner_avatar_url: null, owner_is_discoverable: true, pull_requests: { count: 0, recent: [] } }));
    const own: UserGroupShare = { id: "private-own", owner_id: "user-ada", local_id: "own", parent_session_id: null, title: "private-own", model_provider: "claude-code", model_name: null, visibility: "private", published_at: "2026-01-01T00:00:00Z", turn_count: 1, tokens_in: null, tokens_out: null, status: "pending", shared_at: "2026-01-01T00:00:00Z" };
    const world: GroupRouteFixture = { groupId: "account-collective", groupName: "account collective", viewer: "ada", role: "owner", transcripts: rows, myShares: [own] };
    installGroupRouteREST(world);
    const delegate = globalThis.fetch;
    const deletes: { path: string; authorization: string | null }[] = [];
    let release!: () => void;
    const held = new Promise<void>((resolve) => { release = resolve; });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "DELETE") {
        deletes.push({ path: new URL(String(input)).pathname, authorization: new Headers(init.headers).get("Authorization") });
        if (deletes.length === 1) {
          await held;
          if (testCase.responseStatus === 500) return new Response(JSON.stringify({ error: "original account failure" }), { status: 500, headers: { "content-type": "application/json" } });
        }
      }
      return delegate(input, init);
    }));
    setAuthTokenCookie("first-credential");
    const client = await renderGroupDetailRoute(world.groupId);
    fireEvent.click(await screen.findByTestId("collective-library-disclosure-toggle"));
    const library = await screen.findByTestId("collective-transcript-library");
    await screen.findByText("private-own");
    fireEvent.click(within(library).getByRole("checkbox", { name: "select all" }));
    fireEvent.click(within(library).getByRole("button", { name: "remove selected (2)" }));
    expect(deletes).toEqual([]);
    fireEvent.click(within(library).getByRole("button", { name: "yes" }));
    if (testCase.timing === "before-dispatch") {
      setAuthTokenCookie("second-credential");
      await act(async () => { release(); await held; });
      await waitFor(() => expect(client.isMutating()).toBe(0));
      expect(deletes).toEqual([]);
      expect(within(library).getByRole("button", { name: "remove selected (0)" })).toBeDisabled();
      return;
    }
    await waitFor(() => expect(deletes).toEqual([{ path: `/api/v1/groups/${world.groupId}/transcripts/${first}`, authorization: "Bearer first-credential" }]));
    if (testCase.switch === "identity") {
      world.viewer = "bea";
      world.transcripts = [];
      world.myShares = [];
      await act(async () => { await client.invalidateQueries({ queryKey: ["me"] }); });
      await waitFor(() => expect(client.getQueryData<User>(["me"])?.id).toBe("user-bea"));
      // The actual flat reads must follow the new viewer, before the old DELETE resolves.
      await waitFor(() => expect(screen.queryByText("private-own")).toBeNull());
      expect(screen.queryByText(second)).toBeNull();
    } else {
      setAuthTokenCookie("second-credential");
      expect(getAuthHeaders().Authorization).toBe("Bearer second-credential");
    }
    await act(async () => { release(); await held; });
    await waitFor(() => expect(client.isMutating()).toBe(0));
    expect(deletes).toEqual([{ path: `/api/v1/groups/${world.groupId}/transcripts/${first}`, authorization: "Bearer first-credential" }]);
    expect(screen.queryByText(/original account failure/)).toBeNull();
    if (testCase.switch === "credential") expect(within(library).getByRole("button", { name: "remove selected (0)" })).toBeDisabled();
  });
}
