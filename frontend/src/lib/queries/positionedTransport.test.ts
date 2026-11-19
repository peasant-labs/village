import { readFileSync } from "node:fs";
import { afterEach, expect, it, vi } from "vitest";
import { parse } from "yaml";
import { villagePositionedTransport } from "./positionedTransport";

type Case = {
  name: string; operation: "header" | "directory" | "segment" | "window" | "locate";
  request: object; path: string; query: Record<string, string>; post?: boolean;
};
type RetryCase = { name: string; header?: string; expected?: number };
const fixture = parse(readFileSync("src/testdata/positioned-transport.yaml", "utf8")) as { cases: Case[]; retry: RetryCase[] };
const required = ["transport-header", "transport-directory", "transport-segment", "transport-window", "transport-locate",
  "retry-after-seconds", "retry-after-date", "retry-after-absent", "retry-after-garbage"];
afterEach(() => vi.unstubAllGlobals());

it("retains the named transport cases", () => {
  expect([...fixture.cases, ...fixture.retry].map(({ name }) => name).sort()).toEqual([...required].sort());
});

for (const item of fixture.cases) {
  it(item.name, async () => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    const signal = new AbortController().signal;
    const method = villagePositionedTransport("a/b")[item.operation]!;
    await method(item.request, signal);
    const [input, init] = fetchMock.mock.calls[0];
    const url = new URL(String(input));
    expect(url.pathname).toBe(`/api/v1/transcripts/a%2Fb${item.path}`);
    expect(Object.fromEntries(url.searchParams)).toEqual(item.query);
    expect(init?.cache).toBe("no-store");
    expect(init?.signal).toBe(signal);
    expect(init?.method).toBe(item.post ? "POST" : "GET");
    if (item.post) expect(JSON.parse(String(init?.body))).toEqual(item.request);
  });
}

for (const item of fixture.retry) {
  it(item.name, async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", {
      status: 503, headers: item.header ? { "Retry-After": item.header } : {},
    })));
    const result = await villagePositionedTransport("id", () => 0).directory({}, new AbortController().signal);
    expect(result.retryAfterSeconds).toBe(item.expected);
  });
}

it("transport-network-error-rejects", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => { throw new Error("offline"); }));
  await expect(villagePositionedTransport("id").directory({}, new AbortController().signal)).rejects.toThrow("offline");
});
