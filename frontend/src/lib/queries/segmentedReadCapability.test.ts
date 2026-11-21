import { readFileSync } from "node:fs";
import { afterEach, expect, it, vi } from "vitest";
import { parse } from "yaml";
import { testdataPath } from "@/test/testdataPath";
import { segmentedReadCapability, TranscriptReadMode } from "./segmentedReadCapability";

const FIXTURE = testdataPath("segmented-read-capability.yaml");

type Fixture = { name: string; body?: string; positioned?: boolean; rejected?: boolean; hangs?: boolean };
const { cases } = parse(readFileSync(FIXTURE, "utf8")) as { cases: Fixture[] };
const requiredNames = [
  "capability-advertised-mounts-positioned", "capability-v1-only-uses-content",
  "capability-later-without-0.2.0-uses-content", "capability-later-serving-0.2.0-mounts-positioned",
  "capability-lookalike-token-refused-leading", "capability-lookalike-min-token-refused",
  "capability-lookalike-token-refused-trailing", "capability-lookalike-token-refused-middle",
  "capability-min-only-uses-content", "capability-min-token-serves-0.2.0", "capability-empty-body-uses-content",
  "capability-refused-pair-uses-content", "capability-ignores-health-trait",
  "capability-read-failure-uses-content", "capability-timeout-uses-content",
];

afterEach(() => vi.unstubAllGlobals());

/** What the race below settles to when the version read has not answered. */
const PENDING = Symbol("the version read is still pending");
/** Long enough for an aborted read to settle; a read that ignores its deadline never does. */
const PENDING_SENTINEL_MS = 50;

it("retains all capability cases", () => {
  expect(cases.map(({ name }) => name).sort()).toEqual([...requiredNames].sort());
});

it("capability-closed-set", () => {
  expect(Object.values(TranscriptReadMode).sort()).toEqual(["content", "positioned"]);
});

for (const fixture of cases) {
  it(fixture.name, async () => {
    // A hanging read answers only by rejecting when its signal aborts, as fetch does.
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      expect(String(input)).toMatch(/\/schema\/version$/);
      if (fixture.hangs) {
        return new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener("abort", () => reject(init.signal?.reason));
        });
      }
      if (fixture.rejected) return Promise.reject(new Error("offline"));
      return Promise.resolve(new Response(fixture.body, { status: 200 }));
    });
    vi.stubGlobal("fetch", fetchMock);
    // The deadline is the test's own, fired once the read is in flight.
    const deadline = new AbortController();
    const mode = segmentedReadCapability(undefined, deadline.signal);
    if (fixture.hangs) deadline.abort(new DOMException("the version read timed out", "TimeoutError"));
    // A read that ignores the deadline never settles. Race it against a short
    // sentinel, so that read fails this assertion by name, not the runner's
    // own test timeout.
    let sentinelTimer: ReturnType<typeof setTimeout> | undefined;
    const sentinel = new Promise<typeof PENDING>((settle) => {
      sentinelTimer = setTimeout(() => settle(PENDING), PENDING_SENTINEL_MS);
    });
    const settled = await Promise.race([mode, sentinel]);
    clearTimeout(sentinelTimer);
    expect(settled).toBe(
      fixture.positioned ? TranscriptReadMode.Positioned : TranscriptReadMode.Content,
    );
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(String(fetchMock.mock.calls[0][0])).toMatch(/\/schema\/version$/);
  });
}
