-- v2/v3 session metrics from local analytics pipeline
ALTER TABLE transcripts
    ADD COLUMN tokens_in BIGINT,
    ADD COLUMN tokens_out BIGINT,
    ADD COLUMN title_generated TEXT,
    ADD COLUMN outcome VARCHAR(20),
    ADD COLUMN files_touched INTEGER,
    ADD COLUMN lines_changed INTEGER,
    ADD COLUMN retry_loops INTEGER,
    ADD COLUMN retry_tokens_wasted INTEGER,
    ADD COLUMN within_session_reverts INTEGER,
    ADD COLUMN signal_density REAL,
    ADD COLUMN spec_quality_score REAL,
    ADD COLUMN exploration_ratio REAL,
    ADD COLUMN scope_breadth INTEGER,
    ADD COLUMN discovery_turns INTEGER,
    ADD COLUMN m2_token_outcome_ratio REAL,
    ADD COLUMN m3_unique_tool_count INTEGER,
    ADD COLUMN m4_error_recovery_count INTEGER,
    ADD COLUMN m4_consecutive_error_max INTEGER,
    ADD COLUMN m5_context_utilization_pct REAL,
    ADD COLUMN m5_peak_context_tokens INTEGER,
    ADD COLUMN m5_avg_message_tokens INTEGER,
    ADD COLUMN m6_output_survival_pct REAL,
    ADD COLUMN m6_lines_survived INTEGER,
    ADD COLUMN m6_lines_total INTEGER,
    ADD COLUMN m7_spec_word_count INTEGER,
    ADD COLUMN m7_spec_has_examples BOOLEAN,
    ADD COLUMN m7_spec_has_constraints BOOLEAN,
    ADD COLUMN computed_at TIMESTAMPTZ,
    ADD COLUMN compute_version INTEGER;

CREATE INDEX idx_transcripts_outcome ON transcripts(outcome);
CREATE INDEX idx_transcripts_signal_density ON transcripts(signal_density);
