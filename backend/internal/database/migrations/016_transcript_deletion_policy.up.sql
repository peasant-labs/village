-- Per-collective policy controlling what happens to a leaving member's
-- shared transcripts. 'user_choice' lets the leaving member decide; 'mandatory'
-- auto-retracts all of the leaving member's contributions on departure.
ALTER TABLE groups
    ADD COLUMN transcript_deletion_policy TEXT NOT NULL DEFAULT 'user_choice'
        CHECK (transcript_deletion_policy IN ('user_choice', 'mandatory'));
