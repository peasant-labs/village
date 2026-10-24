import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import YAML from "yaml";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The cache needs Next's incremental cache, which only exists inside a running
// server. This stand-in keeps the contract that matters here: a result is
// stored under its key parts, and a thrown read is not stored.
const store = vi.hoisted(() => new Map<string, unknown>());
vi.mock("next/cache", () => ({
  unstable_cache: <T>(fn: () => Promise<T>, keyParts: string[]) => async (): Promise<T> => {
    const key = JSON.stringify(keyParts);
    if (store.has(key)) return store.get(key) as T;
    const value = await fn();
    store.set(key, value);
    return value;
  },
}));

import { loadPublicTranscript } from "./loadPublicTranscript";

type Reply =
  | { kind: "response"; status: number; visibility?: string; updated_at?: string }
  | { kind: "reject" }
  | { kind: "hang" };

interface Step {
  meta: Reply;
  content?: Reply;
  expect: "content" | "null";
}

interface Case {
  name: string;
  env?: boolean;
  parallel?: boolean;
  maxMs?: number;
  steps: Step[];
  metadataFetches: number;
  contentFetches: number;
}

function loadCases(): Case[] {
  const raw = readFileSync(resolve(process.cwd(), "testdata/share-card/cases.yaml"), "utf8");
  return (YAML.parse(raw) as { cases: Case[] }).cases;
}

const CONTENT_BODY = JSON.stringify({ role: "user", text: "hello" });

function reply(r: Reply, body: () => string, signal?: AbortSignal | null): Promise<Response> {
  if (r.kind === "reject") return Promise.reject(new TypeError("fetch failed"));
  if (r.kind === "hang") {
    return new Promise((_, reject) => {
      signal?.addEventListener("abort", () => reject(signal.reason));
    });
  }
  return Promise.resolve(new Response(r.status === 200 ? body() : "{}", { status: r.status }));
}

function makeFetch(steps: Step[]) {
  const metaQueue = steps.map((s) => s.meta);
  const contentQueue = steps.flatMap((s) => (s.content ? [s.content] : []));
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const fetchFn = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, init });
    if (url.endsWith("/content")) {
      const r = contentQueue.shift();
      if (!r) throw new Error("unexpected content fetch");
      return reply(r, () => CONTENT_BODY, init?.signal);
    }
    const r = metaQueue.shift();
    if (!r) throw new Error("unexpected metadata fetch");
    return reply(
      r,
      () =>
        JSON.stringify({
          transcript: {
            id: "t1",
            visibility: r.kind === "response" ? r.visibility : undefined,
            updated_at: r.kind === "response" ? r.updated_at : undefined,
            title: "a title",
          },
        }),
      init?.signal,
    );
  });
  return { fetchFn: fetchFn as unknown as typeof fetch, calls };
}

const cases = loadCases();

describe("loadPublicTranscript", () => {
  beforeEach(() => {
    store.clear();
    vi.stubEnv("VILLAGE_INTERNAL_API_URL", "http://backend:8080/api/v1");
    vi.stubEnv("VILLAGE_PUBLIC_ORIGIN", "https://village.example");
  });
  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it("has every required case", () => {
    const required = [
      "public-returns-content",
      "shared",
      "private",
      "missing",
      "api-error-500",
      "timeout",
      "no-env",
      "visibility-flip",
      "single-flight",
      "no-auth-header",
    ];
    expect(cases.map((c) => c.name)).toEqual(expect.arrayContaining(required));
  });

  it.each(cases.map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    if (c.env === false) vi.stubEnv("VILLAGE_INTERNAL_API_URL", "");
    const { fetchFn, calls } = makeFetch(c.steps);
    const started = Date.now();

    const run = () => loadPublicTranscript("t1", { fetch: fetchFn });
    const results = c.parallel
      ? await Promise.all(c.steps.map(run))
      : await c.steps.reduce<Promise<Awaited<ReturnType<typeof run>>[]>>(
          async (acc, step, i) => {
            const out = await acc;
            const result = await run();
            expect(result === null ? "null" : "content", `step ${i}`).toBe(step.expect);
            return [...out, result];
          },
          Promise.resolve([]),
        );
    if (c.parallel) results.forEach((r, i) => expect(r === null ? "null" : "content", `step ${i}`).toBe(c.steps[i].expect));

    expect(Date.now() - started).toBeLessThan(c.maxMs ?? 1000);
    expect(calls.filter((x) => !x.url.endsWith("/content"))).toHaveLength(c.metadataFetches);
    expect(calls.filter((x) => x.url.endsWith("/content"))).toHaveLength(c.contentFetches);

    for (const { url, init } of calls) {
      const headers = new Headers(init?.headers);
      expect(headers.has("authorization"), url).toBe(false);
      expect(headers.has("cookie"), url).toBe(false);
      expect(init?.credentials, url).toBe("omit");
      expect(init?.signal, url).toBeDefined();
    }
    for (const x of calls.filter((x) => !x.url.endsWith("/content"))) {
      expect(x.init?.cache).toBe("no-store");
      expect(x.url).toBe("http://backend:8080/api/v1/transcripts/t1");
    }
    if (c.name === "public-returns-content") {
      expect(results[0]).toMatchObject({
        transcript: { id: "t1", visibility: "public" },
        content: { role: "user", text: "hello" },
      });
    }
  });

  it("answers a forbidden id exactly as a missing one", async () => {
    const forbidden = makeFetch([{ meta: { kind: "response", status: 403 }, expect: "null" }]);
    const missing = makeFetch([{ meta: { kind: "response", status: 404 }, expect: "null" }]);
    const a = await loadPublicTranscript("t1", { fetch: forbidden.fetchFn });
    const b = await loadPublicTranscript("t1", { fetch: missing.fetchFn });
    expect(a).toStrictEqual(b);
    expect(a).toBeNull();
  });

  it("never throws when fetch itself throws synchronously", async () => {
    const boom = vi.fn(() => {
      throw new Error("boom");
    }) as unknown as typeof fetch;
    await expect(loadPublicTranscript("t1", { fetch: boom })).resolves.toBeNull();
  });
});
