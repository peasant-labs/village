-- Supplemental seed for the dev-feedback features:
--   discoverability / anon attribution, multi-provider auth, owner-sees-anon,
--   transcript-deletion policy, leave+retract, "your contributions", join consent,
--   GitHub-org member filter.
--
-- Designed to be layered on top of scripts/seed.sql and re-runnable (idempotent).
-- It wires data onto the REAL logged-in account `vitorhw` so owner-side flows
-- can be exercised directly. If you log in under a different handle, change the
-- two refs below.

\set vitor_handle '''vitorhw'''
SELECT set_config('app.actor_id', '00000000-0000-0000-0000-000000000000', false);

-- ── New mock users ──────────────────────────────────────────────────────────
-- dana/gina are ANON (is_discoverable=false). erin/frank/iris exercise the new
-- OAuth providers so provider variety shows up in member lists.
INSERT INTO users (id, github_id, github_username, display_name, avatar_url, is_discoverable, provider, provider_user_id) VALUES
    ('f0000000-0000-0000-0000-000000000001', 2001, 'dana-anon',     'Dana (anon)',   'https://avatars.githubusercontent.com/u/2001', false, 'github',      '2001'),
    ('f0000000-0000-0000-0000-000000000002', 2002, 'gina-anon',     'Gina (anon)',   'https://avatars.githubusercontent.com/u/2002', false, 'github',      '2002'),
    ('f0000000-0000-0000-0000-000000000003', 2003, 'erin-gitlab',   'Erin (GitLab)', 'https://avatars.githubusercontent.com/u/2003', true,  'gitlab',      'gl-3001'),
    ('f0000000-0000-0000-0000-000000000004', 2004, 'frank-hf',      'Frank (HF)',    'https://avatars.githubusercontent.com/u/2004', true,  'huggingface', 'hf-3002'),
    ('f0000000-0000-0000-0000-000000000005', 2005, 'iris-codeberg', 'Iris (Codeberg)','https://avatars.githubusercontent.com/u/2005', true, 'codeberg',    'cb-3003')
ON CONFLICT (github_id) DO NOTHING;

-- Transcript rows and bodies are created by the server's encrypted privacy
-- seed mode before these relationship fixtures are applied.

-- ── GitHub orgs (drives the "Scope to org" member filter) ────────────────────
-- The filter dropdown is built from the VIEWER's visible orgs, then matches
-- members whose visible orgs overlap. Give vitorhw two orgs, and place the new
-- members into them.
INSERT INTO user_github_orgs (user_id, org_login, org_id, avatar_url, visible)
SELECT id, v.org_login, v.org_id, v.avatar_url, true
FROM users
CROSS JOIN (VALUES
    ('anthropic-labs', 90001, 'https://avatars.githubusercontent.com/u/90001'),
    ('data-collective', 90003, 'https://avatars.githubusercontent.com/u/90003')
) AS v(org_login, org_id, avatar_url)
WHERE github_username = :vitor_handle
ON CONFLICT (user_id, org_login) DO NOTHING;

INSERT INTO user_github_orgs (user_id, org_login, org_id, avatar_url, visible) VALUES
    ('f0000000-0000-0000-0000-000000000001', 'anthropic-labs',  90001, 'https://avatars.githubusercontent.com/u/90001', true),
    ('f0000000-0000-0000-0000-000000000001', 'data-collective', 90003, 'https://avatars.githubusercontent.com/u/90003', true),
    ('f0000000-0000-0000-0000-000000000003', 'anthropic-labs',  90001, 'https://avatars.githubusercontent.com/u/90001', true),
    ('f0000000-0000-0000-0000-000000000004', 'data-collective', 90003, 'https://avatars.githubusercontent.com/u/90003', true)
ON CONFLICT (user_id, org_login) DO NOTHING;

