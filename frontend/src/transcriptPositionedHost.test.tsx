import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { Suspense } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { parse } from "yaml";
import { buildPositionedBodies } from "../scripts/journey/lib/positioned-bodies.mjs";
import {
  installMountedRouteTeardown,
  installRESTFixture,
  renderProductionRoute,
  type MountedRouteHandler,
} from "@/test/mountedProductionRoute";
import { readTranscriptForDownload } from "@/lib/transcriptExport";
import TranscriptPreview from "@/components/contribute/TranscriptPreview";
import { TranscriptHeaderActionsLabel, TranscriptHeaderActionsTestId } from "@/components/transcript/TranscriptHeaderActions";
import { SessionDetailV2TestId } from "@/components/session-detail/v2/SessionDetailV2";
import { TranscriptViewMode } from "@/components/session-detail/v2/transcriptViewMode";

// The transcript page over the positioned reader. The real route mounts
// (TranscriptDetailPage -> SessionDetailV2 -> fairtrade's reader and viewer);
// only the REST API is mocked, and every positioned body comes from the one
// shared builder, so it is a body Schema's parsers accept.

type Spec = Parameters<typeof buildPositionedBodies>[0] & { title: string };
type NoFallbackCase = {
  name: string; status?: number; retryAfter?: string; network?: boolean; malformed?: boolean;
  kind: string; retry?: string;
};
type LandingCase = {
  name: string; why: string; search: string; expectWindowAnchor: string; expectSegments: string[];
  expectText?: string; expectSnapped?: string;
  /** main-partition positions this case's server sends no display number for */
  numberAbsent?: number[];
  /** the number label the opened (active) row itself shows */
  expectOpenedLabel?: string;
};
type DownloadRow = { name: string; label: string; file: string; type: string };
/** What a graph case ends on: the drawn graph, the list alone, or the graph's failure state. */
const GraphOutcome = Object.freeze({ Graph: "graph", List: "list", Failure: "failure" } as const);
type GraphCase = {
  name: string; views: TranscriptViewMode[]; contentStatus: number; expectContentReads: number;
  expect: (typeof GraphOutcome)[keyof typeof GraphOutcome];
};
type Fixture = {
  transcript: Spec;
  retryActions: string[];
  noFallback: NoFallbackCase[];
  landing: LandingCase[];
  downloads: { search: string; loadedSegments: string[]; rows: DownloadRow[] };
  viewButtons: Record<TranscriptViewMode, string>;
  graphFailureTitle: string;
  graph: GraphCase[];
};

// unstring-allow: data-path-literal — named once at module scope and read only through this constant
const TESTDATA = resolve(process.cwd(), "src/testdata");
const fixture = parse(readFileSync(resolve(TESTDATA, "transcript-positioned-host.yaml"), "utf8")) as Fixture;
const spec = fixture.transcript;
const [opening, , , middle, last] = spec.turns.map((turn) => turn.content);
const id = spec.transcriptId;

const requiredFixtureNames = [
  "v2-failure-409-stale", "v2-failure-503-busy", "v2-failure-410-unavailable", "v2-failure-404-denied",
  "v2-failure-network", "v2-failure-malformed",
  "turn-param-lands-requested-position", "turn-param-snapped-status-copy", "turn-param-snapped-unnumbered-row",
  "download-json-bytes-partial-load", "download-jsonl-bytes-partial-load", "download-markdown-bytes-partial-load",
  "graph-open-reads-content-once", "list-mode-never-reads-content", "graph-content-failure-keeps-list",
];

it("retains every named positioned-host case", () => {
  const names = [...fixture.noFallback, ...fixture.landing, ...fixture.downloads.rows, ...fixture.graph].map(({ name }) => name);
  expect(names.sort()).toEqual([...requiredFixtureNames].sort());
});

installMountedRouteTeardown();
afterEach(() => vi.restoreAllMocks());

