import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AppRouterContext } from "next/dist/shared/lib/app-router-context.shared-runtime";
import type { AppRouterInstance } from "next/dist/shared/lib/app-router-context.shared-runtime";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import ExplorePage from "@/app/explore/ExplorePage";
import type { TranscriptListItem, TranscriptListResponse } from "@/lib/types";
import {
  loadSessionListPaginationFixtures,
  type PaginationAction,
  type PaginationData,
  type PaginationExpect,
} from "@/test/sessionListPaginationFixtures";

/**
 * Mounted `/` session-pagination evidence.
 *
 * This test mounts the REAL Explore route (src/app/page.tsx), the REAL discovery
 * query hook (useTranscripts → TanStack Query → api → fetch), and the REAL
 * published Explore component from @peasant-labs/fairtrade/commons. The ONLY
 * thing controlled by the test is the HTTP schedule: fetch is replaced by a
 * per-request deferred so page responses settle in a chosen order (never a
 * timer/sleep). Every interaction goes through the actual accessible pager and
 * order controls, so the assertions describe what a keyboard/AT user observes.
 *
 * The permutations live entirely in src/testdata/session-list-pagination.yaml;
 * this file is the interpreter. Against a released Fairtrade whose Explore copies
 * the response page back into navigation intent, the intent-preservation and
 * request-bound assertions fail closed (the documented red-before-fix state);
 * against the corrected Explore they pass.
 */

const PAGE_SIZE = 24;
const TOTAL_ITEMS = 72; // 3 pages at 24/page, so the numbered pager is present.

const fixtures = loadSessionListPaginationFixtures();

type Deferred = {
  page: number;
  signal: AbortSignal | undefined;
  resolve: (value: unknown) => void;
  reject: (reason: unknown) => void;
};

function wireItem(id: string): TranscriptListItem {
  return {
    transcript: {
      id,
      owner_id: "owner-1",
      local_id: id,
      title: `Session ${id}`,
      description: null,
      visibility: "public",
      model_provider: "claude-code",
      model_name: "Claude",
      harness_version: "1.0",
      session_start: "2026-08-14T09:00:00Z",
      session_end: "2026-08-14T09:05:00Z",
      turn_count: 4,
      token_count: 100,
      tool_call_count: 1,
      duration_ms: 300000,
      git_branch: null,
      project_name: "commons-pagination",
      parent_session_id: null,
      title_generated: null,
      license_id: null,
    } as unknown as TranscriptListItem["transcript"],
    tags: [],
    owner: { github_username: "octocat", display_name: "Octo Cat", avatar_url: null } as unknown as TranscriptListItem["owner"],
  };
}

function buildResponse(data: PaginationData): TranscriptListResponse {
  return { transcripts: data.ids.map(wireItem), total: TOTAL_ITEMS, agent_total: 0, page: data.page, limit: PAGE_SIZE };
}

function makeRouter(): AppRouterInstance {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    refresh: vi.fn(),
    back: vi.fn(),
    forward: vi.fn(),
    prefetch: vi.fn(),
  } as unknown as AppRouterInstance;
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

class Schedule {
  readonly pending: Deferred[] = [];
  readonly transcriptCalls: Array<{ page: number; signal: AbortSignal | undefined }> = [];

  fetch = vi.fn((input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = String(input);
    if (url.includes("/tags/popular")) return Promise.resolve(jsonResponse([]));
    if (url.includes("/groups/search")) return Promise.resolve(jsonResponse({ collectives: [] }));
    if (url.includes("/transcripts")) {
      const match = url.match(/[?&]page=(\d+)/);
      const page = match ? Number(match[1]) : 0;
      const signal = init?.signal ?? undefined;
      this.transcriptCalls.push({ page, signal });
      return new Promise<Response>((resolve, reject) => {
        this.pending.push({
          page,
          signal,
          resolve: (value) => resolve(jsonResponse(value)),
          reject,
        });
      });
    }
    return Promise.resolve(jsonResponse({}));
  });

  takePending(page: number): Deferred {
    const index = this.pending.findIndex((deferred) => deferred.page === page);
    if (index < 0) {
      throw new Error(
        `mounted pagination schedule expected an in-flight request for page ${page}, but the only pending pages are ` +
          `[${this.pending.map((deferred) => deferred.page).join(", ")}]. The mounted route did not issue that request; ` +
          `check that the pager click emitted the intent and that the query key includes the page.`,
      );
    }
    return this.pending.splice(index, 1)[0];
  }

