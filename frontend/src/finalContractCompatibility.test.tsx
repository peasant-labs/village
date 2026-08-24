import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { Harness as SchemaHarness, isHarness, type Harness } from "@peasant-labs/schema";
import type { SessionDetailPayload } from "@peasant-labs/schema";
import { afterEach, describe, expect, it } from "vitest";
import { SessionDetailV2 } from "@/components/session-detail/v2/SessionDetailV2";
import { adaptExplore } from "@/lib/adapters/explore";
import type { Transcript, TranscriptListResponse, User } from "@/lib/types";
import { loadFinalContractCompatibilityFixtures } from "@/test/finalContractCompatibilityFixtures";

const fixtures = loadFinalContractCompatibilityFixtures();

function transcriptList(modelProvider: string, transcriptID: string): TranscriptListResponse {
  const transcript: Transcript = {
    id: transcriptID,
    owner_id: "owner-1",
    local_id: "session-1",
    title: "contract fixture",
    description: null,
    visibility: "public",
    model_provider: modelProvider,
    model_name: "fixture-model",
    harness_version: null,
    session_start: null,
    session_end: null,
    turn_count: 0,
    token_count: 0,
    blob_size_bytes: 0,
    schema_version: "0.1.0",
    published_at: "2026-08-03T00:00:00Z",
    updated_at: "2026-08-03T00:00:00Z",
    parent_session_id: null,
    ingested_at: null,
    source_format: null,
    project_name: "fixture-project",
    git_remote: null,
    git_branch: null,
    duration_ms: 0,
    tool_call_count: 0,
    tokens_in: 0,
    tokens_out: 0,
    project_hash: null,
    subagent_count: 0,
    subagents: [],
    diagnostics_warnings: [],
    diagnostics_partial: false,
    title_generated: null,
    outcome: null,
    files_touched: null,
    lines_changed: null,
    retry_loops: null,
    retry_tokens_wasted: null,
    within_session_reverts: null,
    signal_density: null,
    spec_quality_score: null,
    exploration_ratio: null,
    scope_breadth: null,
    discovery_turns: null,
    m2_token_outcome_ratio: null,
    m3_unique_tool_count: null,
    m4_error_recovery_count: null,
    m4_consecutive_error_max: null,
    m5_context_utilization_pct: null,
    m5_peak_context_tokens: null,
    m5_avg_message_tokens: null,
    m6_output_survival_pct: null,
    m6_lines_survived: null,
    m6_lines_total: null,
    m7_spec_word_count: null,
    m7_spec_has_examples: null,
    m7_spec_has_constraints: null,
    computed_at: null,
    compute_version: null,
    content_hash: null,
    license_id: null,
    session_origin: "user",
  };
  const owner: User = {
    id: "owner-1",
    github_id: 1,
    github_username: "fixture-owner",
    display_name: "Fixture Owner",
    avatar_url: null,
    created_at: "2026-08-03T00:00:00Z",
    updated_at: "2026-08-03T00:00:00Z",
    is_discoverable: true,
    username_chosen: true,
    provider_username: "fixture-owner",
  };
  return { transcripts: [{ transcript, tags: [], owner }], total: 1, agent_total: 0, page: 1, limit: 20 };
}

function sessionDetail(turnsState: "omitted" | "null" | "nullable-fields", stopReasons?: ["max_turn_requests", "refusal"]): SessionDetailPayload {
  const turns = turnsState === "nullable-fields"
    ? stopReasons?.map((stopReason, index) => ({
        index,
        role: "assistant" as const,
        content: index === 0 ? "nullable contract turn" : "second contract turn",
        timestamp: "2026-08-03T00:00:00Z",
        depth: 0,
        stopReason,
        tokensIn: null,
        tokensOut: null,
        toolCalls: [{ id: `tool-${index}`, name: "fixture", arguments: "{}", result: "", durationMs: null, exitCode: null }],
      }))
    : turnsState === "null" ? null : undefined;
  const detail = {
    id: `session-${turnsState}`,
    harness: "claude-code" as const,
    startTime: "2026-08-03T00:00:00Z",
    endTime: "2026-08-03T00:01:00Z",
    durationMins: 1,
    totalTokens: 0,
    tokensIn: 0,
    tokensOut: 0,
    turnCount: turns?.length ?? 0,
    toolCallCount: turns?.length ?? 0,
  };
  if (turnsState === "omitted") return detail as SessionDetailPayload;
  return { ...detail, turns: turns ?? null };
}

function renderSession(detail: SessionDetailPayload): void {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <SessionDetailV2
        sessionId={detail.id}
        transcriptId={`transcript-${detail.id}`}
        projectName="fixture-project"
        detail={detail}
      />
    </QueryClientProvider>,
  );
}

afterEach(cleanup);

describe("final package compatibility on production paths", () => {
  it("accepts the complete published Schema Harness enum through the Explore boundary", () => {
    expect(new Set(fixtures.harnesses.map(({ value }) => value))).toEqual(new Set(Object.values(SchemaHarness)));
    for (const fixture of fixtures.harnesses) {
      expect(isHarness(fixture.value)).toBe(true);
      expect(adaptExplore(transcriptList(fixture.value, `transcript-${fixture.name}`), { collectives: [] }, []).transcripts.transcripts[0]?.modelProvider)
        .toBe(fixture.value as Harness);
    }
  });

  it(fixtures.offContractHarness.name, () => {
    const fixture = fixtures.offContractHarness;
    expect(isHarness(fixture.value)).toBe(false);
    expect(() => adaptExplore(transcriptList(fixture.value, fixture.transcriptId), { collectives: [] }, []))
      .toThrowError(fixture.expectedError);
  });

  for (const fixture of fixtures.sessions) {
    it(fixture.name, () => {
      renderSession(sessionDetail(fixture.turnsState, fixture.stopReasons));
      expect(document.querySelectorAll(".txn-turn")).toHaveLength(fixture.expectedTurnCount);
      if (fixture.expectedText != null) {
        expect(screen.getAllByText(fixture.expectedText, { exact: false }).length).toBeGreaterThan(0);
      }
    });
  }
});
