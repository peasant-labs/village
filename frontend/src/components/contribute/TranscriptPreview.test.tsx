import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  caseByName,
  loadGroupsContributeTreeFixtures,
  toContributableTranscript,
} from "@/test/groupsContributeTreeFixtures";

const cases = loadGroupsContributeTreeFixtures();

// SessionDetailV2 mounts fairtrade's real TranscriptViewer, which brings its
// own graph engine + xyflow styles that this component test does not need to
// re-prove (that composite's own contract is exercised by the demo/DS
// gates). Mocked here so the assertions are about SessionDetailV2's WIRING
// (which capabilities/props it hands the composite), not the composite's own
// rendering -- the same idiom `sessionPageOrchestration.test.tsx` uses for
// the Explore composite.
const h = vi.hoisted(() => ({
  realMetadata: false,
  viewerProps: { current: null as Record<string, unknown> | null },
  transcriptQuery: {
    current: (id: string): { data: unknown; isLoading: boolean } => {
      void id;
      return { data: undefined, isLoading: true };
    },
  },
  contentQuery: {
    current: (id: string): { data: unknown; isLoading: boolean; error: unknown } => {
      void id;
      return { data: undefined, isLoading: true, error: null };
    },
  },
}));

vi.mock("@peasant-labs/fairtrade/ui", async () => {
  const actual = await vi.importActual<Record<string, unknown>>("@peasant-labs/fairtrade/ui");
  return {
    ...actual,
    TranscriptViewer: (props: Record<string, unknown>) => {
      h.viewerProps.current = props;
      return <div data-testid="transcript-viewer">{props.beforeHeader as React.ReactNode}</div>;
    },
  };
});

vi.mock("@/providers/AuthProvider", () => ({
  useAuth: () => ({ user: null, isLoading: false, isLoggedIn: false }),
}));

vi.mock("@/lib/queries/transcripts", async () => {
  const actual = await vi.importActual<typeof import("@/lib/queries/transcripts")>("@/lib/queries/transcripts");
  return {
  useTranscript: (id: string) => h.realMetadata ? actual.useTranscript(id) : h.transcriptQuery.current(id),
  useTranscriptContent: (id: string) => h.contentQuery.current(id),
  useUpdateTranscript: () => ({ mutate: vi.fn() }),
  useTranscriptAnnotations: () => ({ data: { annotations: [] } }),
  useCreateTranscriptAnnotation: () => ({ mutateAsync: vi.fn() }),
};
});

import TranscriptPreview from "@/components/contribute/TranscriptPreview";

