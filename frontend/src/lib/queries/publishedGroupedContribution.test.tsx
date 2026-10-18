import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { AuthProvider } from "@/providers/AuthProvider";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useGroupedContributable } from "./groupedCollectives";

interface WireCase { name: string; requestedId: string; response: unknown; success: boolean; error: string }
const cases: WireCase[] = parse(readFileSync(resolve(process.cwd(), "src/testdata/published-grouped-contribution.yaml"), "utf8"), { strict: true });
const required = new Set(["published-canonical-key-renders-current-collective", "deprecated-only-key-refused-on-canonical-read", "canonical-other-collective-refused-before-caching"]);
for (const row of cases) if (!required.delete(row.name)) throw new Error(`Unknown/repeated published contribution case ${row.name}`);
if (required.size) throw new Error(`Missing published contribution cases: ${[...required].join(", ")}`);
afterEach(() => vi.unstubAllGlobals());

for (const row of cases) {
  it(row.name, async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input));
      if (url.pathname.endsWith("/auth/me")) return new Response(JSON.stringify({ error: "signed out" }), { status: 401 });
      expect(url.pathname).toBe(`/api/v1/collectives/${row.requestedId}/contributable`);
      expect(url.searchParams.get("view")).toBe("grouped");
      return new Response(JSON.stringify(row.response), { status: 200, headers: { "content-type": "application/json" } });
    }));
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}><AuthProvider>{children}</AuthProvider></QueryClientProvider>;
    const view = renderHook(() => useGroupedContributable(row.requestedId, { limit: 20 }), { wrapper });
    await waitFor(() => expect(view.result.current.status).toBe(row.success ? "success" : "error"));
    if (row.success) expect(view.result.current.data?.pages[0].group_id).toBe(row.requestedId);
    else {
      expect(view.result.current.error?.message).toContain(row.error);
      expect(view.result.current.data).toBeUndefined();
    }
    view.unmount(); client.clear();
  });
}
