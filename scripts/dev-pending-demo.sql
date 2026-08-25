-- dev-pending-demo.sql
--
-- One-off helper for testing the curated-collective approve/reject review
-- workflow as YOURSELF (the OAuth-created dev user) in your own collective.
--
-- The main scripts/seed.sql only inserts pending shares into alice-dev's
-- seeded "Curated Showcase" collective. This script lands a single PENDING
-- submission awaiting review into YOUR collective so a "review" row shows up on
-- /groups/{id}.
--
-- Run (Docker-compose dev setup, matches `make seed`):
--   docker exec -i $(docker compose ps -q postgres) psql -U peasant -d peasant \
--     < scripts/dev-pending-demo.sql
--
-- Or directly against a local postgres:
--   psql -d peasant -f scripts/dev-pending-demo.sql
--
-- Prereq: you must have run `make seed` first, otherwise the transcript FK
-- below will fail (the transcript c0000000-...-000000000001 is created in
-- seed.sql).

-- Edit these two if you want to target a different collective / transcript:
\set collective_id '315ba58d-45d2-4df9-a0e6-c8b80c25a5ae'
\set transcript_id 'c0000000-0000-0000-0000-000000000001'

\echo ''
\echo '>>> If you hit a foreign-key error on the attempt transcript_id,'
\echo '>>> run `make seed` first to populate the seed transcripts.'
\echo ''

-- 1. Flip the collective to curated so future contributions also auto-go to
--    pending. Not strictly required (we INSERT with status='pending' below),
--    but it makes the surrounding UX match what you want to test.
UPDATE groups
SET acceptance_mode = 'curated'
WHERE id = :'collective_id'::uuid
  AND acceptance_mode <> 'curated';

-- 2. Open ONE submission awaiting review in your collective.
--    transcript_shares is derived by a database trigger from the attempt
--    history and refuses a direct write, so this opens the attempt and the
--    derivation produces the row the review page reads.
INSERT INTO transcript_share_attempts (transcript_id, group_id, attempt_no, status)
VALUES (
    :'transcript_id'::uuid,
    :'collective_id'::uuid,
    1,
    'pending'
)
ON CONFLICT DO NOTHING;

-- 2b. Ensure the transcript's owner is a contributor member of the collective
--     too — otherwise the Contributors sidebar would show them while the
--     Members list wouldn't, which is confusing in a seeded demo.
INSERT INTO group_members (group_id, user_id, role)
SELECT :'collective_id'::uuid, t.owner_id, 'contributor'
FROM transcripts t
WHERE t.id = :'transcript_id'::uuid
ON CONFLICT DO NOTHING;

-- 3. Confirmation: should print one row showing the pending share you just
--    landed (or the pre-existing one if you re-ran this script).
SELECT
    t.title              AS transcript_title,
    ts.status            AS share_status,
    g.name               AS collective_name,
    g.acceptance_mode    AS collective_mode,
    ts.shared_at
FROM transcript_shares ts
JOIN transcripts t ON t.id = ts.transcript_id
JOIN groups g      ON g.id = ts.group_id
WHERE ts.group_id     = :'collective_id'::uuid
  AND ts.transcript_id = :'transcript_id'::uuid;
