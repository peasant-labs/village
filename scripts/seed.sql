-- Seed data for development
--
-- SYSTEM ACTOR, SESSION-SCOPED: the migration-026 governance-audit triggers are
-- FAIL-CLOSED — every INSERT / governance-axis UPDATE / DELETE on transcripts
-- must declare app.actor_id or the statement aborts. `make seed` pipes this file
-- through autocommit psql, where SET LOCAL is a no-op (each statement is its own
-- transaction), so the actor is declared SESSION-scoped (is_local = false) and
-- covers every transcript INSERT/UPDATE below. Seeded governance events attribute
-- to the reserved system actor (docs/deletion-data-lifecycle-model.md §7).
SELECT set_config('app.actor_id', '00000000-0000-0000-0000-000000000000', false);

-- Test users
INSERT INTO users (id, github_id, github_username, display_name, avatar_url, provider, provider_user_id) VALUES
    ('a0000000-0000-0000-0000-000000000001', 1001, 'alice-dev', 'Alice Developer', 'https://avatars.githubusercontent.com/u/1001', 'github', '1001'),
    ('a0000000-0000-0000-0000-000000000002', 1002, 'bob-ai', 'Bob AI', 'https://avatars.githubusercontent.com/u/1002', 'github', '1002'),
    ('a0000000-0000-0000-0000-000000000003', 1003, 'charlie-ml', 'Charlie ML', 'https://avatars.githubusercontent.com/u/1003', 'github', '1003')
ON CONFLICT (github_id) DO NOTHING;

-- Tags
INSERT INTO tags (id, name) VALUES
    ('b0000000-0000-0000-0000-000000000001', 'debugging'),
    ('b0000000-0000-0000-0000-000000000002', 'greenfield'),
    ('b0000000-0000-0000-0000-000000000003', 'refactoring'),
    ('b0000000-0000-0000-0000-000000000004', 'claude-code'),
    ('b0000000-0000-0000-0000-000000000005', 'gemini-cli'),
    ('b0000000-0000-0000-0000-000000000006', 'multi-agent'),
    ('b0000000-0000-0000-0000-000000000007', 'iterative-refinement')
ON CONFLICT (name) DO NOTHING;

-- Transcript rows and bodies are created by the server's encrypted core seed
-- mode before these relationship fixtures are applied.

-- Tag links
INSERT INTO transcript_tags (transcript_id, tag_id) VALUES
    ('c0000000-0000-0000-0000-000000000001', 'b0000000-0000-0000-0000-000000000001'),
    ('c0000000-0000-0000-0000-000000000001', 'b0000000-0000-0000-0000-000000000004'),
    ('c0000000-0000-0000-0000-000000000002', 'b0000000-0000-0000-0000-000000000002'),
    ('c0000000-0000-0000-0000-000000000002', 'b0000000-0000-0000-0000-000000000004'),
    ('c0000000-0000-0000-0000-000000000003', 'b0000000-0000-0000-0000-000000000003'),
    ('c0000000-0000-0000-0000-000000000003', 'b0000000-0000-0000-0000-000000000005'),
    ('c0000000-0000-0000-0000-000000000004', 'b0000000-0000-0000-0000-000000000001'),
    ('c0000000-0000-0000-0000-000000000004', 'b0000000-0000-0000-0000-000000000006'),
    ('c0000000-0000-0000-0000-000000000005', 'b0000000-0000-0000-0000-000000000002'),
    ('c0000000-0000-0000-0000-000000000005', 'b0000000-0000-0000-0000-000000000007')
ON CONFLICT DO NOTHING;

-- GitHub org memberships
-- alice: anthropic-labs (visible), data-collective (visible)
-- bob:   anthropic-labs (visible), openai-research (hidden)
-- charlie: openai-research (visible), data-collective (visible)
INSERT INTO user_github_orgs (user_id, org_login, org_id, avatar_url, visible) VALUES
    ('a0000000-0000-0000-0000-000000000001', 'anthropic-labs', 90001, 'https://avatars.githubusercontent.com/u/90001', true),
    ('a0000000-0000-0000-0000-000000000001', 'data-collective', 90003, 'https://avatars.githubusercontent.com/u/90003', true),
    ('a0000000-0000-0000-0000-000000000002', 'anthropic-labs', 90001, 'https://avatars.githubusercontent.com/u/90001', true),
    ('a0000000-0000-0000-0000-000000000002', 'openai-research', 90002, 'https://avatars.githubusercontent.com/u/90002', false),
    ('a0000000-0000-0000-0000-000000000003', 'openai-research', 90002, 'https://avatars.githubusercontent.com/u/90002', true),
    ('a0000000-0000-0000-0000-000000000003', 'data-collective', 90003, 'https://avatars.githubusercontent.com/u/90003', true)
ON CONFLICT (user_id, org_login) DO NOTHING;

-- Groups with different acceptance modes
INSERT INTO groups (id, name, description, created_by, acceptance_mode) VALUES
    ('d0000000-0000-0000-0000-000000000001', 'AI Research Team', 'Sharing transcripts related to AI research', 'a0000000-0000-0000-0000-000000000001', 'open'),
    ('d0000000-0000-0000-0000-000000000002', 'Verified Contributors', 'Only verified org members can share here', 'a0000000-0000-0000-0000-000000000002', 'verified_only'),
    ('d0000000-0000-0000-0000-000000000003', 'Curated Showcase', 'Owner-approved transcripts only', 'a0000000-0000-0000-0000-000000000001', 'curated')
