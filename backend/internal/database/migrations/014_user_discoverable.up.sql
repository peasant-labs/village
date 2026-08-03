-- Private-mode toggle for users. When false:
--   1. The user is hidden from collective member and contributor lists.
--   2. Their transcripts are attributed to "anon" instead of their handle.
ALTER TABLE users
    ADD COLUMN is_discoverable BOOLEAN NOT NULL DEFAULT TRUE;
