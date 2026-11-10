import type { ProjectHash, SessionID, TranscriptID } from "@peasant-labs/schema";
import type { Transcript, TranscriptListItem, TranscriptListResponse } from "@/lib/types";

/**
 * One complete `Transcript` wire row, with every field the contract carries
 * filled in, so a fixture only has to state the handful of fields its case is
 * actually about.
 *
 * The wire type has more than fifty columns, most of them optional analytics.
 * Spelling them out per fixture file made each new mounted-route fixture a
 * fifty-line copy that drifted from the contract the moment a column changed;
 * this is the one place a new column has to be added.
 */
export function makeTranscriptFixture(overrides: Partial<Transcript> = {}): Transcript {
  return {
    id: "transcript-0",
    local_id: "local-0",
    title: "fixture session",
    description: null,
    visibility: "public",
    model_provider: "claude-code",
    model_name: "claude-fable-5",
    harness_version: null,
    session_start: "2026-08-20T09:00:00Z",
    session_end: "2026-08-20T09:30:00Z",
    turn_count: 12,
    token_count: 900,
    blob_size_bytes: null,
    schema_version: "0.14.0",
    published_at: "2026-08-20T10:00:00Z",
    updated_at: "2026-08-20T10:00:00Z",
    parent_session_id: null,
    parent_transcript_id: null,
    ingested_at: null,
    source_format: null,
    git_branch: null,
    git_remote: null,
    project_hash: "0".repeat(64),
    project_name: "fixture project",
    project_display_name: "fixture project",
    project_name_source: "consented",
    project_remote_label: "",
    tool_call_count: null,
    subagent_count: null,
    duration_ms: null,
    tokens_in: null,
    tokens_out: null,
    subagents: null,
    diagnostics_warnings: null,
    diagnostics_partial: null,
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
    ...overrides,
  };
}


/** One transcript list row as the contract types it. The app's `Transcript` is
 *  wider in exactly one closed field (`license_id`), so fixtures cross into the
 *  contract's row type here; `owner_orgs` is the contract field the app's view
 *  row does not carry. */
export type TranscriptListRow = TranscriptListResponse["transcripts"][number];

/**
 * The contract brands three string identities (`TranscriptID`, `SessionID`,
 * `ProjectHash`); the fixture keeps readable names and the same strings at
 * runtime. This states each brand once, at the one boundary where a fixture row
 * crosses into the contract's row type, so every other field of the row is
 * still checked by the compiler rather than an `as unknown as` cast.
 */
function wireTranscript(row: Transcript): TranscriptListRow["transcript"] {
  return {
    ...row,
    id: row.id as TranscriptID,
    local_id: row.local_id as SessionID,
    parent_session_id: row.parent_session_id as SessionID | null,
    parent_transcript_id: row.parent_transcript_id as TranscriptID | null,
    project_hash: row.project_hash as ProjectHash | null,
  };
}

export function asTranscriptListRow(row: TranscriptListItem): TranscriptListRow {
  return {
    transcript: wireTranscript(row.transcript),
    tags: row.tags,
    owner: row.owner,
    owner_orgs: [],
    shares: row.shares ?? null,
    attestations: row.attestations ?? null,
    pull_requests: row.pull_requests ?? { count: 0, recent: [] },
  };
}
