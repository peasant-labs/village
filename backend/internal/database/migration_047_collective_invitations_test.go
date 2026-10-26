package database

import (
	"strings"
	"testing"
)

// TestMigration047CollectiveInvitationsStructure pins what migration 047 must
// declare and that it is registered. Its behavior against PostgreSQL is
// proven in TestMigration047MembershipOriginAndInvitations.
func TestMigration047CollectiveInvitationsStructure(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/047_collective_invitations.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/047_collective_invitations.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"ADD COLUMN origin TEXT NOT NULL DEFAULT 'legacy'",
		"CHECK (origin IN ('creator', 'self_join', 'invitation', 'legacy'))",
		"ALTER COLUMN origin DROP DEFAULT",
		"CHECK (origin <> 'creator' OR role = 'owner')",
		"CREATE TRIGGER trg_group_member_origin_write_once",
		"CREATE TABLE collective_invitations",
		"invitee_id       UUID REFERENCES users(id) ON DELETE SET NULL",
		"invited_by       UUID REFERENCES users(id) ON DELETE SET NULL",
		"group_id         UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE",
		"CHECK (status IN ('pending', 'declined'))",
		"CREATE UNIQUE INDEX idx_collective_invitations_target",
		"CREATE INDEX idx_collective_invitations_invited_by",
		"CREATE TABLE user_notification_emails",
		"user_id           UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE",
		"email_invitations BOOLEAN NOT NULL DEFAULT true",
		"CREATE TABLE user_daily_quotas",
		"CHECK (quota IN ('invitation_sent', 'invitation_email', 'verification_email'))",
		"PRIMARY KEY (user_id, quota, day)",
	} {
		if !strings.Contains(string(up), required) {
			t.Errorf("migration 047 up SQL missing %q", required)
		}
	}
	for _, required := range []string{
		"DROP TABLE IF EXISTS user_daily_quotas",
		"DROP TABLE IF EXISTS user_notification_emails",
		"DROP TABLE IF EXISTS collective_invitations",
		"DROP TRIGGER IF EXISTS trg_group_member_origin_write_once ON group_members",
		"DROP COLUMN IF EXISTS origin",
	} {
		if !strings.Contains(string(down), required) {
			t.Errorf("migration 047 down SQL missing %q", required)
		}
	}
	found := false
	for _, migration := range migrations {
		if migration.version == 47 {
			found = true
		}
	}
	if !found {
		t.Fatal("migration 047 is not registered")
	}
}
