//go:build integration

package database

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// TestMigration042ReverseIndexServesTheLookup proves the index exists after the
// migrations and that the read it exists for answers correctly: the attachments
// binding one transcript, and only the ones still advertising it.
func TestMigration042ReverseIndexServesTheLookup(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)

	var definition string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE tablename = 'pull_request_attachment_transcripts'
		  AND indexname = 'idx_pull_request_attachment_transcripts_transcript'
	`).Scan(&definition); err != nil {
		t.Fatalf("the reverse index must exist after the migrations: %v", err)
	}
	if definition == "" || !strings.Contains(definition, "(transcript_id)") {
		t.Fatalf("index definition = %q, want it to index transcript_id", definition)
	}

	var ownerID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES (942001, 'reverse-index-owner', '942001') RETURNING id
	`).Scan(&ownerID); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	var groupID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, linked_github_org)
		VALUES ('reverse-index-collective', $1, 'peasant-labs') RETURNING id
	`, ownerID).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}

	seedTranscript := func(localID string) pgtype.UUID {
		t.Helper()
		// A transcript write needs the transaction-local markers the storage
		// trigger demands, the same way every other test seeds one.
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin transcript seed: %v", err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
			t.Fatalf("declare actor and writer marker: %v", err)
		}
		var id pgtype.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO transcripts (owner_id, local_id, model_provider, blob_key, blob_size_bytes, schema_version,
			                         project_hash, wrapped_data_key, encryption_algorithm, key_version, content_hash, visibility)
			VALUES ($1, $2, 'claude-code', 'k', 1, '2', 'h', '\x00', 'aes-256-gcm-random-nonce-v1', '1', 'c', 'public')
			RETURNING id
		`, ownerID, localID).Scan(&id); err != nil {
			t.Fatalf("insert transcript: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit transcript seed: %v", err)
		}
		return id
	}
	seedAttachment := func(number int, state string) pgtype.UUID {
		t.Helper()
		var id pgtype.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO pull_request_attachments (group_id, repo_owner, repo_name, github_repo_id, number, head_sha,
			                                      base_remote, head_remote, author_id, state)
			VALUES ($1, 'acme', 'widgets', 942, $2, 'sha', 'acme/widgets', 'acme/widgets', $3, $4)
			RETURNING id
		`, groupID, number, ownerID, state).Scan(&id); err != nil {
			t.Fatalf("insert attachment: %v", err)
		}
		return id
	}
	bind := func(attachmentID, transcriptID pgtype.UUID, position int) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO pull_request_attachment_transcripts (attachment_id, transcript_id, position, previous_visibility)
			VALUES ($1, $2, $3, 'private')
		`, attachmentID, transcriptID, position); err != nil {
			t.Fatalf("bind transcript: %v", err)
		}
	}

	queries := sqlc.New(pool)
	bound := seedTranscript("reverse-index-bound")
	other := seedTranscript("reverse-index-other")

	attached := seedAttachment(1, "attached")
	detached := seedAttachment(2, "detached")
	bind(attached, bound, 0)
	bind(detached, bound, 0)
	bind(attached, other, 1)

	found, err := queries.ListAttachmentsBindingTranscript(ctx, bound)
	if err != nil {
		t.Fatalf("read the attachments binding a transcript: %v", err)
	}
	if len(found) != 1 || found[0].ID != attached {
		t.Fatalf("attachments = %+v, want only the attached attachment that binds this transcript", found)
	}
}
