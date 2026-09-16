-- Tie an attachment to the collective that enabled it.
--
-- An attachment needs three facts that live on `collective_repositories`: the
-- collective that linked the repository (whose owner opted in, so its members
-- may see the prompts), the App installation to post through, and whether the
-- repository is private. Only the first is stored here. The linking collective
-- is the attachment's own fact, while the installation and the repository's
-- privacy are refreshed by every re-link (`LinkCollectiveRepository` upserts
-- both), so reading them through the link keeps them current instead of
-- freezing a value that a reinstall or a visibility change would invalidate.
--
-- Nullable on purpose, and `ON DELETE SET NULL` rather than CASCADE. A
-- collective can be deleted (`DELETE /groups/{id}`) and its repository links
-- cascade with it. A CASCADE here would take the attachment's transcripts with
-- it and lose the visibility each one held before an attach widened it, leaving
-- those transcripts shared with a collective that no longer exists and no
-- record left to restore them. SET NULL keeps the snapshot, so a detach still
-- restores exactly what was recorded; an attachment whose collective is gone
-- cannot post, and the lifecycle fails closed rather than guessing.
--
-- No backfill is needed: no caller created an attachment row before the change
-- that lands the lifecycle handlers, so a pre-existing row (there is none in an
-- environment that never ran unreleased code) correctly starts unbound.

ALTER TABLE pull_request_attachments
    ADD COLUMN group_id UUID REFERENCES groups(id) ON DELETE SET NULL;