const json = (body: unknown, init: ResponseInit = {}) =>
  new Response(JSON.stringify(body), { status: 200, ...init, headers: { "content-type": "application/json", ...init.headers } });

const ADVERTISED = { segmentedReadVersion: "0.2.0", minSegmentedReadVersion: "0.1.0" };
const POSITIONED_ROUTE = /\/transcripts\/[^/]+\/(header|segments\/locate|segments\/\d+|segments|window)$/;

type Bodies = ReturnType<typeof buildPositionedBodies>;
type Failure = { status?: number; retryAfter?: string; network?: boolean; malformed?: boolean };

/** The advertisement, and the five positioned routes answered from `bodies`, or failed the given
    way. `segmentFailure` fails only segment reads, after the rest answer. */
function positionedRoutes(
  bodies: Bodies,
  options: { failure?: Failure; segmentFailure?: Failure; annotations?: unknown[]; contentStatus?: number } = {},
): MountedRouteHandler {
  const fail = (failure: Failure) => {
    if (failure.network) throw new TypeError("Failed to fetch");
    if (failure.malformed) return json({ unexpected: true });
    return json({ error: "refused" }, { status: failure.status, headers: failure.retryAfter ? { "Retry-After": failure.retryAfter } : {} });
  };
  return (raw, init) => {
    const url = new URL(raw);
    if (url.pathname.endsWith("/schema/version")) return json(ADVERTISED);
    if (options.contentStatus !== undefined && options.contentStatus !== 200 && url.pathname.endsWith(`/transcripts/${id}/content`)) {
      return json({ error: "refused" }, { status: options.contentStatus });
    }
    if (options.annotations && url.pathname.endsWith(`/transcripts/${id}/annotations`)) {
      return json({ annotations: options.annotations });
    }
    const match = url.pathname.match(POSITIONED_ROUTE);
    if (!match) return undefined;
    if (options.failure) return fail(options.failure);
    const answers: Record<string, () => Response> = {
      header: () => json(bodies.header),
      window: () => {
        const anchor = url.searchParams.get("anchor");
        return json(bodies.window(anchor === null ? undefined : Number(anchor)));
      },
      segments: () => json(bodies.directory(Number(url.searchParams.get("page") ?? "1"))),
      "segments/locate": () => {
        const found = bodies.locate(JSON.parse(String(init?.body)).reference);
        return found ? json(found) : json({ error: "no match" }, { status: 404 });
      },
    };
    const route = match[1];
    const answer = answers[route];
    if (answer) return answer();
    if (options.segmentFailure) return fail(options.segmentFailure);
    return json(bodies.segment(Number(route.split("/")[1])));
  };
}

function metadata(contentHash = "hash-current") {
  return {
    transcript: {
      id, local_id: "local-positioned", model_provider: "claude-code" as const, visibility: "public",
      title: spec.title, description: null, project_name: "positioned-reader", content_hash: contentHash,
    },
    owner: { id: "owner-1" },
    enriched_shares: [],
  };
}

async function mountPositioned(
  bodies: Bodies,
  search = "",
  options: Parameters<typeof positionedRoutes>[1] & { contentHash?: string } = {},
) {
  const fetchMock = installRESTFixture(id, metadata(options.contentHash), bodies.content, "positioned host", undefined, positionedRoutes(bodies, options));
  await renderProductionRoute(id, search);
  await waitFor(() => expect(screen.getByTestId(SessionDetailV2TestId.ModePositioned.wire)).toBeInTheDocument());
  return fetchMock;
}

const urls = (fetchMock: ReturnType<typeof vi.fn>) => fetchMock.mock.calls.map(([input]) => new URL(String(input)));
const contentReads = (fetchMock: ReturnType<typeof vi.fn>) => urls(fetchMock).filter((url) => url.pathname.endsWith("/content"));
const segmentReads = (fetchMock: ReturnType<typeof vi.fn>) =>
  urls(fetchMock).map((url) => url.pathname.match(/\/segments\/(\d+)$/)?.[1]).filter((value): value is string => value !== undefined);

