import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { describe, expect, it } from "vitest";
import { effectiveExploreTokens } from "./explore";

type Case = { name: string; tokens_in: number | null; tokens_out: number | null; token_count: number | null; expected: number | null };
type Fixture = { required_names: string[]; cases: Case[] };
const fixture = parse(readFileSync(resolve(process.cwd(), "src/testdata/explore-adapter.yaml"), "utf8")) as Fixture;

describe("Explore effective token adapter", () => {
  it("retains every required named behavior", () => {
    expect(new Set(fixture.cases.map((entry) => entry.name))).toEqual(new Set(fixture.required_names));
  });
  for (const entry of fixture.cases) {
    it(entry.name, () => expect(effectiveExploreTokens(entry)).toBe(entry.expected));
  }
});
