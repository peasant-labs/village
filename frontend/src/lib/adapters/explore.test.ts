import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { createElement } from "react";
import userEvent from "@testing-library/user-event";
import { Explore } from "@peasant-labs/fairtrade/commons";
import { adaptExplore, effectiveExploreTokens } from "./explore";
import { makeTranscriptFixture } from "@/test/transcriptRowFixture";
import { loadExploreAdapterFixtures, tokenTranscript } from "@/test/exploreAdapterFixtures";

const cases = loadExploreAdapterFixtures();
afterEach(cleanup);

describe("Explore effective token adapter", () => {
  for (const entry of cases) {
    it(entry.name, async () => {
      expect(effectiveExploreTokens(entry)).toBe(entry.expected);
      const owner = { id: "owner", github_id: 1, github_username: "owner", display_name: null, avatar_url: null, created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z", is_discoverable: true, username_chosen: true, provider_username: null };
      const transcript = tokenTranscript(entry);
      for (const field of ["tokens_in", "tokens_out", "token_count"]) {
        expect(Object.hasOwn(transcript, field)).toBe(Object.hasOwn(entry, field));
      }
      const payload = adaptExplore({ transcripts: [{ transcript, tags: [], owner }], harness_facets: [], total: 1, agent_total: 0, page: 1, limit: 24 }, { collectives: [] }, []);
      expect(payload.transcripts.transcripts[0].tokenCount).toBe(entry.expected);
      render(createElement(Explore, { data: payload }));
      await userEvent.setup().click(screen.getByRole("button", { name: /fixture session/i }));
      // The published component's detail view renders null as zero; the adapter assertion above
      // separately preserves the wire distinction between absent usage and zero.
      expect(screen.getByText(`${entry.expected ?? 0} tokens`)).toBeVisible();
    });
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
