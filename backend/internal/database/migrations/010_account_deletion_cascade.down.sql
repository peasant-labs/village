-- Reverse 010: re-create the FK constraints without ON DELETE CASCADE.

ALTER TABLE transcripts DROP CONSTRAINT transcripts_owner_id_fkey;
ALTER TABLE transcripts ADD CONSTRAINT transcripts_owner_id_fkey
    FOREIGN KEY (owner_id) REFERENCES users(id);

ALTER TABLE groups DROP CONSTRAINT groups_created_by_fkey;
ALTER TABLE groups ADD CONSTRAINT groups_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES users(id);
