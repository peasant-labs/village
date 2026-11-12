-- The conversation turn count is a query projection of the validated
-- publication. NULL means the publishing client did not measure it; zero means
-- it measured none. Nothing is reconstructed from turn_count.
--
-- displayed_turn_count is the one expression for the number a reader is shown,
-- summed and sorted by: the conversation count when measured, else the stored
-- turn_count. turn_count has no range check, so a negative stored value shows
-- as zero: the displayed number is never negative. The clamp is a CASE and not
-- GREATEST(..., 0), which ignores NULL and would show zero for a row that
-- stores neither count. It is generated, so no writer can set it.
ALTER TABLE transcripts
    ADD COLUMN conversation_turn_count BIGINT,
    ADD CONSTRAINT transcripts_conversation_turn_count_range
        CHECK (conversation_turn_count BETWEEN 0 AND 9007199254740991),
    ADD COLUMN displayed_turn_count BIGINT
        GENERATED ALWAYS AS (COALESCE(conversation_turn_count, CASE WHEN turn_count < 0 THEN 0 ELSE turn_count::bigint END)) STORED;
