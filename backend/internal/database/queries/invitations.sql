-- Collective invitations. An invitation is open while expires_at > now();
-- expiry is decided at read time and no job deletes expired rows. Every read
-- below filters on it, so an expired invitation is never listed, accepted,
-- declined, revoked or counted.

-- name: GetOpenGroupInvitationByUsername :one
-- The open invitation, if any, for one collective and one typed handle. An
-- invitation that exists already makes inviting again the same answer with no
-- new write and no notice.
SELECT * FROM collective_invitations
WHERE group_id = $1 AND invited_username = $2 AND expires_at > now();

-- name: DeleteExpiredGroupInvitationByUsername :exec
-- An expired invitation for the same collective and handle is deleted by the
-- next invitation, so the unique target index admits the new one.
DELETE FROM collective_invitations
WHERE group_id = $1 AND invited_username = $2 AND expires_at <= now();

-- name: CountOpenGroupInvitations :one
SELECT COUNT(*)::int FROM collective_invitations
WHERE group_id = $1 AND expires_at > now();

-- name: CreateGroupInvitation :one
-- invitee_id is NULL when no account holds the handle. The row is written the
-- same way either way, so inviting never confirms that an account exists.
-- ON CONFLICT DO NOTHING answers a concurrent duplicate with no row, which the
-- handler treats as the same idempotent invitation. A day is 24 hours here,
-- whatever the session's time zone, so a lifetime never shifts by an hour.
INSERT INTO collective_invitations (group_id, invitee_id, invited_username, invited_by, status, expires_at)
VALUES (@group_id, sqlc.narg(invitee_id)::uuid, @invited_username, @invited_by, 'pending', now() + make_interval(hours => 24 * @lifetime_days::int))
ON CONFLICT (group_id, invited_username) DO NOTHING
RETURNING *;

-- name: ListGroupInvitations :many
-- The owners' list: pending and declined alike, so a decline is not revealed.
SELECT id, invited_username, invited_by, created_at, expires_at
FROM collective_invitations
WHERE group_id = $1 AND expires_at > now()
ORDER BY created_at DESC, id;

-- name: RevokeGroupInvitation :execrows
-- Revoking deletes the invitation; nothing records it.
DELETE FROM collective_invitations
WHERE id = @id AND group_id = @group_id AND expires_at > now();

-- name: ListMyInvitations :many
-- The invitee's own pending invitations, with the collective's summary.
SELECT ci.id, ci.invited_by, ci.created_at, ci.expires_at,
       g.id AS group_id, g.name AS group_name, g.description AS group_description,
       g.acceptance_mode AS group_acceptance_mode
FROM collective_invitations ci
JOIN groups g ON g.id = ci.group_id
WHERE ci.invitee_id = $1 AND ci.status = 'pending' AND ci.expires_at > now()
ORDER BY ci.created_at DESC, ci.id;

-- name: GetMyPendingInvitation :one
-- One of the caller's own pending invitations. Any other id, including one
-- addressed to someone else, is no row.
SELECT * FROM collective_invitations
WHERE id = @id AND invitee_id = @invitee_id AND status = 'pending' AND expires_at > now();

-- name: GetMyPendingInvitationForUpdate :one
SELECT * FROM collective_invitations
WHERE id = @id AND invitee_id = @invitee_id AND status = 'pending' AND expires_at > now()
FOR UPDATE;

-- name: DeleteInvitation :exec
-- Accepting deletes the invitation: the membership's origin is the record.
DELETE FROM collective_invitations WHERE id = $1;

-- name: DeclineMyInvitation :execrows
-- Declining keeps the row, unchanged to the owners, until it expires, so an
-- owner cannot tell a decline from no answer and cannot notify the person
-- again from the same collective before then.
UPDATE collective_invitations
SET status = 'declined'
WHERE id = @id AND invitee_id = @invitee_id AND status = 'pending' AND expires_at > now();
