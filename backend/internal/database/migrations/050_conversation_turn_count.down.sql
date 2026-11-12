-- Forward-only in any shared environment: use this only on a development
-- database. It discards every published conversation turn count.
ALTER TABLE transcripts
    DROP COLUMN displayed_turn_count,
    DROP COLUMN conversation_turn_count;
