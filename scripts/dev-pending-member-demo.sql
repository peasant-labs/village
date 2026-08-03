-- dev-pending-member-demo.sql
--
-- One-off helper for testing the "Pending member requests" UI on the group
-- detail page. Inserts a single group_members row with role='pending' so an
-- owner of the target collective sees a request to approve / reject.
--
-- Prereqs:
--   1. Migration 013_group_member_pending must be applied (widens the role
--      CHECK constraint to allow 'pending'). It runs automatically on backend
--      startup; you can also force it via `make migrate` or by restarting the
--      backend container.
--   2. `make seed` must have been run first so the target collective and the
--      pending user exist (the user_id below is seed.sql's bob-ai).
--
-- Run (Docker-compose dev setup, matches `make seed`):
--   docker exec -i $(docker compose ps -q postgres) psql -U peasant -d peasant \
--     < scripts/dev-pending-member-demo.sql
--
-- Or directly against a local postgres:
--   psql -d peasant -f scripts/dev-pending-member-demo.sql

-- Edit these two if you want to target a different collective / user:
\set collective_id '315ba58d-45d2-4df9-a0e6-c8b80c25a5ae'
\set pending_user_id 'a0000000-0000-0000-0000-000000000002'

\echo ''
\echo '>>> If you hit a foreign-key error on group_members, run `make seed` first.'
\echo '>>> If you hit a CHECK constraint error on role, ensure migration 013 has applied.'
\echo ''

-- Insert one pending member request. PK is (group_id, user_id), so re-running
-- is idempotent.
INSERT INTO group_members (group_id, user_id, role)
VALUES (
    :'collective_id'::uuid,
    :'pending_user_id'::uuid,
    'pending'
)
ON CONFLICT (group_id, user_id) DO NOTHING;

-- Confirmation: should print one row showing the pending request.
SELECT
    g.name              AS collective_name,
    u.github_username   AS pending_user,
    gm.role             AS member_role,
    gm.joined_at        AS requested_at
FROM group_members gm
JOIN groups g ON g.id = gm.group_id
JOIN users u  ON u.id = gm.user_id
WHERE gm.group_id = :'collective_id'::uuid
  AND gm.user_id  = :'pending_user_id'::uuid;
