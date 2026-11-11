import { readFileSync } from "node:fs";
import type { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { parse } from "yaml";
import { AllPageSizes, zPageSize, type PageSize } from "@peasant-labs/schema";
import { AuthProvider } from "@/providers/AuthProvider";
import { loadHelperGroupQueryFixtures } from "@/test/helperGroupQueryFixtures";
import { asTranscriptListRow, makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { useGroupedTranscripts } from "./helperGroups";
import { useTranscripts } from "./transcripts";
import { TranscriptPageSizeError, type TranscriptPaging } from "./transcriptPaging";

// Both directions must compile if the app's paging model and schema drift.
type MissingSchemaSize = Exclude<PageSize, TranscriptPaging["perPage"]>;
type ExtraAppSize = Exclude<TranscriptPaging["perPage"], PageSize>;
type MissingCatalogSize = Exclude<PageSize, (typeof AllPageSizes)[number]>;
type ExtraCatalogSize = Exclude<(typeof AllPageSizes)[number], PageSize>;
const coversSchema: MissingSchemaSize extends never ? true : never = true;
const onlySchemaSizes: ExtraAppSize extends never ? true : never = true;
const catalogCoversSchema: MissingCatalogSize extends never ? true : never = true;
const catalogIsLegal: ExtraCatalogSize extends never ? true : never = true;

type Case = {
  name: string;
  kind: "flat" | "grouped";
  requested: Record<string, string>;
  response: Record<string, unknown> & { next_items?: string[] };
  paging?: TranscriptPaging;
  rejected?: boolean;
  unsupportedSize?: number;
};
const { cases } = parse(readFileSync("src/testdata/transcript-paging-adapter.yaml", "utf8")) as { cases: Case[] };
const requiredNames = [
  "flat-with-following-page", "flat-without-following-page", "grouped-with-following-page", "grouped-last-page",
  "flat-wrong-echoed-page", "flat-wrong-echoed-size", "grouped-wrong-echoed-page", "grouped-wrong-echoed-size",
  "flat-size-50", "flat-size-100", "grouped-size-50", "grouped-size-100",
  "flat-unsupported-size", "grouped-unsupported-size", "flat-mismatched-unsupported-size", "grouped-mismatched-unsupported-size",
];

afterEach(() => vi.unstubAllGlobals());

it("retains the named paging adapter cases", () => {
  expect(cases.map(({ name }) => name).sort()).toEqual([...requiredNames].sort());
});

it("covers the schema's exact page-size set in each envelope", () => {
  expect(coversSchema && onlySchemaSizes && catalogCoversSchema && catalogIsLegal).toBe(true);
  const schemaSizes = zPageSize.options.map((option) => option.value).sort((a, b) => a - b);
  expect([...AllPageSizes].sort((a, b) => a - b)).toEqual(schemaSizes);
  for (const kind of ["flat", "grouped"] as const) {
    expect([...new Set(cases.filter((item) => item.kind === kind && item.paging).map((item) => item.paging!.perPage))].sort((a, b) => a - b)).toEqual(schemaSizes);
  }
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
      if (fixture.unsupportedSize !== undefined) {
        expect(view.result.current.error).toBeInstanceOf(TranscriptPageSizeError);
        expect((view.result.current.error as TranscriptPageSizeError).received).toBe(fixture.unsupportedSize);
        expect((view.result.current.error as Error).message).toContain(String(fixture.unsupportedSize));
        expect((view.result.current.error as Error).message).toContain(AllPageSizes.join(", "));
      }
    } else {
      expect(view.result.current.data?.paging).toEqual(fixture.paging);
    }
    view.unmount();
    client.clear();
  });
}
