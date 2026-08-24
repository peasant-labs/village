-- Records who drove a published session, so discovery can group sessions that
-- no human prompted in-band away from a publisher's root-level list.
--
-- 'user'    at least one real user turn carries prompt content.
-- 'agent'   no user turn at all, yet assistant or tool work exists.
-- 'unknown' neither could be established (system-only content, or content that
--           could not be read). Fail-safe: 'unknown' is displayed exactly like
--           'user' and is never demoted.
--
-- The DEFAULT makes every historical row 'unknown', so no existing transcript
-- disappears from a list before the operator runs the origin backfill.
ALTER TABLE transcripts
    ADD COLUMN session_origin TEXT NOT NULL DEFAULT 'unknown';

ALTER TABLE transcripts
    ADD CONSTRAINT transcripts_session_origin_menu
    CHECK (session_origin IN ('user', 'agent', 'unknown'));
