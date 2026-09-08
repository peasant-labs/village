import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import { loadHelperGroupQueryFixtures } from "@/test/helperGroupQueryFixtures";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { useGroupedTranscripts, useHelperGroupMembers } from "./helperGroups";

afterEach(() => vi.unstubAllGlobals());

const fixtures = loadHelperGroupQueryFixtures();
for (const fixture of fixtures.cases) {
  it(fixture.name, async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const calls: Array<{ url: URL; signal: AbortSignal | null | undefined }> = [];
    let status = fixture.status;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input));
      if (url.pathname.endsWith("/auth/me")) return new Response(JSON.stringify({ error: "signed out" }), { status: 401 });
      calls.push({ url, signal: init?.signal });
      if (status !== 200) return new Response(JSON.stringify({ code: "group_scope_expired", error: "refresh the originating list; no members returned" }), { status });
      const session = { ...makeTranscriptFixture(fixtures.row), input_submission_count: 0 };
      const response = fixture.kind === "list"
        ? { items: [{ kind: "transcript", transcript: { session } }], page: 1, limit: 20, totalItems: 1, ordinarySessionTotal: 1, helperThreadTotal: 0 }
        : { members: [{ session }], page: 1, limit: 20, total: 1 };
      return new Response(JSON.stringify(response), { status: 200 });
    }));
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}><AuthProvider>{children}</AuthProvider></QueryClientProvider>;
    const view = renderHook(() => ({
      list: useGroupedTranscripts(fixture.params, { enabled: fixture.kind === "list" }),
      members: useHelperGroupMembers(fixtures.group, { expanded: fixture.kind === "members" && fixture.expanded }),
    }), { wrapper });
    await waitFor(() => expect(client.getQueryState(["me"])?.status).toBe("error"));
    if (fixture.kind === "members" && !fixture.expanded) {
      expect(calls).toEqual([]);
      expect(view.result.current.members.data).toBeUndefined();
    } else {
      await waitFor(() => expect(fixture.kind === "list" ? view.result.current.list.status : view.result.current.members.status).toBe(fixture.status === 200 ? "success" : "error"));
      expect(calls).toHaveLength(1);
      expect(Object.fromEntries(calls[0].url.searchParams)).toEqual(fixture.request);
      expect(calls[0].signal).toBeInstanceOf(AbortSignal);
      if (fixture.kind === "members") {
        expect(calls[0].url.pathname).toContain(`/transcript-groups/${fixtures.group.groupId}/members`);
        expect(view.result.current.members.refreshRequired).toBe(fixture.status === 409);
        if (fixture.status === 200) {
          expect(view.result.current.members.data?.members[0].session.id).toBe(fixtures.row.id);
          expect(view.result.current.members.data?.members[0].session.input_submission_count).toBe(0);
          expect(view.result.current.members.data?.members[0].session.turn_count).toBe(5);
        }
      }
      if (fixture.refetchStatus) {
        status = fixture.refetchStatus;
        await act(async () => { await view.result.current.members.refetch(); });
        await waitFor(() => expect(view.result.current.members.isError).toBe(true));
        expect(view.result.current.members.data).toBeUndefined();
        expect(calls).toHaveLength(2);
        expect(calls[1].url.href).toBe(calls[0].url.href);
      }
    }
    view.unmount();
    client.clear();
  });
}
