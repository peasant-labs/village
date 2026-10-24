//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestMigration046AutoAttachDefaultsOn proves the new default against real
// PostgreSQL: an account created after the migration links automatically, and
// an author who turned it off keeps that choice. Existing rows are deliberately
// not rewritten; only the default for a new account changes.
func TestMigration046AutoAttachDefaultsOn(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	for _, m := range migrations {
		if m.version >= 46 {
			continue
		}
		if err := runMigration(pool, m); err != nil {
			t.Fatalf("run migration %d: %v", m.version, err)
		}
	}

	var existingID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES (946001, 'auto-attach-existing', '946001') RETURNING id
	`).Scan(&existingID); err != nil {
		t.Fatalf("insert existing owner: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET auto_attach_pull_requests = false WHERE id = $1`, existingID); err != nil {
		t.Fatalf("record the existing author's explicit choice: %v", err)
	}

	if err := runMigration(pool, requireMigrationVersion(t, 46)); err != nil {
		t.Fatalf("run migration 046: %v", err)
	}

	var existing bool
	if err := pool.QueryRow(ctx, `SELECT auto_attach_pull_requests FROM users WHERE id = $1`, existingID).Scan(&existing); err != nil {
		t.Fatalf("read the existing account's setting: %v", err)
	}
	if existing {
		t.Fatal("migration 046 must not rewrite an existing account's stored choice")
	}
	var newcomer bool
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES (946002, 'auto-attach-newcomer', '946002') RETURNING auto_attach_pull_requests
	`).Scan(&newcomer); err != nil {
		t.Fatalf("insert a new account: %v", err)
	}
	if !newcomer {
		t.Fatal("a new account must start with automatic pull request linking on")
	}
}
