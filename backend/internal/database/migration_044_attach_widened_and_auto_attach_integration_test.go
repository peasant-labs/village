//go:build integration

package database

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestMigration044MarksExistingBindingsAndRequiresNewOnesToSay proves the two
// columns against real PostgreSQL, across the migration itself.
//
// A binding written before 044 was written by an attach that widened its
// transcript, so the migration marks it true. After 044 the column has no
// default: an insert that omits it is refused, and one that states false keeps
// false. The author's automatic linking starts off for an existing account and
// for a new one.
func TestMigration044MarksExistingBindingsAndRequiresNewOnesToSay(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	for _, m := range migrations {
		if m.version >= 44 {
			continue
		}
		if err := runMigration(pool, m); err != nil {
			t.Fatalf("run migration %d: %v", m.version, err)
		}
	}

	var ownerID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES (944001, 'binding-owner', '944001') RETURNING id
	`).Scan(&ownerID); err != nil {
		t.Fatalf("insert owner: %v", err)
	}

	seedTranscript := func(localID string) pgtype.UUID {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin transcript insert: %v", err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
			t.Fatalf("declare actor and writer marker: %v", err)
		}
		var id pgtype.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO transcripts (owner_id, local_id, model_provider, blob_key, schema_version, project_hash,
			                         wrapped_data_key, encryption_algorithm, key_version, git_remote)
			VALUES ($1, $2, 'claude-code', $3, '2', 'c4e19a2f0b73',
			        decode('01','hex'), 'aes-256-gcm-random-nonce-v1', 1, 'git@github.com:acme/widgets.git')
			RETURNING id
		`, ownerID, localID, "transcripts/"+localID+".bin").Scan(&id); err != nil {
			t.Fatalf("insert transcript: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit transcript insert: %v", err)
		}
		return id
	}
	legacy := seedTranscript("binding-legacy")
	omitted := seedTranscript("binding-omitted")
	stated := seedTranscript("binding-stated")

	var attachmentID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO pull_request_attachments (repo_owner, repo_name, github_repo_id, number, head_sha,
		                                      base_remote, head_remote, author_id, state)
		VALUES ('acme', 'widgets', 944, 1, 'sha', 'acme/widgets', 'acme/widgets', $1, 'attached')
		RETURNING id
	`, ownerID).Scan(&attachmentID); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}

	// The shape every binding had before 044: no word on whether it widened.
	if _, err := pool.Exec(ctx, `
		INSERT INTO pull_request_attachment_transcripts (attachment_id, transcript_id, position, previous_visibility)
		VALUES ($1, $2, 0, 'private')
	`, attachmentID, legacy); err != nil {
		t.Fatalf("bind a transcript before the migration: %v", err)
	}

	if err := runMigration(pool, requireMigrationVersion(t, 44)); err != nil {
		t.Fatalf("run migration 044: %v", err)
	}

	var legacyWidened bool
	if err := pool.QueryRow(ctx, `
		SELECT attach_widened FROM pull_request_attachment_transcripts
		WHERE attachment_id = $1 AND transcript_id = $2
	`, attachmentID, legacy).Scan(&legacyWidened); err != nil {
		t.Fatalf("read the existing binding: %v", err)
	}
	if !legacyWidened {
		t.Fatal("a binding that existed before migration 044 must read attach_widened = true: the attach that wrote it widened its transcript, and detach restores it")
	}

	var columnDefault *string
	var nullable string
	if err := pool.QueryRow(ctx, `
		SELECT column_default, is_nullable FROM information_schema.columns
		WHERE table_name = 'pull_request_attachment_transcripts' AND column_name = 'attach_widened'
	`).Scan(&columnDefault, &nullable); err != nil {
		t.Fatalf("read attach_widened's column definition: %v", err)
	}
	if columnDefault != nil || nullable != "NO" {
		t.Fatalf("attach_widened default = %v, nullable = %q; want no default and NOT NULL", columnDefault, nullable)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO pull_request_attachment_transcripts (attachment_id, transcript_id, position, previous_visibility)
		VALUES ($1, $2, 1, 'private')
	`, attachmentID, omitted)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23502" || pgErr.ColumnName != "attach_widened" {
		t.Fatalf("a binding that does not state attach_widened: err = %v, want a not-null violation on attach_widened", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO pull_request_attachment_transcripts (attachment_id, transcript_id, position, previous_visibility, attach_widened)
		VALUES ($1, $2, 2, 'shared', false)
	`, attachmentID, stated); err != nil {
		t.Fatalf("bind a transcript that states attach_widened: %v", err)
	}
	var statedWidened bool
	if err := pool.QueryRow(ctx, `
		SELECT attach_widened FROM pull_request_attachment_transcripts
		WHERE attachment_id = $1 AND transcript_id = $2
	`, attachmentID, stated).Scan(&statedWidened); err != nil {
		t.Fatalf("read the new binding: %v", err)
	}
	if statedWidened {
		t.Fatal("a binding that stated attach_widened = false reads true")
	}

	var existingAuto bool
	if err := pool.QueryRow(ctx, `SELECT auto_attach_pull_requests FROM users WHERE id = $1`, ownerID).Scan(&existingAuto); err != nil {
		t.Fatalf("read the existing account's setting: %v", err)
	}
	if existingAuto {
		t.Fatal("an account that existed before migration 044 must start with automatic linking off")
	}
	var newAuto bool
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES (944002, 'binding-newcomer', '944002') RETURNING auto_attach_pull_requests
	`).Scan(&newAuto); err != nil {
		t.Fatalf("insert a new account: %v", err)
	}
	if newAuto {
		t.Fatal("a new account must start with automatic linking off")
	}
}
