-- Automatic pull request linking is the product's default: a transcript that
-- matches a pull request the author opened attaches without a per-PR click, and
-- the collective repository link remains the consent boundary that decides which
-- repositories are watched. Existing stored choices are left exactly as they
-- are; this changes what a new account starts with.
ALTER TABLE users
    ALTER COLUMN auto_attach_pull_requests SET DEFAULT true;
