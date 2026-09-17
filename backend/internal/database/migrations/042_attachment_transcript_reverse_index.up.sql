-- The visibility-change trigger reads the attachments that bind ONE transcript,
-- to stop advertising prompts whose owner has since made them less visible.
-- The binding table's primary key is (attachment_id, transcript_id), so that
-- lookup has no leading column to use; this index gives it one.
CREATE INDEX idx_pull_request_attachment_transcripts_transcript
    ON pull_request_attachment_transcripts (transcript_id);