async function openMore(): Promise<HTMLElement> {
  fireEvent.click(screen.getByRole("button", { name: TranscriptHeaderActionsLabel.More.wire }));
  return screen.findByRole("menu");
}

/** Records every file handed to the browser, without navigating jsdom. */
function captureDownloads(): Blob[] {
  const saved: Blob[] = [];
  vi.spyOn(URL, "createObjectURL").mockImplementation((blob) => {
    saved.push(blob as Blob);
    return "blob:transcript";
  });
  vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
  vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  return saved;
}

describe("v2-failure-never-requests-content", () => {
  for (const row of fixture.noFallback) {
    it(row.name, async () => {
      const fetchMock = await mountPositioned(buildPositionedBodies(spec), "", { failure: row });
      await waitFor(() => expect(document.querySelector(`[data-read-failure="${row.kind}"]`)).not.toBeNull());
      if (row.retry) {
        fireEvent.click(screen.getByRole("button", { name: row.retry }));
        await waitFor(() => expect(document.querySelector(`[data-read-failure="${row.kind}"]`)).not.toBeNull());
      } else {
        for (const action of fixture.retryActions) expect(screen.queryByRole("button", { name: action })).toBeNull();
      }
      // The downloads stay with the failure state.
      expect(screen.getByTestId(TranscriptHeaderActionsTestId.Root.wire)).toBeInTheDocument();
      expect(contentReads(fetchMock)).toEqual([]);
    });
  }
});

it("capability-absent-mounts-content", async () => {
  const bodies = buildPositionedBodies(spec);
  const fetchMock = installRESTFixture(id, metadata(), bodies.content, "positioned host", undefined, (raw) =>
    new URL(raw).pathname.endsWith("/schema/version") ? json({ segmentedReadVersion: "0.1.0", minSegmentedReadVersion: "0.1.0" }) : undefined,
  );
  await renderProductionRoute(id);
  await waitFor(() => expect(screen.getByTestId(SessionDetailV2TestId.ModeContent.wire)).toBeInTheDocument());
  await waitFor(() => expect(contentReads(fetchMock)).toHaveLength(1));
  expect(urls(fetchMock).some((url) => POSITIONED_ROUTE.test(url.pathname))).toBe(false);
});

it("directory-context-mismatch-discarded", async () => {
  // A later directory page that names another revision is discarded: the
  // reader shows its malformed notice and keeps what it loaded.
  const bodies = buildPositionedBodies({ ...spec, directoryPerPage: 2 }, {
    mutateBody: (name, body) =>
      name === "directory page 2" ? { ...body, context: { ...(body as { context: object }).context, revision: "4" } } : body,
  });
  const fetchMock = await mountPositioned(bodies);
  await waitFor(() => expect(document.querySelector('[data-read-failure="malformed"]')).not.toBeNull());
  expect(urls(fetchMock).some((url) => url.pathname.endsWith("/segments") && url.searchParams.get("page") === "2")).toBe(true);
  await waitFor(() => expect(screen.getAllByText(opening).length).toBeGreaterThan(0));
  expect(contentReads(fetchMock)).toEqual([]);
});

describe("landing", () => {
  for (const row of fixture.landing) {
    it(row.name, async () => {
      const fetchMock = await mountPositioned(buildPositionedBodies({ ...spec, numberAbsent: row.numberAbsent ?? spec.numberAbsent }), row.search);
      await waitFor(() => expect(new Set(segmentReads(fetchMock))).toEqual(new Set(row.expectSegments)));
      const anchors = urls(fetchMock).filter((url) => url.pathname.endsWith("/window")).map((url) => url.searchParams.get("anchor"));
      expect(anchors).toContain(row.expectWindowAnchor);
      if (row.expectText) await waitFor(() => expect(screen.getAllByText(row.expectText!).length).toBeGreaterThan(0));
      if (row.expectSnapped) {
        await waitFor(() => expect(screen.getByTestId(SessionDetailV2TestId.SnappedTurn.wire)).toHaveTextContent(row.expectSnapped!));
        // The line names the row the reader sees by that row's own label.
        if (row.expectOpenedLabel) {
          await waitFor(() => expect(document.querySelector(".txn-turn.txn-active .txn-turnnum")).toHaveTextContent(row.expectOpenedLabel!));
        }
      } else {
        expect(screen.queryByTestId(SessionDetailV2TestId.SnappedTurn.wire)).toBeNull();
      }
      expect(contentReads(fetchMock)).toEqual([]);
    });
  }
});

