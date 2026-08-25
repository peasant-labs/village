DROP TRIGGER IF EXISTS trg_share_attempt_immutable ON transcript_share_attempts;
DROP TRIGGER IF EXISTS trg_transcript_shares_fail_closed ON transcript_shares;
DROP TRIGGER IF EXISTS trg_derive_transcript_share ON transcript_share_attempts;
DROP FUNCTION IF EXISTS guard_share_attempt_immutable();
DROP FUNCTION IF EXISTS guard_transcript_shares_writer();
DROP FUNCTION IF EXISTS derive_transcript_share();
DROP INDEX IF EXISTS idx_share_attempts_group_status;
DROP INDEX IF EXISTS uq_share_attempt_open;
DROP TABLE IF EXISTS transcript_share_attempts;
