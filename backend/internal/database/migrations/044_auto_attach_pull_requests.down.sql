-- Forward-only in any shared environment: use this only on a development
-- database.
ALTER TABLE users DROP COLUMN IF EXISTS auto_attach_pull_requests;
