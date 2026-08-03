-- Down for 023: revert the canonical harness VALUES to their legacy form.
--
-- NOTE: this reverse mapping is necessarily LOSSY. After the up migration ran,
-- backfilled rows and genuinely-new post-hotfix rows both store `claude-code` /
-- `gemini-cli` and are indistinguishable, so this down reverts ALL canonical
-- rows to the legacy value — including ones that were never legacy. It exists
-- for migration symmetry / local rollback, not as a precise inverse.
UPDATE transcripts SET model_provider = 'claude' WHERE model_provider = 'claude-code';
UPDATE transcripts SET model_provider = 'gemini' WHERE model_provider = 'gemini-cli';