function wrap(children: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

afterEach(() => {
  cleanup();
  h.viewerProps.current = null;
  h.realMetadata = false;
  vi.unstubAllGlobals();
});

describe("TranscriptPreview (preview_renders_on_click)", () => {
  it("shows the empty state before any session is clicked, and renders the clicked session once it loads", () => {
    const c = caseByName(cases, "preview", "preview_renders_on_click");
    const row = toContributableTranscript(c.rows[0]);

    render(wrap(<TranscriptPreview transcriptId={null} />));
    expect(screen.getByTestId("transcript-preview-empty").textContent).toContain(c.expect.emptyStateText);
    expect(screen.getByRole("heading", { name: "preview a transcript" })).toBeInTheDocument();
    cleanup();

    h.transcriptQuery.current = (id: string) =>
      id === row.id
        ? {
            data: {
              transcript: {
                id: row.id,
                local_id: row.local_id,
                title: row.title,
                description: null,
                visibility: row.visibility,
                project_hash: row.project_hash,
                project_display_name: row.project_display_name,
                session_origin: row.session_origin,
              },
              owner: { id: "owner-1", github_username: "owner" },
            },
            isLoading: false,
          }
        : { data: undefined, isLoading: true };
    h.contentQuery.current = () => ({ data: { content: { turns: [] }, contentHash: null }, isLoading: false, error: null });

    render(wrap(<TranscriptPreview transcriptId={c.expect.clickedId as string} />));
    expect(screen.getByTestId("transcript-viewer")).toBeInTheDocument();
    expect(screen.getByTestId("preview-header").textContent).toContain(row.title ?? "");
  });
});

describe("SessionDetailV2 preview variant (preview_hides_graph_and_owner_actions)", () => {
  it("hides the graph slot, header actions, and every owner capability", () => {
    const c = caseByName(cases, "preview", "preview_hides_graph_and_owner_actions");
    const row = toContributableTranscript(c.rows[0]);

    h.transcriptQuery.current = (id: string) =>
      id === row.id
        ? {
            data: {
              transcript: {
                id: row.id,
                local_id: row.local_id,
                title: row.title,
                description: null,
                visibility: row.visibility,
                project_hash: row.project_hash,
                project_display_name: row.project_display_name,
                session_origin: row.session_origin,
              },
              // Owner id deliberately DIFFERS from the (mocked, null) viewer,
              // and there is no signed-in user at all -- both would need to
              // hold for `isOwner` in the full variant, and neither must
              // matter in preview (the capability is forced off either way).
              owner: { id: "someone-else", github_username: "owner" },
            },
            isLoading: false,
          }
        : { data: undefined, isLoading: true };
    h.contentQuery.current = () => ({ data: { content: { turns: [] }, contentHash: null }, isLoading: false, error: null });

    render(wrap(<TranscriptPreview transcriptId={row.id} />));

    const props = h.viewerProps.current!;
    expect(props.headerActions).toBeUndefined();
    expect(props.graphSlot).toBeUndefined();
    expect(props.capabilities).toEqual({
      canLabel: false,
      canEdit: false,
      canChangeVisibility: false,
      canContribute: false,
      canExport: false,
    });

    const link = screen.getByTestId("preview-open-full-link");
    expect(link.getAttribute("href")).toBe(c.expect.openFullLinkHref);
  });

  it("hands the viewer the served integrity and both hashes, so a reviewing owner sees the flag", () => {
    const c = caseByName(cases, "preview", "preview_hides_graph_and_owner_actions");
    const row = toContributableTranscript(c.rows[0]);
    const rowHash = "a".repeat(64);
    const rendered = "b".repeat(64);
    h.transcriptQuery.current = () => ({
      data: {
        transcript: {
          id: row.id,
          local_id: row.local_id,
          title: row.title,
          description: null,
          visibility: row.visibility,
          content_hash: rowHash,
          project_hash: row.project_hash,
          project_display_name: row.project_display_name,
          session_origin: row.session_origin,
        },
        owner: { id: "someone-else", github_username: "owner" },
        integrity: { flagged_turns: [1] },
      },
      isLoading: false,
    });
    h.contentQuery.current = () => ({ data: { content: { turns: [] }, contentHash: rendered }, isLoading: false, error: null });

    render(wrap(<TranscriptPreview transcriptId={row.id} />));

    const props = h.viewerProps.current!;
    expect(props.integrity).toEqual({ flagged_turns: [1] });
    expect(props.transcriptContentHash).toBe(rowHash);
    expect(props.contentHash).toBe(rendered);
  });
});


describe("TranscriptPreview metadata read failures", () => {
  for (const fixture of cases.filter((c) => c.kind === "preview" && c.readFailures)) {
    it(fixture.name, async () => {
      if (fixture.kind !== "preview" || !fixture.readFailures) throw new Error("missing preview failures");
      const row = toContributableTranscript(fixture.rows[0]);
      h.realMetadata = true;
      h.contentQuery.current = () => ({ data: { content: { turns: [] }, contentHash: null }, isLoading: false, error: null });
      let releaseRetry: (() => void) | undefined;
      let calls = 0;
      const fetchMock = vi.fn(async (url: string) => {
        expect(url).toMatch(new RegExp(`/transcripts/${row.id}$`));
        calls += 1;
        if (calls === 1) return new Response(JSON.stringify({ error: fixture.expect.message }), { status: fixture.readFailures![0] });
        await new Promise<void>((resolve) => { releaseRetry = resolve; });
        return new Response(JSON.stringify({ transcript: row, owner: { id: "owner-1", github_username: "owner" } }), { status: 200 });
      });
      vi.stubGlobal("fetch", fetchMock);
      render(wrap(<TranscriptPreview transcriptId={row.id} />));
      if (!fixture.expect.retry) {
        expect(await screen.findByText("transcript not found")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "retry transcript preview" })).not.toBeInTheDocument();
        expect(calls).toBe(1);
        return;
      }
      expect(await screen.findByRole("alert")).toHaveTextContent(String(fixture.expect.message));
      expect(screen.queryByText("transcript not found")).not.toBeInTheDocument();
      const retry = screen.getByRole("button", { name: "retry transcript preview" });
      retry.focus();
      fireEvent.click(retry);
      await waitFor(() => expect(releaseRetry).toBeDefined());
      expect(retry).toHaveAttribute("aria-disabled", "true");
      expect(retry).toHaveFocus();
      fireEvent.click(retry);
      expect(calls).toBe(2);
      releaseRetry!();
      expect(await screen.findByTestId("transcript-viewer")).toBeInTheDocument();
      expect(screen.getByTestId("preview-header")).toHaveTextContent(row.title!);
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });
  }
});
