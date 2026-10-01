-- Forward-only in any shared environment: re-running the up file after this
-- down marks EVERY binding attach_widened again, including bindings made since
-- 044, and a later detach would then narrow transcripts their owners widened by
-- hand. Use this only on a development database.
ALTER TABLE users DROP COLUMN IF EXISTS auto_attach_pull_requests;
ALTER TABLE pull_request_attachment_transcripts DROP COLUMN IF EXISTS attach_widened;
