import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { describe, expect, it } from "vitest";
import { formatRecordedDuration, formatRelativeTime } from "@/lib/format";
import { formatCount } from "@/lib/ft-ui";
import { assertExactKeys, assertNamesMatch } from "@/test/fixtureAssertions";

type RelativeTimeCase = { name: string; now: string; at: string; expect: string };
type DurationCase = { name: string; ms: number; expect: string };
type CompactCase = { name: string; n: number; expect: string };

const requiredRelativeTimeNames = [
  "under-a-minute-is-just-now",
  "a-skewed-future-timestamp-is-just-now",
  "minutes",
  "hours",
  "one-day-is-yesterday",
  "days",
  "seven-to-thirteen-days-is-last-week",
  "weeks",
  "past-eight-weeks-is-the-date",
  "an-unparseable-timestamp-states-nothing",
] as const;

const requiredDurationNames = [
  "nothing-recorded",
  "under-a-minute-is-no-minutes",
  "minutes-below-an-hour",
  "whole-hours-rounded-down",
  "thousands-of-hours-are-grouped",
] as const;

const requiredCompactNames = [
  "below-a-thousand-is-the-number",
  "thousands",
  "a-round-thousand-drops-its-decimal",
  "rounding-up-to-a-thousand-thousand-is-a-million",
  "millions",
  "billions",
  "rounding-up-to-a-thousand-million-is-a-billion",
  "trillions",
] as const;

function load(): { relativeTimeCases: RelativeTimeCase[]; durationCases: DurationCase[]; compactCases: CompactCase[] } {
  const parsed = parse(
    readFileSync(resolve(process.cwd(), "src/testdata/summary-formats.yaml"), "utf8"),
    { strict: true },
  ) as { relativeTimeCases: RelativeTimeCase[]; durationCases: DurationCase[]; compactCases: CompactCase[] };
  assertExactKeys(parsed, ["relativeTimeCases", "durationCases", "compactCases"], "summary-formats root");
  assertNamesMatch(
    parsed.relativeTimeCases.map((c) => c.name),
    requiredRelativeTimeNames,
    "summary-formats relativeTimeCases",
  );
  assertNamesMatch(
    parsed.durationCases.map((c) => c.name),
    requiredDurationNames,
    "summary-formats durationCases",
  );
  for (const c of parsed.relativeTimeCases) assertExactKeys(c, ["name", "now", "at", "expect"], c.name);
  for (const c of parsed.durationCases) assertExactKeys(c, ["name", "ms", "expect"], c.name);
  assertNamesMatch(parsed.compactCases.map((c) => c.name), requiredCompactNames, "summary-formats compactCases");
  for (const c of parsed.compactCases) assertExactKeys(c, ["name", "n", "expect"], c.name);
  return parsed;
}

const { relativeTimeCases, durationCases, compactCases } = load();

describe("how long ago, for a list column", () => {
  for (const c of relativeTimeCases) {
    it(c.name, () => {
      expect(formatRelativeTime(c.at, Date.parse(c.now))).toBe(c.expect);
    });
  }
});

describe("a recorded duration, for the summary line", () => {
  for (const c of durationCases) {
    it(c.name, () => {
      expect(formatRecordedDuration(c.ms)).toBe(c.expect);
    });
  }
});

describe("a count shortened for a glance", () => {
  for (const c of compactCases) {
    it(c.name, () => {
      expect(formatCount(c.n)).toBe(c.expect);
    });
  }
});
