import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it, vi } from "vitest";
import { parse } from "yaml";
import { villagePositionedTransport } from "./positionedTransport";

type Case = {
  name: string; operation: "header" | "directory" | "segment" | "window" | "locate";
  request: object; path: string; query: Record<string, string>; post?: boolean;
};
type RetryCase = { name: string; header?: string; now?: number; expected?: number };
const fixture = parse(readFileSync("src/testdata/positioned-transport.yaml", "utf8")) as {
  sendsV2AndEcho: Case[]; retryAfterParse: RetryCase[];
};
const required = {
  sendsV2AndEcho: ["header", "directory-page-one", "directory-later-page-adds-no-echo", "segment", "window",
    "window-default-open", "locate"],
  retryAfterParse: ["seconds", "seconds-zero", "seconds-surrounding-space", "http-date", "http-date-rounds-up",
    "http-date-past-is-zero", "http-date-rfc850", "http-date-asctime-is-gmt", "http-date-impossible-day",
    "http-date-rollover-day", "http-date-not-gmt", "negative-number-not-a-date", "fractional-number-not-a-date",
    "seconds-beyond-safe-integer", "absent", "garbage"],
};
afterEach(() => vi.unstubAllGlobals());

it("retains the named transport rows", () => {
  expect(fixture.sendsV2AndEcho.map(({ name }) => name).sort()).toEqual([...required.sendsV2AndEcho].sort());
  expect(fixture.retryAfterParse.map(({ name }) => name).sort()).toEqual([...required.retryAfterParse].sort());
});

describe("transport-sends-v2-and-echo", () => {
  for (const item of fixture.sendsV2AndEcho) {
    it(item.name, async () => {
      const fetchMock = vi.fn(async (...request: [RequestInfo | URL, RequestInit?]) => (void request, new Response("{}", { status: 200 })));
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
});

describe("transport-retry-after-parse", () => {
  for (const item of fixture.retryAfterParse) {
    it(item.name, async () => {
      vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", {
        status: 503, headers: item.header !== undefined ? { "Retry-After": item.header } : {},
      })));
      const result = await villagePositionedTransport("id", () => item.now ?? 0).directory({}, new AbortController().signal);
      expect(result.status).toBe(503);
      expect(result.retryAfterSeconds).toBe(item.expected);
    });
  }
});

it("transport-network-error-rejects", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => { throw new Error("offline"); }));
  await expect(villagePositionedTransport("id").directory({}, new AbortController().signal)).rejects.toThrow("offline");
});