const carried = (entry: string) => `?entry=${entry}&entryKind=before_redacted_entry&entryRevision=hash-current`;

it("carried-anchor-uses-locator", async () => {
  const fetchMock = await mountPositioned(buildPositionedBodies(spec), carried("entry-middle"));
  await waitFor(() => expect(segmentReads(fetchMock)).toContain("1"));
  const locate = fetchMock.mock.calls.find(([input]) => String(input).includes("/segments/locate"));
  expect(locate?.[1]?.method).toBe("POST");
  expect(JSON.parse(String(locate?.[1]?.body))).toMatchObject({ reference: "entry-middle" });
  await waitFor(() => expect(screen.getAllByText(middle).length).toBeGreaterThan(0));
});

it("locator-no-match-keeps-general-link", async () => {
  const fetchMock = await mountPositioned(buildPositionedBodies(spec), carried("entry-missing"));
  await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/segments/locate"))).toBe(true));
  await waitFor(() => expect(screen.getAllByText(opening).length).toBeGreaterThan(0));
  expect(document.querySelector("[data-read-failure]")).toBeNull();
  expect(segmentReads(fetchMock)).toEqual(["0"]);
});

it("saved-labels-keyed-by-recorded-index", async () => {
  // The second row's recorded index is 11; its position is 1. Only the label
  // saved against the recorded index belongs to it.
  const byRecordedIndex = { id: "a-1", typeId: "turn_flag", typeName: "turn_flag", value: "highlight", targetEntryIndex: 11 };
  const byPosition = { id: "a-2", typeId: "turn_flag", typeName: "turn_flag", value: "retry", targetEntryIndex: 1 };
  await mountPositioned(buildPositionedBodies(spec), "", { annotations: [byRecordedIndex, byPosition] });
  await waitFor(() => expect(screen.getAllByText(byRecordedIndex.value).length).toBeGreaterThan(0));
  expect(screen.queryByText(byPosition.value)).toBeNull();
});

