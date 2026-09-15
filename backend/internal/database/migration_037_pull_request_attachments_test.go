package database

import (
	"fmt"
	"strings"
	"testing"

	"github.com/peasant-labs/village/backend/internal/promptattach"
)

// TestMigration037PullRequestAttachments pins the migration's shape without a
// database: the two tables, the settings columns, and the two closed menus. The
// menu members are derived from the Go constants, never restated here, so
// widening promptattach.All or AllCheckModes without widening the CHECK fails
// this test instead of drifting.
func TestMigration037PullRequestAttachments(t *testing.T) {
	up, err := migrationsFS.ReadFile("migrations/037_pull_request_attachments.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/037_pull_request_attachments.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	upSQL := string(up)

	for _, required := range []string{
		"CREATE TABLE pull_request_attachments",
		"CREATE TABLE pull_request_attachment_transcripts",
		"previous_visibility TEXT NOT NULL",
		"UNIQUE (github_repo_id, number)",
		"pull_request_attachments_state_menu",
		"ADD COLUMN preview_before_attach BOOLEAN NOT NULL DEFAULT false",
		"ADD COLUMN post_prompts_check BOOLEAN NOT NULL DEFAULT true",
		"ADD COLUMN prompts_check_mode TEXT NOT NULL DEFAULT 'informational'",
		"groups_prompts_check_mode_menu",
	} {
		if !strings.Contains(upSQL, required) {
			t.Fatalf("migration 037 up SQL missing %q", required)
		}
	}

	for _, state := range promptattach.All {
		if !strings.Contains(upSQL, fmt.Sprintf("'%s'", state)) {
			t.Fatalf("migration 037 state CHECK omits menu member %q", state)
		}
	}
	for _, mode := range promptattach.AllCheckModes {
		if !strings.Contains(upSQL, fmt.Sprintf("'%s'", mode)) {
			t.Fatalf("migration 037 prompts-check-mode CHECK omits menu member %q", mode)
		}
	}

	downSQL := string(down)
	for _, required := range []string{
		"DROP TABLE IF EXISTS pull_request_attachment_transcripts",
		"DROP TABLE IF EXISTS pull_request_attachments",
		"DROP CONSTRAINT IF EXISTS groups_prompts_check_mode_menu",
		"DROP COLUMN IF EXISTS prompts_check_mode",
		"DROP COLUMN IF EXISTS post_prompts_check",
		"DROP COLUMN IF EXISTS preview_before_attach",
	} {
		if !strings.Contains(downSQL, required) {
			t.Fatalf("migration 037 down SQL missing %q", required)
		}
	}

	found := false
	for _, migration := range migrations {
		if migration.version == 37 {
			found = true
		}
	}
	if !found {
		t.Fatal("migration 037 is not registered")
	}
}
