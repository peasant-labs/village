import { act, cleanup, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { clearAuthTokenCookie, setAuthTokenCookie } from "@/lib/api";
import type { GroupTranscript } from "@/lib/types";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { installGroupRouteREST, renderGroupDetailRoute, type GroupRouteFixture } from "@/test/mountedGroupRoute";
import { loadCollectiveReadAccountFixtures } from "@/test/collectiveReadAccountFixtures";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); clearAuthTokenCookie(); });
for (const testCase of loadCollectiveReadAccountFixtures()) {
  it(testCase.name, async () => {
    const row: GroupTranscript = { ...makeTranscriptFixture({ id: "private-session", title: "initial-private", visibility: "private" }), license_id: null, outcome: null, source_format: null, subagents: null, owner_username: "ada", owner_avatar_url: null, owner_is_discoverable: true, pull_requests: { count: 0, recent: [] } };
    const world: GroupRouteFixture = { groupId: "read-collective", groupName: "read collective", viewer: "ada", role: "owner", transcripts: [row] };
    installGroupRouteREST(world);
    const delegate = globalThis.fetch;
    let hold = false;
    let release!: () => void;
    const held = new Promise<void>((resolve) => { release = resolve; });
    const reads: (string | null)[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input));
      const flat = url.pathname === `/api/v1/groups/${world.groupId}` && !url.search;
      if (flat) reads.push(new Headers(init?.headers).get("Authorization"));
      const response = await delegate(input, init);
      if (url.pathname === `/api/v1/groups/${world.groupId}` && !url.search) {
        if (hold) await held;
      }
      return response;
    }));
    setAuthTokenCookie("initial-credential");
    const client = await renderGroupDetailRoute(world.groupId);
    await screen.findByText("initial-private");
    const original = client.getQueryCache().findAll({ queryKey: ["group", world.groupId] }).find((query) => query.queryKey[2] === "flat" && query.state.status === "success")!;
    expect(original).toBeDefined();
    const priorReads = reads.length;
    row.title = "late-private-response";
    hold = testCase.timing === "after-dispatch";
    if (testCase.timing === "before-dispatch") setAuthTokenCookie("replacement-credential");
    const pending = client.refetchQueries({ queryKey: original.queryKey, exact: true });
    if (testCase.timing === "before-dispatch") expect(reads.length).toBe(priorReads);
    if (testCase.timing === "after-dispatch") {
      await waitFor(() => expect(reads.length).toBe(priorReads + 1));
      setAuthTokenCookie("replacement-credential");
    }
    await act(async () => { release(); await pending; });
    expect(JSON.stringify(original.state.data)).not.toContain("late-private-response");
    expect(original.state.error?.message).toMatch(/signed-in account changed/);
    // Returning to the original credential creates a fresh scope, rather than
    // reusing an earlier credential's cached response.
    const priorVersion = original.queryKey.at(-1);
    await waitFor(() => {
      const replacement = client.getQueryCache().findAll({ queryKey: ["group", world.groupId] }).find((query) => query.getObserversCount() > 0 && query.queryKey[2] === "flat");
      expect(replacement?.queryKey.at(-1)).not.toBe(priorVersion);
      expect(replacement?.state.status).toBe("success");
    });
    setAuthTokenCookie("initial-credential");
    row.title = "current-private-response";
    await act(async () => { await client.invalidateQueries({ queryKey: ["group", world.groupId] }); });
    await screen.findByText("current-private-response");
    const current = client.getQueryCache().findAll({ queryKey: ["group", world.groupId] }).find((query) => query.getObserversCount() > 0 && query.queryKey[2] === "flat")!;
    expect(current.queryKey.at(-1)).not.toBe(priorVersion);
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.queryKey))).not.toContain("credential");
  });
}
