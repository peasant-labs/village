-- Reverses migration 037 only. Pull request attachments and the settings it
-- introduced are new state, so the down path drops them; there is no earlier
-- design to restore.
DROP TABLE IF EXISTS pull_request_attachment_transcripts;
DROP TABLE IF EXISTS pull_request_attachments;

ALTER TABLE groups DROP CONSTRAINT IF EXISTS groups_prompts_check_mode_menu;
ALTER TABLE groups DROP COLUMN IF EXISTS prompts_check_mode;
ALTER TABLE groups DROP COLUMN IF EXISTS post_prompts_check;

ALTER TABLE users DROP COLUMN IF EXISTS preview_before_attach;
