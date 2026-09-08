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
      if (url.searchParams.get("scope") === fixtures.nestedGroup.memberScope) {
        if (fixture.nestedStatus !== 200) return new Response(JSON.stringify({code: "group_scope_expired", error: "refresh the originating list"}), {status: fixture.nestedStatus});
        const session = {...makeTranscriptFixture(fixtures.nestedRow), input_submission_count: 0, purpose: "helper_review"};
        return new Response(JSON.stringify({members: [{kind: "transcript", transcript: {session}}],page:1,limit:20,total:1}), {status:200});
      }
      if (status !== 200) return new Response(JSON.stringify({ code: "group_scope_expired", error: "refresh the originating list; no members returned" }), { status });
      const session = { ...makeTranscriptFixture(fixtures.row), input_submission_count: 0, purpose: fixture.kind === "members" ? "helper_review" : "interaction" };
      const response = fixture.kind === "list"
        ? { items: [{ kind: "transcript", transcript: { session } }], page: 1, limit: 20, totalItems: 1, ordinarySessionTotal: 1, helperThreadTotal: 0 }
        : { members: [{ kind: "transcript", transcript: { session }, ...(fixture.nestedStatus ? {helperGroups: [fixtures.nestedGroup]} : {}) }], page: 1, limit: 20, total: 1 };
      return new Response(JSON.stringify(response), { status: 200 });
    }));
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}><AuthProvider>{children}</AuthProvider></QueryClientProvider>;
    const view = renderHook(({expandNested}) => {
      const list = useGroupedTranscripts(fixture.params, {enabled: fixture.kind === "list"});
      const members = useHelperGroupMembers(fixtures.group, {expanded: fixture.kind === "members" && fixture.expanded});
      const nested = useHelperGroupMembers(members.data?.members[0].helperGroups?.[0], {expanded: expandNested});
      return {list,members,nested};
    }, { wrapper, initialProps: {expandNested: false} });
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
          expect(view.result.current.members.data?.members[0].transcript?.session.id).toBe(fixtures.row.id);
          expect(view.result.current.members.data?.members[0].transcript?.session.input_submission_count).toBe(0);
          expect(view.result.current.members.data?.members[0].transcript?.session.turn_count).toBe(5);
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
      if (fixture.nestedStatus) {
        expect(view.result.current.nested.data).toBeUndefined();
        view.rerender({expandNested: true});
        await waitFor(() => expect(view.result.current.nested.status).toBe(fixture.nestedStatus === 200 ? "success" : "error"));
        expect(calls).toHaveLength(2);
        expect(calls[1].url.pathname).toContain(`/transcript-groups/${fixtures.nestedGroup.groupId}/members`);
        expect(Object.fromEntries(calls[1].url.searchParams)).toEqual({scope:fixtures.nestedGroup.memberScope,page:"1",limit:"20"});
        expect(view.result.current.members.data?.members[0].transcript?.session.id).toBe(fixtures.row.id);
        if (fixture.nestedStatus === 200) {
          expect(view.result.current.nested.data?.members).toHaveLength(1);
          expect(view.result.current.nested.data?.members[0].transcript?.session.id).toBe(fixtures.nestedRow.id);
        } else {
          expect(view.result.current.nested.data).toBeUndefined();
          expect(view.result.current.nested.refreshRequired).toBe(true);
        }
      }
    }
    view.unmount();
    client.clear();
  });
}
