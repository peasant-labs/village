-- Forward-only in any shared environment: use this only on a development
-- database. It discards every open invitation, every notification address,
-- every daily limit count, and the record of which memberships were consented to.
DROP TABLE IF EXISTS user_daily_quotas;
DROP TABLE IF EXISTS user_notification_emails;
DROP TABLE IF EXISTS collective_invitations;
DROP TRIGGER IF EXISTS trg_group_member_origin_write_once ON group_members;
DROP FUNCTION IF EXISTS guard_group_member_origin();
ALTER TABLE group_members DROP CONSTRAINT IF EXISTS group_members_creator_is_owner;
ALTER TABLE group_members DROP COLUMN IF EXISTS origin;
