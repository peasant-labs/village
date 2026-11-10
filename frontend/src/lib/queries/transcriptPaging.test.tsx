import { readFileSync } from "node:fs";
import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { parse } from "yaml";
import { AuthProvider } from "@/providers/AuthProvider";
import { loadHelperGroupQueryFixtures } from "@/test/helperGroupQueryFixtures";
import { asTranscriptListRow, makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { useGroupedTranscripts } from "./helperGroups";
import { useTranscripts } from "./transcripts";
import type { TranscriptPaging } from "./transcriptPaging";

type Case = {
  name: string;
  kind: "flat" | "grouped";
  requested: Record<string, string>;
  response: Record<string, unknown> & { next_items?: string[] };
  paging?: TranscriptPaging;
  rejected?: boolean;
};
const { cases } = parse(readFileSync("src/testdata/transcript-paging-adapter.yaml", "utf8")) as { cases: Case[] };
const requiredNames = [
  "flat-with-following-page", "flat-without-following-page", "grouped-with-following-page", "grouped-last-page",
  "flat-wrong-echoed-page", "flat-wrong-echoed-size", "grouped-wrong-echoed-page", "grouped-wrong-echoed-size",
];

afterEach(() => vi.unstubAllGlobals());

it("retains the named paging adapter cases", () => {
  expect(cases.map(({ name }) => name).sort()).toEqual([...requiredNames].sort());
});

for (const fixture of cases) {
  it(fixture.name, async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const owner = loadHelperGroupQueryFixtures().owner;
    const response = {
      ...fixture.response,
      ...(fixture.response.next_items ? {
        next_items: fixture.response.next_items.map((id) => asTranscriptListRow({
          transcript: makeTranscriptFixture({ id }), tags: [], owner,
        })),
      } : {}),
    };
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      if (new URL(String(input)).pathname.endsWith("/auth/me")) {
        return new Response(JSON.stringify({ error: "signed out" }), { status: 401 });
      }
      return new Response(JSON.stringify(response), { status: 200 });
    }));
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}><AuthProvider>{children}</AuthProvider></QueryClientProvider>
    );
    const view = renderHook(() => {
      const flat = useTranscripts(fixture.requested, { enabled: fixture.kind === "flat" });
      const grouped = useGroupedTranscripts(fixture.requested, { enabled: fixture.kind === "grouped" });
      return fixture.kind === "flat" ? flat : grouped;
    }, { wrapper });
    await waitFor(() => expect(view.result.current.status).toBe(fixture.rejected ? "error" : "success"));
    if (fixture.rejected) {
      expect(view.result.current.data).toBeUndefined();
      expect(view.result.current.error).toBeInstanceOf(Error);
    } else {
      expect(view.result.current.data?.paging).toEqual(fixture.paging);
    }
    view.unmount();
    client.clear();
  });
}
