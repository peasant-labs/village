import { readFileSync } from "node:fs";
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
};
type DownloadRow = { name: string; label: string; file: string; type: string };
type Fixture = {
  transcript: Spec;
  noFallback: NoFallbackCase[];
  landing: LandingCase[];
  downloads: { search: string; loadedSegments: string[]; rows: DownloadRow[] };
};

const fixture = parse(readFileSync("src/testdata/transcript-positioned-host.yaml", "utf8")) as Fixture;
const spec = fixture.transcript;
const id = spec.transcriptId;

const requiredFixtureNames = [
  "v2-failure-409-stale", "v2-failure-503-busy", "v2-failure-410-unavailable", "v2-failure-404-denied",
  "v2-failure-network", "v2-failure-malformed",
  "turn-param-lands-requested-position", "turn-param-snapped-status-copy",
  "download-json-bytes-partial-load", "download-jsonl-bytes-partial-load", "download-markdown-bytes-partial-load",
];

it("retains every named positioned-host case", () => {
  const names = [...fixture.noFallback, ...fixture.landing, ...fixture.downloads.rows].map(({ name }) => name);
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
  options: { failure?: Failure; segmentFailure?: Failure; annotations?: unknown[] } = {},
): MountedRouteHandler {
  const fail = (failure: Failure) => {
    if (failure.network) throw new TypeError("Failed to fetch");
    if (failure.malformed) return json({ unexpected: true });
    return json({ error: "refused" }, { status: failure.status, headers: failure.retryAfter ? { "Retry-After": failure.retryAfter } : {} });
  };
  return (raw, init) => {
    const url = new URL(raw);
    if (url.pathname.endsWith("/schema/version")) return json(ADVERTISED);
    if (options.annotations && url.pathname.endsWith(`/transcripts/${id}/annotations`)) {
      return json({ annotations: options.annotations });
    }
    const match = url.pathname.match(POSITIONED_ROUTE);
    if (!match) return undefined;
    if (options.failure) return fail(options.failure);
    const route = match[1];
    if (route === "header") return json(bodies.header);
    if (route === "window") {
      const anchor = url.searchParams.get("anchor");
      return json(bodies.window(anchor === null ? undefined : Number(anchor)));
    }
    if (route === "segments") return json(bodies.directory(Number(url.searchParams.get("page") ?? "1")));
    if (route === "segments/locate") {
      const match = bodies.locate(JSON.parse(String(init?.body)).reference);
      return match ? json(match) : json({ error: "no match" }, { status: 404 });
    }
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
  await waitFor(() => expect(screen.getByTestId("transcript-mode-positioned")).toBeInTheDocument());
  return fetchMock;
}

const urls = (fetchMock: ReturnType<typeof vi.fn>) => fetchMock.mock.calls.map(([input]) => new URL(String(input)));
const contentReads = (fetchMock: ReturnType<typeof vi.fn>) => urls(fetchMock).filter((url) => url.pathname.endsWith("/content"));
const segmentReads = (fetchMock: ReturnType<typeof vi.fn>) =>
  urls(fetchMock).map((url) => url.pathname.match(/\/segments\/(\d+)$/)?.[1]).filter((value): value is string => value !== undefined);

async function openMore(): Promise<HTMLElement> {
  fireEvent.click(screen.getByRole("button", { name: "more" }));
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
        expect(screen.queryByRole("button", { name: /try again|reload transcript|try now/ })).toBeNull();
      }
      // The downloads stay with the failure state.
      expect(screen.getByTestId("transcript-header-actions")).toBeInTheDocument();
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
  await waitFor(() => expect(screen.getByTestId("transcript-mode-content")).toBeInTheDocument());
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
  await waitFor(() => expect(screen.getAllByText("Open the reader at the deep link.").length).toBeGreaterThan(0));
  expect(contentReads(fetchMock)).toEqual([]);
});

