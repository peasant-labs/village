//go:build integration

package database

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestMigration043LookupIndexServesTheHook proves the index exists after the
// migrations and that the hook's predicate plans onto it, which is the whole
// reason it exists: the table's other index leads with lower(repo_owner), and
// that predicate never constrains the owner.
func TestMigration043LookupIndexServesTheHook(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)

	var definition string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE tablename = 'pull_request_attachments'
		  AND indexname = 'idx_pull_request_attachments_author_repo_name'
	`).Scan(&definition); err != nil {
		t.Fatalf("the lookup index must exist after the migrations: %v", err)
	}
	// The column list, not the index name: the name contains every word this could
	// otherwise be fooled by.
	if !strings.Contains(definition, "(author_id, lower(repo_name))") {
		t.Fatalf("index definition = %q, want the columns (author_id, lower(repo_name))", definition)
	}

	var ownerID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES (943001, 'lookup-index-owner', '943001') RETURNING id
	`).Scan(&ownerID); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	var groupID pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, linked_github_org)
		VALUES ('lookup-index-collective', $1, 'peasant-labs') RETURNING id
	`, ownerID).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO pull_request_attachments (group_id, repo_owner, repo_name, github_repo_id, number, head_sha,
		                                      base_remote, head_remote, author_id, state)
		VALUES ($1, 'acme', 'widgets', 943, 1, 'sha', 'acme/widgets', 'acme/widgets', $2, 'waiting')
	`, groupID, ownerID); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}

	// The predicate is the one ListAuthorAttachmentsForRepo states. It is written
	// out here because EXPLAIN needs the statement itself; the query file and this
	// test are the only two places it appears, and they move together.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin explain: %v", err)
	}
	defer tx.Rollback(ctx)
	// A sequential scan off makes the planner choose among indexes, which is the
	// question: whether one of them serves this predicate at all.
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatalf("disable sequential scans for the explain: %v", err)
	}
	rows, err := tx.Query(ctx, `
		EXPLAIN SELECT * FROM pull_request_attachments
		WHERE author_id = $1
		  AND lower(repo_name) = lower($2)
		  AND state = ANY($3::text[])
	`, ownerID, "widgets", []string{"waiting", "attached"})
	if err != nil {
		t.Fatalf("explain the hook's lookup: %v", err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan the plan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("read the plan: %v", err)
	}
	if !strings.Contains(plan.String(), "idx_pull_request_attachments_author_repo_name") {
		t.Fatalf("the hook's lookup does not plan onto the new index; plan=%s", plan.String())
	}
}