describe("downloads", () => {
  for (const row of fixture.downloads.rows) {
    it(row.name, async () => {
      const saved = captureDownloads();
      const fetchMock = await mountPositioned(buildPositionedBodies(spec), fixture.downloads.search);
      await waitFor(() => expect(new Set(segmentReads(fetchMock))).toEqual(new Set(fixture.downloads.loadedSegments)));
      await waitFor(() => expect(screen.getAllByText(last).length).toBeGreaterThan(0));
      fireEvent.click(within(await openMore()).getByRole("menuitem", { name: row.label }));
      await waitFor(() => expect(saved).toHaveLength(1));
      expect(contentReads(fetchMock)).toHaveLength(1);
      expect(saved[0].type).toMatch(new RegExp(`^${row.type}`));
      expect(await saved[0].text()).toBe(readFileSync(resolve(TESTDATA, row.file), "utf8"));
    });
  }

  it("download-after-v2-page-mount-has-source", async () => {
    const saved = captureDownloads();
    const fetchMock = await mountPositioned(buildPositionedBodies(spec));
    await waitFor(() => expect(screen.getAllByText(opening).length).toBeGreaterThan(0));
    expect(contentReads(fetchMock)).toEqual([]);
    fireEvent.click(within(await openMore()).getByRole("menuitem", { name: TranscriptHeaderActionsLabel.DownloadJSON.wire }));
    await waitFor(() => expect(saved).toHaveLength(1));
    // One authorized read of the whole stored copy, with the viewer's headers.
    const reads = fetchMock.mock.calls.filter(([input]) => String(input).endsWith(`/transcripts/${id}/content`));
    expect(reads).toHaveLength(1);
    expect(reads[0][1]?.cache).toBe("no-store");
  });

  it("download-menu-survives-segment-failure", async () => {
    const saved = captureDownloads();
    const fetchMock = await mountPositioned(buildPositionedBodies(spec), "", { segmentFailure: { status: 409 } });
    await waitFor(() => expect(document.querySelector('[data-read-failure="stale"]')).not.toBeNull());
    expect(contentReads(fetchMock)).toEqual([]);
    fireEvent.click(within(await openMore()).getByRole("menuitem", { name: TranscriptHeaderActionsLabel.DownloadMarkdown.wire }));
    await waitFor(() => expect(saved).toHaveLength(1));
    expect(contentReads(fetchMock)).toHaveLength(1);
  });

  it("download-no-partial-file-on-epoch-change", async () => {
    const saved = captureDownloads();
    let current = true;
    vi.stubGlobal("fetch", vi.fn(async () => {
      // The page moves to another revision while the download is in flight.
      current = false;
      return json(buildPositionedBodies(spec).content);
    }));
    const result = await readTranscriptForDownload(
      { transcriptId: id, format: "json", url: `https://village.test/transcripts/${id}`, storedTitle: spec.title },
      { signal: new AbortController().signal, isCurrent: () => current },
    );
    expect(result).toBe(false);
    expect(saved).toEqual([]);
  });

  it("download-revision-change-mounted-aborts", async () => {
    // The mounted page: a download is in flight when the reader reloads onto
    // another revision. The page aborts the read and saves no file, even when
    // the stored copy answers afterwards.
    const saved = captureDownloads();
    const before = buildPositionedBodies(spec);
    const after = buildPositionedBodies({ ...spec, revision: "4" });
    let reloaded = false;
    let contentSignal: AbortSignal | undefined;
    let answerContent: (() => void) | undefined;
    const routes: MountedRouteHandler = (raw, init) => {
      if (new URL(raw).pathname.endsWith(`/transcripts/${id}/content`)) {
        contentSignal = init?.signal ?? undefined;
        return new Promise<Response>((resolveContent, reject) => {
          answerContent = () => resolveContent(json(before.content));
          contentSignal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
        });
      }
      // Revision 3's segments answer stale until the reader reloads onto revision 4.
      return reloaded ? positionedRoutes(after)(raw, init) : positionedRoutes(before, { segmentFailure: { status: 409 } })(raw, init);
    };
    installRESTFixture(id, metadata(), before.content, "positioned host", undefined, routes);
    await renderProductionRoute(id);
    await waitFor(() => expect(document.querySelector('[data-read-failure="stale"]')).not.toBeNull());
    fireEvent.click(within(await openMore()).getByRole("menuitem", { name: TranscriptHeaderActionsLabel.DownloadJSON.wire }));
    await waitFor(() => expect(contentSignal).toBeDefined());
    reloaded = true;
    fireEvent.click(screen.getByRole("button", { name: "reload transcript" }));
    await waitFor(() => expect(screen.getAllByText(opening).length).toBeGreaterThan(0));
    expect(document.querySelector("[data-read-failure]")).toBeNull();
    await waitFor(() => expect(contentSignal?.aborted).toBe(true));
    // An answer that arrives anyway is not saved.
    answerContent?.();
    await act(async () => { await Promise.resolve(); });
    expect(saved).toEqual([]);
  });

  it("download-abort-saves-nothing", async () => {
    const saved = captureDownloads();
    const controller = new AbortController();
    vi.stubGlobal("fetch", vi.fn((_input: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
    })));
    const pending = readTranscriptForDownload(
      { transcriptId: id, format: "markdown", url: `https://village.test/transcripts/${id}`, storedTitle: spec.title },
      { signal: controller.signal, isCurrent: () => true },
    );
    controller.abort();
    expect(await pending).toBe(false);
    expect(saved).toEqual([]);
  });
});

