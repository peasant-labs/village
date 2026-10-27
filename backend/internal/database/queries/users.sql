-- ⚠️ DO-NOT-TOUCH (TRAP — NOT the harness wire key): users.provider here is the
-- OAuth AUTH IDENTITY (github/gitlab/...), unrelated to the transcript harness.
-- It must NEVER be flipped to `harness` during the push-wire provider→harness
-- changeover. (users.sql.go is sqlc-GENERATED from this file — change neither.)

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByUsername :one
-- Canonical handle lookup. github_username is globally unique (case-insensitive),
-- so this resolves to exactly one user.
SELECT * FROM users WHERE lower(github_username) = lower($1);

-- name: GetUserByProviderIdentity :one
-- Resolves a signed-in identity to a user by the provider that authenticated
-- them, in the exact text form sign-in stored: GitHub OAuth writes
-- provider_user_id = github_id::text, so a webhook's numeric sender id is
-- compared as text and never by login (a login can be renamed and re-used).
-- A caller with no row — never signed in, or signed in through another provider
-- — is pgx.ErrNoRows, which the matcher reports as unresolved.
SELECT * FROM users WHERE provider = $1 AND provider_user_id = $2;

-- name: UpsertUser :one
-- github_username here is a freshly-generated unique candidate handle used only
-- on INSERT; on a returning user (conflict) we preserve their chosen handle and
-- only refresh the raw provider handle / profile fields.
INSERT INTO users (github_id, github_username, provider_username, display_name, avatar_url, provider, provider_user_id, username_chosen)
VALUES ($1, $2, $3, $4, $5, 'github', $1::bigint::text, false)
ON CONFLICT (github_id) DO UPDATE SET
    provider_username = EXCLUDED.provider_username,
    display_name = EXCLUDED.display_name,
    avatar_url = EXCLUDED.avatar_url,
    updated_at = now()
RETURNING *;

-- name: UpsertUserByProvider :one
-- Upsert a user identified by (provider, provider_user_id). github_id is a
-- synthesized stable BIGINT (caller computes from sha256 of the external id);
-- it is not a real GitHub ID for non-github providers. github_username is the
-- canonical, user-chosen handle: on INSERT it's a generated unique candidate,
-- and on a returning user (conflict) it is preserved — only provider_username
-- and profile fields are refreshed.
INSERT INTO users (github_id, github_username, provider_username, display_name, avatar_url, provider, provider_user_id, username_chosen)
VALUES ($1, $2, $3, $4, $5, $6, $7, false)
ON CONFLICT (provider, provider_user_id) DO UPDATE SET
    provider_username = EXCLUDED.provider_username,
    display_name = EXCLUDED.display_name,
    avatar_url = EXCLUDED.avatar_url,
    updated_at = now()
RETURNING *;

-- name: SetUsername :one
-- Sets the user's canonical handle and marks it explicitly chosen. Relies on the
-- case-insensitive unique index to reject collisions (caller maps 23505 -> 409).
UPDATE users SET
    github_username = $2,
    username_chosen = true,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: ConfirmOwnHandle :one
-- Marks the handle chosen at sign-in, when the handle the account holds is its
-- own login. It writes only the flag, never the handle. The guard confirms
-- exactly the handle the caller read and approved, and only while the row is
-- still unchosen; no row means the account changed since it was read (renamed,
-- or chosen by the person on the handle step), and the caller re-reads it.
UPDATE users SET
    username_chosen = true,
    updated_at = now()
WHERE id = $1 AND github_username = $2 AND NOT username_chosen
RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: UpdateUserDiscoverable :one
UPDATE users SET
    is_discoverable = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateUserAttachSettings :one
-- Sets the attach choices one settings PATCH carries: whether this user's own
-- pull request attachments stop at a preview the user confirms, and whether
-- their transcripts are linked automatically when a pull request opens in a
-- repository one of their collectives links. A NULL leaves that choice as it
-- is, so a PATCH that carries one field changes that one only, in one
-- statement.
UPDATE users
SET preview_before_attach     = COALESCE(sqlc.narg(preview_before_attach), preview_before_attach),
    auto_attach_pull_requests = COALESCE(sqlc.narg(auto_attach_pull_requests), auto_attach_pull_requests),
    updated_at                = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListPublicOwnersForViewer :many
-- The people one response shows, each with whether this viewer may see their
-- public profile. It is the single statement behind every owner, member,
-- contributor, and annotation author Village sends to a viewer, and it is
-- batched: a page names every person on it once, never one read per row.
--
-- A profile is visible when ANY of these holds; otherwise the person is shown
-- as anonymous:
--   - the person is discoverable (users.is_discoverable);
--   - the viewer IS the person;
--   - the viewer owns a collective (group_members.role = 'owner') where the
--     person took an act of their own: a membership they consented to
--     (group_members.origin creator, self_join or invitation: they created
--     the collective, joined it themselves, or accepted an invitation), a
--     pending or approved submission of one of their transcripts
--     (transcript_shares.status, the current-state row the review queue and
--     the collective library read), or a leftover 'pending' join-request row.
--     The origins are a positive list, so an origin added later reveals
--     nothing until it is listed here. A 'legacy' membership never counts: it
--     predates provenance, and an owner could add anyone by username then, so
--     it must reveal nothing about a person's transcripts. An invitation the
--     person has not accepted is never read here. Collective owners still see
--     their own roster in full (handler collectivePeopleFor).
-- A NULL viewer (signed out) sees discoverable people only.
SELECT u.id, u.github_username, u.display_name, u.avatar_url,
       (
         u.is_discoverable
         OR COALESCE(u.id = sqlc.narg(viewer_id)::uuid, FALSE)
         OR EXISTS (
             SELECT 1
             FROM group_members viewer_role
             WHERE viewer_role.user_id = sqlc.narg(viewer_id)::uuid
               AND viewer_role.role = 'owner'
               AND (
                     EXISTS (
                         SELECT 1 FROM group_members person_role
                         WHERE person_role.group_id = viewer_role.group_id
                           AND person_role.user_id = u.id
                           AND (
                                 person_role.origin IN ('creator', 'self_join', 'invitation')
                                 OR person_role.role = 'pending'
                               )
                     )
                     OR EXISTS (
                         SELECT 1
                         FROM transcript_shares ts
                         JOIN transcripts t ON t.id = ts.transcript_id
                         WHERE ts.group_id = viewer_role.group_id
                           AND t.owner_id = u.id
                           AND ts.status IN ('pending', 'approved')
                     )
                   )
         )
       )::boolean AS profile_visible
FROM users u
WHERE u.id = ANY(@person_ids::uuid[]);
