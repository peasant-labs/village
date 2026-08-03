-- 023: Phase-A HOTFIX — storage normalization of legacy harness VALUES.
--
-- Pre-merge the CLI emitted harness identifiers as `claude` / `gemini`; the
-- post-merge schema canonicalizes these to `claude-code` / `gemini-cli`. Rows
-- published before the hotfix carry the legacy values in
-- transcripts.model_provider. This migration backfills them to the canonical
-- identifiers so existing rows read consistently with newly-published ones.
--
-- A3 DISTINCT FLOORS: this is the STORAGE-normalization floor. It rewrites the
-- stored model_provider VALUE only. It is NOT the Phase-B display
-- migrate-on-read floor (which normalizes legacy content-blob SHAPES), and it
-- does not touch content blobs, the column name (`model_provider` is retained
-- in Phase A), or idx_transcripts_provider.
--
-- IDEMPOTENT: each UPDATE matches only the legacy value, so an already-canonical
-- row (`claude-code`, `gemini-cli`) is skipped and re-running is a no-op.
-- Non-legacy values (`opencode`, `codex`, `cursor`, `antigravity`, ...) are left
-- untouched.
UPDATE transcripts SET model_provider = 'claude-code' WHERE model_provider = 'claude';
UPDATE transcripts SET model_provider = 'gemini-cli'  WHERE model_provider = 'gemini';