it("preview-read-only-no-downloads", async () => {
  // The contribute and review pages' preview column reads the same way, with
  // no header actions and so no downloads, and never the page's own query.
  const bodies = buildPositionedBodies(spec);
  const fetchMock = installRESTFixture(id, metadata(), bodies.content, "positioned preview", undefined, positionedRoutes(bodies));
  window.history.replaceState({}, "", "/collectives/c-1/review?turn=14");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    render(
      <QueryClientProvider client={client}>
        <Suspense fallback={null}>
          <TranscriptPreview transcriptId={id} />
        </Suspense>
      </QueryClientProvider>,
    );
  });
  await waitFor(() => expect(screen.getByTestId(SessionDetailV2TestId.ModePositioned.wire)).toBeInTheDocument());
  await waitFor(() => expect(screen.getAllByText(opening).length).toBeGreaterThan(0));
  expect(screen.queryByTestId(TranscriptHeaderActionsTestId.Root.wire)).toBeNull();
  expect(screen.queryByRole("button", { name: TranscriptHeaderActionsLabel.More.wire })).toBeNull();
  expect(urls(fetchMock).filter((url) => url.pathname.endsWith("/window")).map((url) => url.searchParams.get("anchor"))).toEqual([null]);
  expect(contentReads(fetchMock)).toEqual([]);
});

it("mock-invalid-body-refuses-start", () => {
  // The shared builder refuses a body Schema's parser refuses, so no mock
  // built on it can start.
  expect(() => buildPositionedBodies(spec, {
    mutateBody: (name, body) => (name === "segment 1" ? { ...body, rows: [] } : body),
  })).toThrow(/positioned mock refused to start: its segment 1 body/);
});

describe("graph", () => {
  // The graph engine measures nodes with ResizeObserver, which jsdom lacks.
  const resizeObserver = class { observe() {} unobserve() {} disconnect() {} };

  for (const row of fixture.graph) {
    it(row.name, async () => {
      const fetchMock = await mountPositioned(buildPositionedBodies(spec), "", { contentStatus: row.contentStatus });
      vi.stubGlobal("ResizeObserver", resizeObserver);
      await waitFor(() => expect(screen.getAllByText(opening).length).toBeGreaterThan(0));
      for (const view of row.views) {
        fireEvent.click(screen.getByRole("button", { name: fixture.viewButtons[view] }));
        if (view === TranscriptViewMode.Graph && row.expect === GraphOutcome.Graph) {
          await waitFor(() => expect(document.querySelector(".react-flow")).not.toBeNull());
        }
      }
      if (row.expect === GraphOutcome.Failure) {
        await waitFor(() => expect(screen.getByText(fixture.graphFailureTitle)).toBeInTheDocument());
        // The list beside it stays positioned and keeps its rows.
        fireEvent.click(screen.getByRole("button", { name: fixture.viewButtons.list }));
        await waitFor(() => expect(screen.getAllByText(opening).length).toBeGreaterThan(0));
        expect(screen.getByTestId(SessionDetailV2TestId.ModePositioned.wire)).toBeInTheDocument();
      }
      if (row.expect === GraphOutcome.List) {
        await waitFor(() => expect(segmentReads(fetchMock).length).toBeGreaterThan(0));
        expect(document.querySelector(".react-flow")).toBeNull();
      }
      await waitFor(() => expect(contentReads(fetchMock)).toHaveLength(row.expectContentReads));
      // Nothing more is read after the reads settle.
      await new Promise((resolve) => setTimeout(resolve, 50));
      expect(contentReads(fetchMock)).toHaveLength(row.expectContentReads);
    });
  }
});