  requestedPages(): number[] {
    return this.transcriptCalls.map((call) => call.page);
  }

  abortedFor(page: number): boolean {
    return this.transcriptCalls.some((call) => call.page === page && call.signal?.aborted === true);
  }
}

async function flush(): Promise<void> {
  // Drain the fetch→json()→TanStack→React chain fully. Several macrotasks are
  // awaited (not one) so an erroneous repaint that hops through a deeper async
  // chain still lands before the following assertion — the drain is bounded and
  // deterministic, never a timing sleep used as the oracle.
  await act(async () => {
    for (let i = 0; i < 4; i += 1) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
  });
}

function visibleIds(): string[] {
  return [...document.querySelectorAll<HTMLAnchorElement>('a[href^="/transcripts/"]')].map((anchor) =>
    anchor.getAttribute("href")!.replace("/transcripts/", ""),
  );
}

/** The pagination landmark that owns the pager controls (never the top-nav). */
function pager(): HTMLElement {
  return screen.getByRole("navigation", { name: "pagination" });
}

function currentPageMarker(): number | null {
  // Scope to the pagination landmark: a full mounted layout also marks the active
  // route with aria-current="page" on the top-nav, so a document-wide selector
  // could read the nav rather than the pager.
  const marker = pager().querySelector('[aria-current="page"]');
  return marker ? Number(marker.textContent) : null;
}

/**
 * The visible (non-sr-only) transient loading cue text, or null when the cue is
 * absent. This is the sighted-user counterpart to the polite announcer; it is a
 * distinct element from the sr-only status region.
 */
function visibleLoadingText(): string | null {
  const cue = screen.queryByTestId("session-list-loading");
  if (cue == null) return null;
  if (cue.classList.contains("sr-only")) {
    throw new Error(
      `visible loading cue "session-list-loading" is sr-only; sighted users would see no loading text. ` +
        `The cue must be a visible strip (icon + text), separate from the sr-only polite status region.`,
    );
  }
  return cue.textContent ?? "";
}

function statusText(): string {
  return screen.getByTestId("session-list-status").textContent ?? "";
}

function pagerButton(name: string): HTMLElement {
  return screen.getByRole("button", { name });
}

async function performAction(user: ReturnType<typeof userEvent.setup>, schedule: Schedule, action: PaginationAction): Promise<void> {
  switch (action.kind) {
    case "resolve": {
      const deferred = schedule.takePending(action.requestPage);
      deferred.resolve(buildResponse(fixtures.data[action.dataId]));
      break;
    }
    case "resolveIgnoringAbort": {
      const deferred = schedule.takePending(action.requestPage);
      // The scenario only means something if this request was already superseded
      // and cancelled: the corrected route must ignore its late arrival.
      expect(deferred.signal?.aborted, `page ${action.requestPage} must have been aborted before its late response`).toBe(true);
      deferred.resolve(buildResponse(fixtures.data[action.dataId]));
      break;
    }
    case "reject": {
      const deferred = schedule.takePending(action.requestPage);
      deferred.reject(new Error("network unavailable"));
      break;
    }
    case "clickPage":
      await user.click(pagerButton(`page ${action.page}`));
      break;
    case "clickNext":
      await user.click(pagerButton("next page"));
      break;
    case "clickPrev":
      await user.click(pagerButton("previous page"));
      break;
    case "clickRetry":
      await user.click(screen.getByRole("button", { name: /retry/i }));
      break;
    case "observe":
      // The interpreter's normal bounded drain runs before and after this
      // no-interaction observation, proving pending state stays clean after
      // additional queued work has had another chance to commit.
      break;
    case "changeOrder":
      await user.click(screen.getByRole("radio", { name: action.order }));
      break;
  }
}