ON CONFLICT DO NOTHING;

INSERT INTO group_members (group_id, user_id, role) VALUES
    -- AI Research Team (open)
    ('d0000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000001', 'owner'),
    ('d0000000-0000-0000-0000-000000000001', 'a0000000-0000-0000-0000-000000000002', 'member'),
    -- Verified Contributors (verified_only)
    ('d0000000-0000-0000-0000-000000000002', 'a0000000-0000-0000-0000-000000000002', 'owner'),
    ('d0000000-0000-0000-0000-000000000002', 'a0000000-0000-0000-0000-000000000001', 'member'),
    ('d0000000-0000-0000-0000-000000000002', 'a0000000-0000-0000-0000-000000000003', 'member'),
    -- Curated Showcase (curated)
    ('d0000000-0000-0000-0000-000000000003', 'a0000000-0000-0000-0000-000000000001', 'owner'),
    ('d0000000-0000-0000-0000-000000000003', 'a0000000-0000-0000-0000-000000000002', 'member'),
    ('d0000000-0000-0000-0000-000000000003', 'a0000000-0000-0000-0000-000000000003', 'member')
ON CONFLICT DO NOTHING;

-- Transcript shares with different statuses
-- AI Research Team (open): auto-approved share
INSERT INTO transcript_shares (transcript_id, group_id, status) VALUES
    ('c0000000-0000-0000-0000-000000000004', 'd0000000-0000-0000-0000-000000000001', 'approved')
ON CONFLICT DO NOTHING;
UPDATE transcripts SET visibility = 'shared' WHERE id = 'c0000000-0000-0000-0000-000000000004';

-- Verified Contributors: approved shares from verified members
INSERT INTO transcript_shares (transcript_id, group_id, status) VALUES
    ('c0000000-0000-0000-0000-000000000003', 'd0000000-0000-0000-0000-000000000002', 'approved'),
    ('c0000000-0000-0000-0000-000000000005', 'd0000000-0000-0000-0000-000000000002', 'approved')
ON CONFLICT DO NOTHING;
UPDATE transcripts SET visibility = 'shared' WHERE id IN (
    'c0000000-0000-0000-0000-000000000003',
    'c0000000-0000-0000-0000-000000000005'
);

-- Curated Showcase: mix of pending (received for review), approved, and rejected.
-- The 'pending' rows are the seed fixture for the owner-review workflow on
-- /groups/{id} — alice-dev (owner of Curated Showcase) sees them under
-- "Pending review" and can approve / reject.
INSERT INTO transcript_shares (transcript_id, group_id, status) VALUES
    ('c0000000-0000-0000-0000-000000000001', 'd0000000-0000-0000-0000-000000000003', 'approved'),
    ('c0000000-0000-0000-0000-000000000002', 'd0000000-0000-0000-0000-000000000003', 'pending'),
    ('c0000000-0000-0000-0000-000000000003', 'd0000000-0000-0000-0000-000000000003', 'pending'),
    ('c0000000-0000-0000-0000-000000000005', 'd0000000-0000-0000-0000-000000000003', 'rejected')
ON CONFLICT DO NOTHING;
UPDATE transcripts SET visibility = 'shared' WHERE id = 'c0000000-0000-0000-0000-000000000001';

-- Attestations (org members vouch for transcript usage)
INSERT INTO attestations (id, transcript_id, attester_id, org_login, attestation_type, note) VALUES
    -- anthropic-labs used alice's auth debugging transcript for training
    ('e0000000-0000-0000-0000-000000000001', 'c0000000-0000-0000-0000-000000000001',
     'a0000000-0000-0000-0000-000000000001', 'anthropic-labs', 'used_in_training',
     'Used as a training example for auth-related debugging patterns'),
    -- anthropic-labs deployed alice's React setup transcript
    ('e0000000-0000-0000-0000-000000000002', 'c0000000-0000-0000-0000-000000000002',
     'a0000000-0000-0000-0000-000000000002', 'anthropic-labs', 'deployed',
     'Template adopted for internal frontend bootstrapping'),
    -- openai-research evaluated bob's query refactoring transcript
    ('e0000000-0000-0000-0000-000000000003', 'c0000000-0000-0000-0000-000000000003',
     'a0000000-0000-0000-0000-000000000003', 'openai-research', 'evaluated',
     'Benchmarked query optimization patterns against our dataset'),
    -- data-collective referenced charlie's REST API transcript
    ('e0000000-0000-0000-0000-000000000004', 'c0000000-0000-0000-0000-000000000005',
     'a0000000-0000-0000-0000-000000000003', 'data-collective', 'referenced',
     'Referenced in our Go API best practices guide'),
    -- anthropic-labs also referenced the multi-agent debugging session
    ('e0000000-0000-0000-0000-000000000005', 'c0000000-0000-0000-0000-000000000004',
     'a0000000-0000-0000-0000-000000000001', 'anthropic-labs', 'referenced',
     'Cited in multi-agent coordination research paper')
ON CONFLICT (transcript_id, org_login, attestation_type) DO NOTHING;
