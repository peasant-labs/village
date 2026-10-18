-- A user-scoped issuance fence survives empty organization snapshots. It is
-- server-private state, not a permission, timestamp or externally served field.
ALTER TABLE users ADD COLUMN github_org_sync_generation BIGINT NOT NULL DEFAULT 0
    CONSTRAINT users_github_org_sync_generation_nonnegative CHECK (github_org_sync_generation >= 0);