describe("landing", () => {
  for (const row of fixture.landing) {
    it(row.name, async () => {
      const fetchMock = await mountPositioned(buildPositionedBodies(spec), row.search);
      await waitFor(() => expect(new Set(segmentReads(fetchMock))).toEqual(new Set(row.expectSegments)));
      const anchors = urls(fetchMock).filter((url) => url.pathname.endsWith("/window")).map((url) => url.searchParams.get("anchor"));
      expect(anchors).toContain(row.expectWindowAnchor);
      if (row.expectText) await waitFor(() => expect(screen.getAllByText(row.expectText!).length).toBeGreaterThan(0));
      if (row.expectSnapped) {
        await waitFor(() => expect(screen.getByTestId("transcript-snapped-turn")).toHaveTextContent(row.expectSnapped!));
      } else {
        expect(screen.queryByTestId("transcript-snapped-turn")).toBeNull();
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
  await waitFor(() => expect(screen.getAllByText("The second segment holds the middle rows.").length).toBeGreaterThan(0));
});

it("locator-no-match-keeps-general-link", async () => {
  const fetchMock = await mountPositioned(buildPositionedBodies(spec), carried("entry-missing"));
  await waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/segments/locate"))).toBe(true));
  await waitFor(() => expect(screen.getAllByText("Open the reader at the deep link.").length).toBeGreaterThan(0));
  expect(document.querySelector("[data-read-failure]")).toBeNull();
  expect(segmentReads(fetchMock)).toEqual(["0"]);
});

it("saved-labels-keyed-by-recorded-index", async () => {
  // The second row's recorded index is 11; its position is 1. Only the label
  // saved against the recorded index belongs to it.
  await mountPositioned(buildPositionedBodies(spec), "", {
    annotations: [
      { id: "a-1", typeId: "turn_flag", typeName: "turn_flag", value: "highlight", targetEntryIndex: 11 },
      { id: "a-2", typeId: "turn_flag", typeName: "turn_flag", value: "retry", targetEntryIndex: 1 },
    ],
  });
  await waitFor(() => expect(screen.getAllByText("highlight").length).toBeGreaterThan(0));
  expect(screen.queryByText("retry")).toBeNull();
});

describe("downloads", () => {
  for (const row of fixture.downloads.rows) {
    it(row.name, async () => {
      const saved = captureDownloads();
      const fetchMock = await mountPositioned(buildPositionedBodies(spec), fixture.downloads.search);
      await waitFor(() => expect(new Set(segmentReads(fetchMock))).toEqual(new Set(fixture.downloads.loadedSegments)));
      await waitFor(() => expect(screen.getAllByText("Now read the last segment.").length).toBeGreaterThan(0));
      fireEvent.click(within(await openMore()).getByRole("menuitem", { name: row.label }));
      await waitFor(() => expect(saved).toHaveLength(1));
      expect(contentReads(fetchMock)).toHaveLength(1);
      expect(saved[0].type).toMatch(new RegExp(`^${row.type}`));
      expect(await saved[0].text()).toBe(readFileSync(`src/testdata/${row.file}`, "utf8"));
    });
  }

  it("download-after-v2-page-mount-has-source", async () => {
    const saved = captureDownloads();
    const fetchMock = await mountPositioned(buildPositionedBodies(spec));
    await waitFor(() => expect(screen.getAllByText("Open the reader at the deep link.").length).toBeGreaterThan(0));
    expect(contentReads(fetchMock)).toEqual([]);
    fireEvent.click(within(await openMore()).getByRole("menuitem", { name: "download json" }));
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
    fireEvent.click(within(await openMore()).getByRole("menuitem", { name: "download markdown" }));
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
  await waitFor(() => expect(screen.getByTestId("transcript-mode-positioned")).toBeInTheDocument());
  await waitFor(() => expect(screen.getAllByText("Open the reader at the deep link.").length).toBeGreaterThan(0));
  expect(screen.queryByTestId("transcript-header-actions")).toBeNull();
  expect(screen.queryByRole("button", { name: "more" })).toBeNull();
  expect(urls(fetchMock).filter((url) => url.pathname.endsWith("/window")).map((url) => url.searchParams.get("anchor"))).toEqual([null]);
  expect(contentReads(fetchMock)).toEqual([]);
});