function assertExpectations(schedule: Schedule, expectation: PaginationExpect, location: string): void {
  if (expectation.currentPage !== undefined) {
    expect(currentPageMarker(), `${location}: current page marker`).toBe(expectation.currentPage);
  }
  if (expectation.visibleIds !== undefined) {
    // Ordered comparison: the rows render in the response/fixture order, so an
    // intra-page row-order regression is caught, not sorted away.
    expect(visibleIds(), `${location}: visible row ids (in order)`).toEqual(expectation.visibleIds);
  }
  if (expectation.uniqueIds) {
    const ids = visibleIds();
    expect(new Set(ids).size, `${location}: visible ids must be unique`).toBe(ids.length);
  }
  if (expectation.ariaBusy !== undefined) {
    const results = screen.getByTestId("session-list-results");
    expect(results.getAttribute("aria-busy"), `${location}: results aria-busy`).toBe(String(expectation.ariaBusy));
  }
  if (expectation.status !== undefined) {
    expect(statusText(), `${location}: polite status text`).toBe(expectation.status);
  }
  if (expectation.statusIncludes !== undefined) {
    expect(statusText(), `${location}: polite status includes`).toContain(expectation.statusIncludes);
  }
  if (expectation.alert !== undefined) {
    expect(screen.queryAllByRole("alert").length > 0, `${location}: alert presence`).toBe(expectation.alert);
  }
  if (expectation.alertIncludes !== undefined) {
    const alert = screen.getByRole("alert");
    for (const fragment of expectation.alertIncludes) {
      expect(alert.textContent, `${location}: actionable alert includes ${JSON.stringify(fragment)}`).toContain(fragment);
    }
  }
  if (expectation.requestedPages !== undefined) {
    expect(schedule.requestedPages(), `${location}: cumulative requested pages (request bound)`).toEqual(expectation.requestedPages);
  }
  if (expectation.abortedPages !== undefined) {
    for (const page of expectation.abortedPages) {
      expect(schedule.abortedFor(page), `${location}: superseded page ${page} must receive an abort`).toBe(true);
    }
  }
  if (expectation.focusPage !== undefined) {
    expect(
      (document.activeElement as HTMLElement | null)?.getAttribute("aria-label"),
      `${location}: focus remains on the activated pager control`,
    ).toBe(`page ${expectation.focusPage}`);
  }
  if (expectation.visibleLoading !== undefined) {
    const cue = visibleLoadingText();
    if (expectation.visibleLoading === false) {
      expect(cue, `${location}: visible loading cue must be absent when not loading`).toBeNull();
    } else {
      expect(cue, `${location}: visible loading cue must be present while a page loads`).not.toBeNull();
      expect(cue, `${location}: visible loading cue text`).toContain(expectation.visibleLoading);
    }
  }
  if (expectation.singleCurrent) {
    expect(pager().querySelectorAll('[aria-current="page"]').length, `${location}: exactly one current page`).toBe(1);
  }
  if (expectation.prevDisabled !== undefined) {
    expect((pagerButton("previous page") as HTMLButtonElement).disabled, `${location}: previous-page disabled`).toBe(
      expectation.prevDisabled,
    );
  }
  if (expectation.nextDisabled !== undefined) {
    expect((pagerButton("next page") as HTMLButtonElement).disabled, `${location}: next-page disabled`).toBe(
      expectation.nextDisabled,
    );
  }
}

function wrap(children: ReactNode): ReactNode {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return createElement(
    QueryClientProvider,
    { client },
    createElement(AppRouterContext.Provider, { value: makeRouter() }, children),
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("mounted / session pagination race and accessibility evidence", () => {
  for (const scenario of fixtures.scenarios) {
    it(scenario.name, async () => {
      const schedule = new Schedule();
      vi.stubGlobal("fetch", schedule.fetch);
      const user = userEvent.setup();

      await act(async () => {
        render(wrap(createElement(ExplorePage)));
      });
      await flush();

      for (const [index, step] of scenario.steps.entries()) {
        const location = `${scenario.name} > ${step.name} [${index}]`;
        await performAction(user, schedule, step.action);
        await flush();
        // Poll the observable state until it fully reflects the step. The whole
        // expectation bundle is asserted together, so the mounted route must
        // converge on every observable at once (never a partially-applied frame).
        // A stale-page repaint or an extra oscillating request cannot converge and
        // fails closed here rather than being masked by a fixed sleep.
        await waitFor(() => assertExpectations(schedule, step.expect, location));
        // Harden steady-state / negative steps (e.g. an abort-ignoring late
        // response that must NOT repaint): once converged, drain again and
        // re-assert. A transient erroneous repaint arriving after the first
        // convergence would flip an observable here and fail closed, instead of
        // slipping through because waitFor passed on its first evaluation.
        await flush();
        assertExpectations(schedule, step.expect, `${location} (post-settle)`);
      }
    });
  }
});
