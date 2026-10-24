import { isValidElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The transcript route's server entry as Next calls it: the metadata and the
// page for one request, with the request's cookies and headers stubbed and the
// API answered by a stubbed `fetch`. The decision itself is driven case by case
// in `src/signInRoute.test.ts`; this pins how the route wires it.

// One request: its cookie and its headers, the same headers object for every
// read Next makes while serving it.
const request = vi.hoisted(() => ({ cookie: null as string | null, headers: new Headers() }));

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) => (name === "peasant_token" && request.cookie !== null ? { name, value: request.cookie } : undefined),
  }),
  headers: async () => request.headers,
}));

class Redirected extends Error {
  constructor(readonly destination: string) {
    super(`redirected to ${destination}`);
  }
}

vi.mock("next/navigation", async () => ({
  ...(await import("@/test/nextNavigationMock")),
  redirect: (destination: string) => {
    throw new Redirected(destination);
  },
}));

// The card cache needs Next's incremental cache; this stand-in never stores.
vi.mock("next/cache", () => ({
  unstable_cache: <T,>(fn: () => Promise<T>) => fn,
}));

import Page, { generateMetadata } from "./page";
import TranscriptDetailPage from "./TranscriptDetailPage";

const ID = "7f1d2c3b";

function api(visibility: "public" | null) {
  const calls: string[] = [];
  const fetchFn = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    calls.push(url);
    if (visibility === null) return new Response("{}", { status: 404 });
    if (url.endsWith("/content")) {
      return new Response(
        JSON.stringify({
          id: "ses_1",
          harness: "claude-code",
          title: "rewrite billing retries",
          startTime: "2026-07-16T08:00:00Z",
          endTime: "2026-07-16T08:01:00Z",
          durationMins: 1,
          totalTokens: 10,
          tokensIn: 5,
          tokensOut: 5,
          turnCount: 1,
          toolCallCount: 0,
          turns: [{ index: 0, role: "user", content: "rewrite billing retries", timestamp: "2026-07-16T08:00:00Z", depth: 0 }],
        }),
        { status: 200 },
      );
    }
    return new Response(
      JSON.stringify({
        transcript: { id: ID, title: "rewrite billing retries", visibility, updated_at: "2026-07-16T08:02:00Z" },
      }),
      { status: 200 },
    );
  });
  vi.stubGlobal("fetch", fetchFn);
  return calls;
}

async function page(search: Record<string, string> = {}) {
  return Page({ params: Promise.resolve({ id: ID }), searchParams: Promise.resolve(search) });
}

describe("the transcript route on the server", () => {
  beforeEach(() => {
    vi.stubEnv("VILLAGE_INTERNAL_API_URL", "http://backend:8080/api/v1");
    vi.stubEnv("VILLAGE_PUBLIC_ORIGIN", "https://village.example");
    request.cookie = null;
    request.headers = new Headers({ "sec-fetch-dest": "document" });
  });
  afterEach(() => {
    vi.unstubAllEnvs();
    vi.unstubAllGlobals();
  });

  it("serves a public transcript to a signed-out visitor, marked open", async () => {
    api("public");
    const element = await page();
    expect(isValidElement(element)).toBe(true);
    expect(element.type).toBe(TranscriptDetailPage);
    expect((element.props as { openSignedOut: boolean }).openSignedOut).toBe(true);
  });

  it("keeps a public transcript's page out of search results", async () => {
    api("public");
    const metadata = await generateMetadata({ params: Promise.resolve({ id: ID }) });
    // The card's own metadata, and still noindex: open to anyone with the link,
    // never listed.
    expect(metadata.title).toBe("rewrite billing retries");
    expect(metadata.robots).toEqual({ index: false, follow: false });
  });

  it("sends a signed-out visitor to sign in for a transcript that is not public", async () => {
    api(null);
    await expect(page({ turn: "2" })).rejects.toEqual(
      new Redirected("https://peasantlabs.org/?login&next=%2Ftranscripts%2F7f1d2c3b%3Fturn%3D2"),
    );
  });

  it("reads the API once per request, a refused one included", async () => {
    const calls = api(null);
    // The page refuses, and Next renders the redirect again with its metadata.
    await expect(page()).rejects.toBeInstanceOf(Redirected);
    await generateMetadata({ params: Promise.resolve({ id: ID }) });
    await expect(page()).rejects.toBeInstanceOf(Redirected);
    expect(calls).toEqual(["http://backend:8080/api/v1/transcripts/7f1d2c3b"]);

    // The next request reads again: nothing is reused across requests.
    request.headers = new Headers({ "sec-fetch-dest": "document" });
    await expect(page()).rejects.toBeInstanceOf(Redirected);
    expect(calls).toHaveLength(2);
  });

  it("serves a signed-in request without reading the transcript anonymously", async () => {
    const calls = api(null);
    request.cookie = "demo-token";
    const element = await page();
    expect((element.props as { openSignedOut: boolean }).openSignedOut).toBe(false);
    expect(calls).toEqual([]);
  });
});
