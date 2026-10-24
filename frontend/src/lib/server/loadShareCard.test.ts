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

import { loadShareCard } from "./loadShareCard";

type Payload = "figures" | "no-figures" | "unparseable";
type Reply =
  | {
      kind: "response";
      status: number;
      visibility?: string;
      updated_at?: string | number;
      payload?: Payload;
      delayMs?: number;
    }
  | { kind: "reject" }
  | { kind: "hang" };

type Expect = "card-figures" | "card-no-figures" | "null";

interface Step {
  meta: Reply;
  content?: Reply;
  expect: Expect;
}

interface Case {
  name: string;
  env?: boolean;
  parallel?: boolean;
  fakeTimers?: boolean;
  steps: Step[];
  metadataFetches: number;
  contentFetches: number;
  cacheValues?: "card" | "empty";
}

const cases = (
  YAML.parse(readFileSync(resolve(process.cwd(), "testdata/share-card/cases.yaml"), "utf8")) as { cases: Case[] }
).cases;

const REQUIRED = [
  "public-both-figures",
  "public-no-figures",
  "shared",
  "private",
  "missing",
  "visibility-flip",
  "deadline",
  "missing-updated-at",
  "unparseable-content",
  "caches-card-not-content",
  "single-flight",
  "no-auth-header",
];

// A transcript text that must never reach the cache.
const SECRET_PROMPT = "rewrite billing retries with SECRET-PROMPT-TEXT";

function sessionDetail(): Record<string, unknown> {
  return {
    id: "ses_share",
    harness: "claude-code",
    model: "claude-opus-5-5",
    title: "rewrite billing retries",
    startTime: "2026-07-16T08:00:00Z",
    endTime: "2026-07-16T08:01:00Z",
    durationMins: 1,
    totalTokens: 84000,
    tokensIn: 70000,
    tokensOut: 14000,
    turnCount: 2,
    toolCallCount: 0,
    inputSubmissionCount: 6,
    turns: [
      { index: 0, role: "user", content: SECRET_PROMPT, timestamp: "2026-07-16T08:00:00Z", depth: 0 },
      { index: 1, role: "assistant", content: "done", timestamp: "2026-07-16T08:00:10Z", depth: 0 },
    ],
  };
}

function contentBody(payload: Payload = "figures"): string {
  if (payload === "unparseable") return JSON.stringify({ not: "a session" });
  const detail = sessionDetail();
  if (payload === "no-figures") delete detail.inputSubmissionCount;
  return JSON.stringify(detail);
}

function reply(r: Reply, body: () => string, signal?: AbortSignal | null): Promise<Response> {
  if (r.kind === "reject") return Promise.reject(new TypeError("fetch failed"));
  if (r.kind === "hang") {
    return new Promise((_, reject) => {
      signal?.addEventListener("abort", () => reject(signal.reason));
    });
  }
  const response = new Response(r.status === 200 ? body() : "{}", { status: r.status });
  if (!r.delayMs) return Promise.resolve(response);
  return new Promise((resolveReply, reject) => {
    signal?.addEventListener("abort", () => reject(signal.reason));
    setTimeout(() => resolveReply(response), r.delayMs);
  });
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
      return reply(r, () => contentBody(r.kind === "response" ? r.payload : undefined), init?.signal);
    }
    const r = metaQueue.shift();
    if (!r) throw new Error("unexpected metadata fetch");
    return reply(
      r,
      () =>
        JSON.stringify({
          transcript: {
            id: "t1",
            title: "a title",
            visibility: r.kind === "response" ? r.visibility : undefined,
            updated_at: r.kind === "response" ? r.updated_at : undefined,
          },
        }),
      init?.signal,
    );
  });
  return { fetchFn: fetchFn as unknown as typeof fetch, calls };
}

function label(result: unknown): Expect {
  if (result === null) return "null";
  const card = result as { figures: unknown };
  return card.figures ? "card-figures" : "card-no-figures";
}

describe("loadShareCard", () => {
  beforeEach(() => {
    store.clear();
    vi.stubEnv("VILLAGE_INTERNAL_API_URL", "http://backend:8080/api/v1");
    vi.stubEnv("VILLAGE_PUBLIC_ORIGIN", "https://village.example");
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllEnvs();
  });

  it("has every required case", () => {
    expect(cases.map((c) => c.name)).toEqual(expect.arrayContaining(REQUIRED));
  });

  it.each(cases.map((c) => [c.name, c] as const))("%s", async (_name, c) => {
    if (c.env === false) vi.stubEnv("VILLAGE_INTERNAL_API_URL", "");
    if (c.fakeTimers) vi.useFakeTimers();
    const { fetchFn, calls } = makeFetch(c.steps);
    const run = () => loadShareCard("t1", { fetch: fetchFn });

    const results: unknown[] = [];
    if (c.parallel) {
      const pending = c.steps.map(run);
      results.push(...(await Promise.all(pending)));
    } else if (c.fakeTimers) {
      const pending = run();
      await vi.advanceTimersByTimeAsync(3000);
      results.push(await pending);
    } else {
      for (const step of c.steps) {
        const result = await run();
        results.push(result);
        expect(label(result), `step ${results.length - 1}`).toBe(step.expect);
      }
    }
    results.forEach((r, i) => expect(label(r), `step ${i}`).toBe(c.steps[i].expect));

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

    if (c.cacheValues === "empty") expect(store.size).toBe(0);
    if (c.cacheValues === "card") {
      expect(store.size).toBeGreaterThan(0);
      for (const [key, value] of store) {
        const card = value as Record<string, unknown>;
        expect(Object.keys(card).sort(), key).toEqual(
          ["brand", "chip", "figures", "firstPrompt", "firstPromptIsTitle", "harness", "title"].sort(),
        );
        expect(key).not.toContain("SECRET-PROMPT-TEXT");
        expect(key).not.toContain("assistant");
      }
    }
    if (c.name === "public-both-figures") {
      expect(results[0]).toMatchObject({
        title: "rewrite billing retries",
        harness: "claude-code",
        brand: "claude",
        chip: "Claude Code (Opus 5.5)",
        figures: { prompts: 6, tokens: 84000 },
      });
    }
    if (c.name === "caches-card-not-content") {
      // The stored value is the card the caller got, and the transcript text appears nowhere but the prompt line.
      const [stored] = [...store.values()];
      expect(stored).toStrictEqual(results[0]);
      expect(JSON.stringify([...store.keys()])).not.toContain("turns");
    }
  });

  it("answers a forbidden id exactly as a missing one", async () => {
    const forbidden = makeFetch([{ meta: { kind: "response", status: 403 }, expect: "null" }]);
    const missing = makeFetch([{ meta: { kind: "response", status: 404 }, expect: "null" }]);
    const a = await loadShareCard("t1", { fetch: forbidden.fetchFn });
    const b = await loadShareCard("t1", { fetch: missing.fetchFn });
    expect(a).toStrictEqual(b);
    expect(a).toBeNull();
  });

  it("never throws when fetch itself throws synchronously", async () => {
    const boom = vi.fn(() => {
      throw new Error("boom");
    }) as unknown as typeof fetch;
    await expect(loadShareCard("t1", { fetch: boom })).resolves.toBeNull();
  });
});
