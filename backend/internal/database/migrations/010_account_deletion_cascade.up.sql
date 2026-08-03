-- Account deletion cascade: transcripts.owner_id and groups.created_by
-- reference users(id) without ON DELETE CASCADE, so deleting a user fails
-- FK constraints. Re-create those two FKs WITH ON DELETE CASCADE so that
-- deleting a user also removes their transcripts and the groups they created.
-- All other user-referencing tables already cascade.

ALTER TABLE transcripts DROP CONSTRAINT transcripts_owner_id_fkey;
ALTER TABLE transcripts ADD CONSTRAINT transcripts_owner_id_fkey
    FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE groups DROP CONSTRAINT groups_created_by_fkey;
ALTER TABLE groups ADD CONSTRAINT groups_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE CASCADE;
