import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { createElement } from "react";
import { describe, expect, it, vi } from "vitest";
import ExplorePage from "./ExplorePage";
import { AuthProvider } from "@/providers/AuthProvider";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import type { User } from "@/lib/types";

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }) }));

function user(id: string): User {
  return { id, github_id: 1, github_username: id, display_name: null, avatar_url: null, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", is_discoverable: true, username_chosen: true, provider_username: null };
}

describe("mounted Explore viewer-scoped retention", () => {
  it("viewer-a-to-b-clears-last-confirmed-during-pending-and-failure", async () => {
    const viewerA = user("viewer-a");
    let rejectViewerB: (() => void) | undefined;
    let transcriptCalls = 0;
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/transcripts?")) {
        transcriptCalls++;
        if (transcriptCalls > 1) return new Promise<Response>((_, reject) => { rejectViewerB = () => reject(new Error("viewer request failed")); });
        const transcript = makeTranscriptFixture({ id: "viewer-a-row", title: "viewer a private marker" });
        return new Response(JSON.stringify({ transcripts: [{ transcript, tags: [], owner: viewerA }], harness_facets: [], total: 1, agent_total: 0, page: 1, limit: 24 }), { status: 200, headers: { "content-type": "application/json" } });
      }
      if (url.includes("/groups/search")) return new Response(JSON.stringify({ collectives: [] }), { status: 200, headers: { "content-type": "application/json" } });
      if (url.includes("/tags/popular")) return new Response(JSON.stringify([]), { status: 200, headers: { "content-type": "application/json" } });
      throw new Error("unexpected mounted Explore request");
    }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.setQueryData(["me"], viewerA);
    const view = render(createElement(QueryClientProvider, { client }, createElement(AuthProvider, null, createElement(ExplorePage))));
    await waitFor(() => expect(document.querySelector('a[href="/transcripts/viewer-a-row"]')).not.toBeNull());
    act(() => client.setQueryData(["me"], user("viewer-b")));
    view.rerender(createElement(QueryClientProvider, { client }, createElement(AuthProvider, null, createElement(ExplorePage))));
    await waitFor(() => expect(transcriptCalls).toBe(2));
    expect(document.querySelector('a[href="/transcripts/viewer-a-row"]')).toBeNull();
    act(() => rejectViewerB?.());
    expect(await screen.findByRole("alert")).toHaveTextContent("viewer request failed");
    expect(document.querySelector('a[href="/transcripts/viewer-a-row"]')).toBeNull();
    vi.unstubAllGlobals();
  });
});
