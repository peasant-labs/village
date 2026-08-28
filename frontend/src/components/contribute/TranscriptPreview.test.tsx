import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
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
      return <div data-testid="transcript-viewer" />;
    },
  };
});

vi.mock("@/providers/AuthProvider", () => ({
  useAuth: () => ({ user: null, isLoading: false, isLoggedIn: false }),
}));

vi.mock("@/lib/queries/transcripts", () => ({
  useTranscript: (id: string) => h.transcriptQuery.current(id),
  useTranscriptContent: (id: string) => h.contentQuery.current(id),
  useUpdateTranscript: () => ({ mutate: vi.fn() }),
  useTranscriptAnnotations: () => ({ data: { annotations: [] } }),
  useCreateTranscriptAnnotation: () => ({ mutateAsync: vi.fn() }),
}));

import TranscriptPreview from "@/components/contribute/TranscriptPreview";

function wrap(children: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

afterEach(() => {
  cleanup();
  h.viewerProps.current = null;
});

describe("TranscriptPreview (preview_renders_on_click)", () => {
  it("shows the empty state before any session is clicked, and renders the clicked session once it loads", () => {
    const c = caseByName(cases, "preview", "preview_renders_on_click");
    const row = toContributableTranscript(c.rows[0]);

    render(wrap(<TranscriptPreview transcriptId={null} />));
    expect(screen.getByTestId("transcript-preview-empty").textContent).toBe(c.expect.emptyStateText);
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
    h.contentQuery.current = () => ({ data: { turns: [] }, isLoading: false, error: null });

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
    h.contentQuery.current = () => ({ data: { turns: [] }, isLoading: false, error: null });

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
});
