import { afterEach, describe, expect, it, vi } from "vitest";
import { TRANSCRIPT_LIST_ENDPOINT } from "@/lib/transcriptPageRequest";
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
// test exercises the real page.tsx orchestration (retained rows, aria-busy,
// live status, exact-key retry) without the
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
import ExplorePage from "@/app/explore/ExplorePage";

const fixtures = loadSessionPageOrchestrationFixtures();

// One stable response object per data id, mirroring TanStack keeping a cached
// object reference across renders.
const dataObjects = new Map<string, TranscriptListResponse>(
  Object.entries(fixtures.data).map(([id, value]) => [
    id,
    { transcripts: [], total: value.total, agent_total: 0, page: value.page, limit: value.limit },
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
  // Names the step in every message: these scenarios run several phases inside
  // one test, so a bare assertion failure would not say which phase broke.
  const location = step.name;

  const alerts = screen.queryAllByRole("alert");
  expect(alerts.length > 0).toBe(expectation.alert);

  if (expectation.status != null) {
    expect(screen.getByTestId("session-list-status").textContent).toBe(expectation.status);
  }

  if (expectation.visibleLoading != null) {
    const loadingCue = screen.queryByTestId("session-list-loading");
    if (expectation.visibleLoading) {
      expect(loadingCue).not.toBeNull();
      // The cue must be a visible, sighted-user affordance, not a screen-reader
      // only region, and must carry the concise loading text.
      expect(loadingCue!.className).not.toContain("sr-only");
      expect(loadingCue!.textContent).toContain("loading page");
    } else {
      expect(loadingCue).toBeNull();
    }
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
    // The actionable body, not merely the heading: a panel that lost its
    // message would still carry the title. It must name the endpoint and the
    // cause the request reported.
    const panel = screen.getAllByRole("alert")[0];
    const text = (panel.textContent ?? "").replace(/\s+/g, " ");
    expect(text).toContain(TRANSCRIPT_LIST_ENDPOINT);
    expect(text).toContain("network unavailable");
    // This surface's retry names the exact page it re-issues; the home page's
    // panel says only "retry". The two share one component, so this is what
    // notices if the lift ever collapsed their labels into one.
    expect(
      screen.getByRole("button", { name: /^retry(ing)? page \d+$/ }),
    ).toBeInTheDocument();
  } else {
    expect(screen.queryByTestId("session-list-results")).toBeNull();
    expect(screen.queryByTestId("explore")).toBeNull();
    expect(screen.queryAllByRole("alert").length).toBe(0);
  }

  if (expectation.retryLabel !== undefined || expectation.retryBusy !== undefined) {
    const retry = screen.getByRole("button", { name: /^retry(ing)? page \d+$/ });
    if (expectation.retryLabel !== undefined) {
      expect((retry.textContent ?? "").trim(), `${location}: retry label`).toBe(
        expectation.retryLabel,
      );
    }
    if (expectation.retryBusy !== undefined) {
      expect(retry.getAttribute("aria-disabled"), `${location}: retry busy`).toBe(
        expectation.retryBusy ? "true" : null,
      );
      // Never a real `disabled`: it would hand focus back to the document and
      // not return it, so the reader who pressed would lose their place.
      expect(retry.hasAttribute("disabled"), `${location}: retry not truly disabled`).toBe(
        false,
      );
      if (expectation.retryBusy) {
        // Busy REFUSES the press rather than merely announcing it.
        const before = h.refetch.mock.calls.length;
        act(() => {
          fireEvent.click(retry);
        });
        expect(h.refetch.mock.calls.length, `${location}: busy press refused`).toBe(before);
      }
    }
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
