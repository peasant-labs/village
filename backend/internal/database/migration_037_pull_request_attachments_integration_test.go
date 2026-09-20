//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/peasant-labs/village/backend/internal/promptattach"
)

// TestMigration037MenusAndSettings proves the real columns over a real
// database: the attachment state defaults to `requested` and the CHECK accepts
// every Go menu member and rejects anything else; the per-user preview
// preference defaults off; and the collective check settings default to
// posting a check in `informational` mode under the closed mode menu.
func TestMigration037MenusAndSettings(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := insertFenceOwner(t, ctx, tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit owner fixture: %v", err)
	}

	var attachmentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO pull_request_attachments (
			repo_owner, repo_name, github_repo_id, number, head_sha, base_remote, head_remote, author_id
		) VALUES (
			'acme', 'widgets', 1, 1, '0123456789abcdef', 'https://github.com/acme/widgets.git',
			'https://github.com/acme/widgets.git', $1::uuid
		) RETURNING id::text`, owner).Scan(&attachmentID); err != nil {
		t.Fatalf("insert attachment fixture: %v", err)
	}

	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM pull_request_attachments WHERE id=$1::uuid`, attachmentID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != string(promptattach.Requested) {
		t.Fatalf("attachment state default = %q, want %q", state, promptattach.Requested)
	}
	for _, member := range promptattach.All {
		if _, err := pool.Exec(ctx, `UPDATE pull_request_attachments SET state=$2 WHERE id=$1::uuid`, attachmentID, string(member)); err != nil {
			t.Fatalf("state menu member %q was rejected by the CHECK: %v", member, err)
		}
	}
	for _, rejected := range []string{"merged", "REQUESTED", "", "attached "} {
		if _, err := pool.Exec(ctx, `UPDATE pull_request_attachments SET state=$2 WHERE id=$1::uuid`, attachmentID, rejected); err == nil {
			t.Fatalf("database accepted attachment state %q outside the closed menu", rejected)
		}
	}

	var preview bool
	if err := pool.QueryRow(ctx, `SELECT preview_before_attach FROM users WHERE id=$1::uuid`, owner).Scan(&preview); err != nil {
		t.Fatal(err)
	}
	if preview {
		t.Fatal("users.preview_before_attach must default false")
	}

	var groupID string
	if err := pool.QueryRow(ctx, `INSERT INTO groups (name, created_by) VALUES ('attach-group', $1::uuid) RETURNING id::text`, owner).Scan(&groupID); err != nil {
		t.Fatalf("insert group fixture: %v", err)
	}
	var postsCheck bool
	var mode string
	if err := pool.QueryRow(ctx, `SELECT post_prompts_check, prompts_check_mode FROM groups WHERE id=$1::uuid`, groupID).Scan(&postsCheck, &mode); err != nil {
		t.Fatal(err)
	}
	if !postsCheck {
		t.Fatal("groups.post_prompts_check must default true")
	}
	if mode != string(promptattach.Informational) {
		t.Fatalf("groups.prompts_check_mode default = %q, want %q", mode, promptattach.Informational)
	}
	for _, member := range promptattach.AllCheckModes {
		if _, err := pool.Exec(ctx, `UPDATE groups SET prompts_check_mode=$2 WHERE id=$1::uuid`, groupID, string(member)); err != nil {
			t.Fatalf("check-mode menu member %q was rejected by the CHECK: %v", member, err)
		}
	}
	for _, rejected := range []string{"blocking", "REQUIRED", "informational "} {
		if _, err := pool.Exec(ctx, `UPDATE groups SET prompts_check_mode=$2 WHERE id=$1::uuid`, groupID, rejected); err == nil {
			t.Fatalf("database accepted prompts_check_mode %q outside the closed menu", rejected)
		}
	}
}
