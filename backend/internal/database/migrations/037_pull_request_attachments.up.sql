-- The pull request prompt-attachment lifecycle.
--
-- One row per observed pull request. This migration makes the attachment
-- state machine REPRESENTABLE: the closed state menu, the transition
-- timestamps, the digest, the GitHub object ids the posting path later fills
-- in, and the transcripts an attachment holds together with the visibility
-- each one had before it was widened. It does NOT match transcripts, compute
-- a digest, post to GitHub, or move any transcript's visibility - those are
-- separate work. The only statement that changes an EXISTING attachment's
-- state is the Go transition function (`internal/promptattach.Transition`),
-- which enforces the closed transition table; creating an attachment writes the
-- initial state directly.
--
-- `state` is CHECK-constrained to the closed menu, mirrored by the Go
-- `promptattach.State` constants so a value that round-trips through the
-- database is always one of them (the `session_origin` pattern, migration 033).
CREATE TABLE pull_request_attachments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repo_owner          TEXT NOT NULL,
    repo_name           TEXT NOT NULL,
    github_repo_id      BIGINT NOT NULL,   -- GitHub's stable repository id
    number              INTEGER NOT NULL,  -- pull request number within the repo
    head_sha            TEXT NOT NULL,     -- the head commit the attachment is scoped to
    base_remote         TEXT NOT NULL,     -- normalized remote of the PR base repository
    head_remote         TEXT NOT NULL,     -- normalized remote of the PR head repository (differs for a fork)
    author_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- A requester may have no Village account at all, so this is the numeric
    -- GitHub id, not a users FK. NULL means the request came from a source with
    -- no GitHub identity recorded.
    requester_github_id BIGINT,
    state               TEXT NOT NULL DEFAULT 'requested',
    comment_id          BIGINT,            -- GitHub sticky-comment id once posted
    check_run_id        BIGINT,            -- GitHub check-run id once posted
    digest              JSONB,             -- schema.PromptDigest, once computed
    -- One nullable timestamp per state. The transition function stamps the
    -- target state's column on every move so the lifecycle timeline is exact.
    requested_at        TIMESTAMPTZ,
    waiting_at          TIMESTAMPTZ,
    preview_at          TIMESTAMPTZ,
    attached_at         TIMESTAMPTZ,
    detached_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- One attachment per pull request: exactly one sticky comment, edited in
    -- place, is a product invariant.
    UNIQUE (github_repo_id, number),
    CONSTRAINT pull_request_attachments_state_menu
        CHECK (state IN ('requested', 'waiting', 'preview', 'attached', 'detached'))
);

-- The prompt-request list reads an author's waiting attachments.
CREATE INDEX idx_pull_request_attachments_author_state
    ON pull_request_attachments (author_id, state);
-- Repository lookup for the webhook and publish-completion paths.
CREATE INDEX idx_pull_request_attachments_repo
    ON pull_request_attachments (lower(repo_owner), lower(repo_name));

-- The transcripts an attachment holds, ordered by session start. `previous_visibility`
-- is the transcripts.visibility value read before an attach widened it, so detach
-- can restore EXACTLY that value rather than a default. It is deliberately not
-- CHECK-constrained here: it is a copy of a value already constrained on
-- transcripts.visibility, and a third copy of that menu is the drift AGENTS.md
-- warns about, not an extra guarantee.
CREATE TABLE pull_request_attachment_transcripts (
    attachment_id       UUID NOT NULL REFERENCES pull_request_attachments(id) ON DELETE CASCADE,
    transcript_id       UUID NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    position            INTEGER NOT NULL CHECK (position >= 0),
    previous_visibility TEXT NOT NULL,
    PRIMARY KEY (attachment_id, transcript_id),
    UNIQUE (attachment_id, position)
);

-- The per-user preview preference the attach path reads: when true, every
-- attach enters `preview` for the author to confirm, on any repository.
ALTER TABLE users
    ADD COLUMN preview_before_attach BOOLEAN NOT NULL DEFAULT false;

-- Per-collective check settings the posting path reads: whether the
-- "peasant / prompts" check is posted at all, and whether it is informational
-- or required.
ALTER TABLE groups
    ADD COLUMN post_prompts_check BOOLEAN NOT NULL DEFAULT true;

ALTER TABLE groups
    ADD COLUMN prompts_check_mode TEXT NOT NULL DEFAULT 'informational',
    ADD CONSTRAINT groups_prompts_check_mode_menu
        CHECK (prompts_check_mode IN ('informational', 'required'));
