import { readFileSync } from "node:fs";
import { afterEach, expect, it, vi } from "vitest";
import { parse } from "yaml";
import { segmentedReadCapability, TranscriptReadMode } from "./segmentedReadCapability";

type Fixture = { name: string; body?: string; positioned?: boolean; rejected?: boolean };
const { cases } = parse(readFileSync("src/testdata/segmented-read-capability.yaml", "utf8")) as { cases: Fixture[] };
const requiredNames = [
  "capability-advertised-mounts-positioned", "capability-v1-only-uses-content",
  "capability-later-without-0.2.0-uses-content", "capability-lookalike-token-refused-leading",
  "capability-lookalike-token-refused-trailing", "capability-lookalike-token-refused-middle",
  "capability-min-only-uses-content", "capability-min-token-does-not-enable", "capability-empty-body-uses-content",
  "capability-refused-pair-uses-content", "capability-ignores-health-trait",
  "capability-read-failure-uses-content",
];

afterEach(() => vi.unstubAllGlobals());

it("retains all capability cases", () => {
  expect(cases.map(({ name }) => name).sort()).toEqual([...requiredNames].sort());
});

it("capability-closed-set", () => {
  expect(Object.values(TranscriptReadMode).sort()).toEqual(["content", "positioned"]);
});

for (const fixture of cases) {
  it(fixture.name, async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      expect(String(input)).toMatch(/\/schema\/version$/);
      if (fixture.rejected) throw new Error("offline");
      return new Response(fixture.body, { status: 200 });
    });
    vi.stubGlobal("fetch", fetchMock);
    expect(await segmentedReadCapability()).toBe(
      fixture.positioned ? TranscriptReadMode.Positioned : TranscriptReadMode.Content,
    );
    expect(fetchMock).toHaveBeenCalledOnce();
    expect(String(fetchMock.mock.calls[0][0])).toMatch(/\/schema\/version$/);
  });
}
