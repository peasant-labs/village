-- A person's notification address. Personal contact data: these rows are read
-- only for the person's own settings and to send them their own email. No
-- statement here joins into a response another viewer receives.

-- name: GetNotificationEmail :one
SELECT * FROM user_notification_emails WHERE user_id = $1;

-- name: SetNotificationEmail :one
-- Sets or replaces the address. A replaced address is unverified again, and
-- the new verification token replaces any earlier one, so an old token cannot
-- confirm the new address. The invitation-email choice is kept.
INSERT INTO user_notification_emails (user_id, address, verify_token_hash, verify_expires_at)
VALUES (@user_id, @address, @verify_token_hash, @verify_expires_at)
ON CONFLICT (user_id) DO UPDATE
SET address           = EXCLUDED.address,
    verified_at       = NULL,
    verify_token_hash = EXCLUDED.verify_token_hash,
    verify_expires_at = EXCLUDED.verify_expires_at,
    updated_at        = now()
RETURNING *;

-- name: VerifyNotificationEmail :one
-- Confirms the address the token was issued for. A wrong, expired or replaced
-- token matches no row.
UPDATE user_notification_emails
SET verified_at       = now(),
    verify_token_hash = NULL,
    verify_expires_at = NULL,
    updated_at        = now()
WHERE user_id = @user_id
  AND address IS NOT NULL
  AND verify_token_hash = @verify_token_hash
  AND verify_expires_at > now()
RETURNING *;

-- name: RemoveNotificationEmail :exec
-- Removes the address and everything about it. The invitation-email choice is
-- kept, so an opt-out survives removing and re-adding an address.
UPDATE user_notification_emails
SET address           = NULL,
    verified_at       = NULL,
    verify_token_hash = NULL,
    verify_expires_at = NULL,
    updated_at        = now()
WHERE user_id = $1;

-- name: SetEmailInvitations :one
INSERT INTO user_notification_emails (user_id, email_invitations)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE
SET email_invitations = EXCLUDED.email_invitations,
    updated_at        = now()
RETURNING *;

-- name: UnsubscribeInvitationEmails :exec
-- The one-click unsubscribe: it can only turn invitation emails off.
UPDATE user_notification_emails
SET email_invitations = false, updated_at = now()
WHERE user_id = $1;

-- name: GetInvitationEmailRecipient :one
-- The address to email an invitation to, only when the person verified it and
-- still wants invitation emails. No row means send nothing.
SELECT address::text AS address
FROM user_notification_emails
WHERE user_id = $1
  AND address IS NOT NULL
  AND verified_at IS NOT NULL
  AND email_invitations;
