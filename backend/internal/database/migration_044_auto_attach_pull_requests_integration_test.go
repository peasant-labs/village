//go:build integration

package database

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestMigration044AutoAttachStartsOff proves the column against real PostgreSQL:
// the author's automatic linking starts off for an existing account and for a
// new one.
func TestMigration044AutoAttachStartsOff(t *testing.T) {
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
		VALUES (944001, 'auto-attach-owner', '944001') RETURNING id
	`).Scan(&ownerID); err != nil {
		t.Fatalf("insert owner: %v", err)
	}

	if err := runMigration(pool, requireMigrationVersion(t, 44)); err != nil {
		t.Fatalf("run migration 044: %v", err)
	}

	var existing bool
	if err := pool.QueryRow(ctx, `SELECT auto_attach_pull_requests FROM users WHERE id = $1`, ownerID).Scan(&existing); err != nil {
		t.Fatalf("read the existing account's setting: %v", err)
	}
	if existing {
		t.Fatal("an account that existed before migration 044 must start with automatic linking off")
	}
	var newcomer bool
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES (944002, 'auto-attach-newcomer', '944002') RETURNING auto_attach_pull_requests
	`).Scan(&newcomer); err != nil {
		t.Fatalf("insert a new account: %v", err)
	}
	if newcomer {
		t.Fatal("a new account must start with automatic linking off")
	}
}