-- ── Enrich vitorhw's OWN collective "AI research collective" (curated) ───────
-- Goal: as owner you should see anon members + an anon pending member, and an
-- anon contributor + an anon PENDING share with their REAL handle (others see "anon").
INSERT INTO group_members (group_id, user_id, role)
SELECT g.id, v.user_id::uuid, v.role
FROM groups g
CROSS JOIN (VALUES
    ('f0000000-0000-0000-0000-000000000001', 'member'),   -- dana-anon (anon member)
    ('f0000000-0000-0000-0000-000000000003', 'member'),   -- erin-gitlab
    ('f0000000-0000-0000-0000-000000000004', 'member'),   -- frank-hf
    ('f0000000-0000-0000-0000-000000000005', 'member'),   -- iris-codeberg
    ('f0000000-0000-0000-0000-000000000002', 'pending')   -- gina-anon (anon, awaiting approval)
) AS v(user_id, role)
WHERE g.name = 'AI research collective'
ON CONFLICT DO NOTHING;

-- Contributions to vitorhw's collective: an accepted anonymous contributor and
-- an anonymous submission still awaiting review.
--
-- transcript_shares is derived by a database trigger from the attempt history
-- and refuses a direct write, so the seed opens attempts and lets the
-- derivation produce the current-state rows.
INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
SELECT v.transcript_id::uuid, g.id, 1, v.status
FROM groups g
CROSS JOIN (VALUES
    ('c1000000-0000-0000-0000-000000000001', 'approved'), -- dana anon -> approved (contributor)
    ('c1000000-0000-0000-0000-000000000002', 'pending'),  -- dana anon -> PENDING review
    ('c1000000-0000-0000-0000-000000000003', 'approved')  -- erin gitlab -> approved
) AS v(transcript_id, status)
WHERE g.name = 'AI research collective'
ON CONFLICT DO NOTHING;

UPDATE transcripts SET visibility = 'shared'
WHERE id IN ('c1000000-0000-0000-0000-000000000001', 'c1000000-0000-0000-0000-000000000003');

-- ── Deletion-policy + leave/retract + "your contributions" ───────────────────
-- AI Research Team -> MANDATORY: vitorhw is a contributor; share one of their
-- transcripts so leaving shows the locked-in mandatory retraction notice.
UPDATE groups SET transcript_deletion_policy = 'mandatory' WHERE name = 'AI Research Team';

-- Verified Contributors -> user_choice (default): add vitorhw as a member and
-- share their other transcript so leaving shows the optional retract checkbox.
INSERT INTO group_members (group_id, user_id, role)
SELECT g.id, (SELECT id FROM users WHERE github_username = :vitor_handle), 'member'
FROM groups g
JOIN users u ON u.github_username = :vitor_handle
WHERE g.name = 'Verified Contributors'
ON CONFLICT DO NOTHING;

INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
SELECT v.transcript_id::uuid, g.id, 1, 'approved'
FROM (VALUES
    ('AI Research Team',      'a77d3338-0b48-4b0d-9e84-7f65923d3612'),
    ('Verified Contributors', '777fa7a5-e856-451f-8ec5-fe9f93330f48')
) AS v(group_name, transcript_id)
JOIN groups g ON g.name = v.group_name
JOIN transcripts t ON t.id = v.transcript_id::uuid
JOIN users u ON u.github_username = :vitor_handle AND t.owner_id = u.id
ON CONFLICT DO NOTHING;

UPDATE transcripts SET visibility = 'shared'
WHERE id IN ('a77d3338-0b48-4b0d-9e84-7f65923d3612', '777fa7a5-e856-451f-8ec5-fe9f93330f48');

-- ── Join-consent test target ─────────────────────────────────────────────────
-- An OPEN collective vitorhw is NOT a member of, so the anon join-consent dialog
-- can be triggered by clicking "Join as Contributor".
INSERT INTO groups (id, name, description, created_by, acceptance_mode, transcript_deletion_policy) VALUES
    ('d1000000-0000-0000-0000-000000000001', 'Open Sandbox', 'Open collective for testing the anon join-consent flow', 'a0000000-0000-0000-0000-000000000001', 'open', 'user_choice')
ON CONFLICT DO NOTHING;

INSERT INTO group_members (group_id, user_id, role) VALUES
    ('d1000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000001', 'owner'),
    ('d1000000-0000-0000-0000-000000000001', 'f0000000-0000-0000-0000-000000000003', 'member')
ON CONFLICT DO NOTHING;
