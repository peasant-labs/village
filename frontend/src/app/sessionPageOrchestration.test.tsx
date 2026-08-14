import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { TranscriptListResponse } from "@/lib/types";
import {
  loadSessionPageOrchestrationFixtures,
  type OrchestrationQuery,
  type OrchestrationStep,
} from "@/test/sessionPageOrchestrationFixtures";

// Shared mutable holders visible to the hoisted module mocks below. The page
// consumes useTranscripts, the Explore surface, the two facet queries, the
// router, and the explore adapter; we mock exactly those dependencies so the
// test exercises the real page.tsx orchestration (settled gate, retained rows,
// rejected-key retention, aria-busy, live status, exact-key retry) without the
// network or the released Fairtrade component. Real-Fairtrade mounted behavior is
// owned by the separate mounted-evidence slice (village#27).
const h = vi.hoisted(() => ({
  query: { current: null as unknown },
  onFiltersChange: { current: null as ((next: unknown) => void) | null },
  refetch: vi.fn(),
}));

vi.mock("@/lib/queries/transcripts", () => ({
  useTranscripts: () => h.query.current,
}));
vi.mock("@/lib/queries/groups", () => ({
  useSearchCollectives: () => ({ data: { collectives: [] } }),
}));
vi.mock("@/lib/queries/tags", () => ({
  usePopularTags: () => ({ data: [] }),
}));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock("@/lib/adapters/explore", () => ({
  // Passthrough: surface the displayed list response's page so the test can
  // assert which page's rows page.tsx chose to present.
  adaptExplore: (list: TranscriptListResponse) => ({
    transcripts: { transcripts: [], total: list.total, page: list.page, limit: list.limit },
    collectives: [],
    popularTags: [],
  }),
}));
vi.mock("@peasant-labs/fairtrade/commons", async () => {
  const { createElement } = await import("react");
  return {
    Explore: (props: {
      data: { transcripts: { page: number; total: number } };
      onFiltersChange: (next: unknown) => void;
    }) => {
      h.onFiltersChange.current = props.onFiltersChange;
      return createElement("div", {
        "data-testid": "explore",
        "data-page": props.data.transcripts.page,
        "data-total": props.data.transcripts.total,
      });
    },
  };
});

// Imported after the mocks are declared (vi.mock is hoisted regardless).
import ExplorePage from "@/app/page";

const fixtures = loadSessionPageOrchestrationFixtures();

// One stable response object per data id, mirroring TanStack keeping a cached
// object reference across renders. Stable identity is required so page.tsx's
// guarded render-phase updates converge instead of looping.
const dataObjects = new Map<string, TranscriptListResponse>(
  Object.entries(fixtures.data).map(([id, value]) => [
    id,
    { transcripts: [], total: value.total, page: value.page, limit: value.limit },
  ]),
);

function buildQueryReturn(query: OrchestrationQuery) {
  return {
    data: query.dataId == null ? null : dataObjects.get(query.dataId)!,
    isLoading: query.isLoading,
    isFetching: query.isFetching,
    isPlaceholderData: query.isPlaceholderData,
    isError: query.isError,
    error: query.isError ? new Error("network unavailable") : null,
    refetch: h.refetch,
  };
}

function assertStep(step: OrchestrationStep): void {
  const expectation = step.expect;

  const alerts = screen.queryAllByRole("alert");
  expect(alerts.length > 0).toBe(expectation.alert);

  if (expectation.status != null) {
    expect(screen.getByTestId("session-list-status").textContent).toBe(expectation.status);
  }

  if (expectation.renders === "explore") {
    const results = screen.getByTestId("session-list-results");
    const explore = screen.getByTestId("explore");
    expect(Number(explore.getAttribute("data-page"))).toBe(expectation.displayedPage);
    if (expectation.ariaBusy != null) {
      expect(results.getAttribute("aria-busy")).toBe(String(expectation.ariaBusy));
    }
  } else if (expectation.renders === "errorSurface") {
    expect(screen.queryByTestId("session-list-results")).toBeNull();
    expect(screen.queryByTestId("explore")).toBeNull();
    expect(screen.getByText("Failed to load transcripts")).toBeInTheDocument();
  } else {
    expect(screen.queryByTestId("session-list-results")).toBeNull();
    expect(screen.queryByTestId("explore")).toBeNull();
    expect(screen.queryAllByRole("alert").length).toBe(0);
  }

  if (step.action === "clickRetry") {
    const retry = screen.getByRole("button", { name: /retry/i });
    act(() => {
      fireEvent.click(retry);
    });
    if (expectation.refetchCalled) {
      expect(h.refetch).toHaveBeenCalled();
    }
  }
}

afterEach(() => {
  cleanup();
  h.query.current = null;
  h.onFiltersChange.current = null;
  h.refetch.mockReset();
});

describe("mounted / session page orchestration", () => {
  for (const scenario of fixtures.scenarios) {
    it(scenario.name, () => {
      let rerenderPage: ReturnType<typeof render>["rerender"] | null = null;

      scenario.steps.forEach((step, index) => {
        h.query.current = buildQueryReturn(step.query);
        h.refetch.mockClear();

        if (index === 0) {
          const view = render(<ExplorePage />);
          rerenderPage = view.rerender;
        } else if (step.setFiltersPage != null) {
          const page = step.setFiltersPage;
          act(() => {
            h.onFiltersChange.current?.({
              query: "",
              provider: "all",
              topics: [],
              order: "recent",
              page,
            });
          });
        } else {
          act(() => {
            rerenderPage?.(<ExplorePage />);
          });
        }

        assertStep(step);
      });
    });
  }
});
