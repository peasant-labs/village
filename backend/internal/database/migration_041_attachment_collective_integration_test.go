//go:build integration

package database

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// TestMigration041AttachmentSurvivesCollectiveDeletion proves the reason
// group_id is nullable with ON DELETE SET NULL rather than CASCADE: deleting a
// collective must keep the attachment and, above all, the previous_visibility
// snapshot its transcripts carry. A cascade would delete those snapshots while
// the transcripts themselves stayed widened, leaving nothing to restore them
// from.
func TestMigration041AttachmentSurvivesCollectiveDeletion(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	queries := sqlc.New(pool)

	var ownerID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES (941001, 'attachment-owner', '941001') RETURNING id
	`).Scan(&ownerID); err != nil {
		t.Fatalf("insert owner: %v", err)
	}

	var groupID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, linked_github_org)
		VALUES ('attachment-collective', $1, 'peasant-labs') RETURNING id
	`, ownerID).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO collective_repositories (group_id, owner, name, installation_id, is_private, linked_by)
		VALUES ($1, 'peasant-labs', 'village', 4242, false, $2)
	`, groupID, ownerID); err != nil {
		t.Fatalf("link repository: %v", err)
	}

	// A transcript, written the way the encrypted storage path writes one.
	var transcriptID pgtype.UUID
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transcript insert: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
		t.Fatalf("declare actor and writer marker: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcripts (owner_id, local_id, model_provider, blob_key, schema_version, project_hash,
		                         wrapped_data_key, encryption_algorithm, key_version, git_remote)
		VALUES ($1, $2, 'claude-code', $3, '2', 'c4e19a2f0b73',
		        decode('01','hex'), 'aes-256-gcm-random-nonce-v1', 1, 'git@github.com:peasant-labs/village.git')
		RETURNING id
	`, ownerID, "attachment-session", "transcripts/attachment.bin").Scan(&transcriptID); err != nil {
		t.Fatalf("insert transcript: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit transcript insert: %v", err)
	}

	attachment, err := queries.CreatePullRequestAttachment(ctx, sqlc.CreatePullRequestAttachmentParams{
		GroupID:           groupID,
		RepoOwner:         "peasant-labs",
		RepoName:          "village",
		GithubRepoID:      4242,
		Number:            7,
		HeadSha:           "abc1234",
		BaseRemote:        "peasant-labs/village",
		HeadRemote:        "peasant-labs/village",
		AuthorID:          ownerID,
		RequesterGithubID: pgtype.Int8{},
	})
	if err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	if !attachment.GroupID.Valid || attachment.GroupID != groupID {
		t.Fatalf("attachment group_id = %v, want the linking collective %v", attachment.GroupID, groupID)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO pull_request_attachment_transcripts (attachment_id, transcript_id, position, previous_visibility)
		VALUES ($1, $2, 0, 'private')
	`, attachment.ID, transcriptID); err != nil {
		t.Fatalf("bind transcript: %v", err)
	}

	// Delete the collective: its repository links cascade away, but the
	// attachment and the snapshot do not.
	if _, err := pool.Exec(ctx, "DELETE FROM groups WHERE id = $1", groupID); err != nil {
		t.Fatalf("delete collective: %v", err)
	}

	var links int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM collective_repositories WHERE group_id = $1", groupID).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 0 {
		t.Fatalf("collective links = %d after deletion, want 0 (they cascade)", links)
	}

	survivor, err := queries.GetPullRequestAttachment(ctx, attachment.ID)
	if err != nil {
		t.Fatalf("attachment was deleted with its collective: %v", err)
	}
	if survivor.GroupID.Valid {
		t.Fatalf("attachment group_id = %v after the collective was deleted, want NULL", survivor.GroupID)
	}

	var previousVisibility string
	if err := pool.QueryRow(ctx, `
		SELECT previous_visibility FROM pull_request_attachment_transcripts
		WHERE attachment_id = $1 AND transcript_id = $2
	`, attachment.ID, transcriptID).Scan(&previousVisibility); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			t.Fatal("the visibility snapshot was deleted with the collective, so nothing could restore it")
		}
		t.Fatalf("read snapshot: %v", err)
	}
	if previousVisibility != "private" {
		t.Fatalf("previous_visibility = %q, want the recorded private value", previousVisibility)
	}
}
