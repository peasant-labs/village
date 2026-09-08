import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { parse } from "yaml";
import { describe, expect, it } from "vitest";
import { adaptExplore, effectiveExploreTokens } from "./explore";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";

type Case = { name: string; tokens_in?: number | null; tokens_out?: number | null; token_count?: number | null; expected: number | null };
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

describe("Explore authoritative facet adapter", () => {
  it("preserves nonempty and explicit-empty facets at the reviewed top-level seam", () => {
    const owner = { id: "owner", github_id: 1, github_username: "owner", display_name: null, avatar_url: null, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", is_discoverable: true, username_chosen: true, provider_username: null };
    const row = { transcript: makeTranscriptFixture(), tags: [], owner };
    const base = { transcripts: [row], total: 99, agent_total: 0, page: 1, limit: 24 };
    const nonempty = adaptExplore({ ...base, harness_facets: [{ harness: "codex", count: 24 }] }, { collectives: [] }, []);
    expect(nonempty.harnessFacets).toEqual([{ harness: "codex", count: 24 }]);
    expect(nonempty.transcripts.transcripts).toHaveLength(1);
    expect(nonempty.transcripts.total).toBe(99);
    expect(adaptExplore({ ...base, harness_facets: [] }, { collectives: [] }, []).harnessFacets).toEqual([]);
  });
});
