-- ⚠️ DO-NOT-TOUCH (TRAP — NOT the harness wire key): this `provider` column is
-- the user's OAuth AUTH IDENTITY (github/gitlab/huggingface/codeberg/sourcehut).
-- It is UNRELATED to the transcript harness (claude-code/gemini-cli/...) and must
-- NEVER be flipped/renamed to `harness` during the push-wire provider→harness
-- changeover. The harness wire key lives on transcripts.model_provider /
-- SessionDetailPayload.harness — a different concern entirely. See the epoch
-- TRAP note (peasant B4 ast-grep no-trap-harness-flip guard).
--
-- Multi-provider OAuth: identify users by (provider, provider_user_id).
-- Existing GitHub users get provider='github' and provider_user_id=github_id::text.
-- Non-GitHub users store the real external ID in provider_user_id, and a
-- synthesized stable BIGINT (sha256-derived, sign bit cleared) in github_id —
-- this keeps github_id NOT NULL + UNIQUE intact without changing the wider
-- schema. github_id remains the canonical surrogate for GitHub-aware code
-- paths (org affiliations, attestations) and is unused for non-GitHub users.

ALTER TABLE users
    ADD COLUMN provider VARCHAR(32) NOT NULL DEFAULT 'github'
        CHECK (provider IN ('github', 'gitlab', 'huggingface', 'codeberg', 'sourcehut')),
    ADD COLUMN provider_user_id TEXT;

UPDATE users
SET provider_user_id = github_id::text
WHERE provider_user_id IS NULL;

ALTER TABLE users
    ALTER COLUMN provider_user_id SET NOT NULL;

CREATE UNIQUE INDEX idx_users_provider_provider_user_id
    ON users (provider, provider_user_id);
