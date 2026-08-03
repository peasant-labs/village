//go:build integration

package database

// Live catalog and failure-path coverage for the association ingress migrations.
// Source-shape tests cannot prove Postgres installed the composite constraints,
// trigger, and indexes with the intended semantics, so this test reads pg_catalog
// and exercises every rejected mutation inside a savepoint.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration028And029_AssociationCatalogAndFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping association catalog integration test in -short mode")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, migration023TestDatabaseURL(t))
	if err != nil {
		t.Skipf("cannot create database pool: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("cannot reach test database: %v", err)
	}
	migrateTestDatabaseThrough(t, pool, migrationBoundary029)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin catalog transaction: %v", err)
	}
	defer tx.Rollback(ctx)

	requireCatalogConstraint(t, ctx, tx, "transcript_associations_owner_transcript_fk", "transcript association owner FK", "FOREIGN KEY (owner_id, transcript_id)", "REFERENCES transcripts(owner_id, id)")
	requireCatalogConstraint(t, ctx, tx, "annotations_target_association_owner_fk", "annotation association owner FK", "FOREIGN KEY (owner_id, target_association_id)", "REFERENCES transcript_associations(owner_id, association_id)")
	requireCatalogConstraint(t, ctx, tx, "transcripts_owner_id_id_key", "owner/transcript identity", "UNIQUE (owner_id, id)")
	requireCatalogConstraint(t, ctx, tx, "transcript_associations_pkey", "association owner identity", "PRIMARY KEY (owner_id, association_id)")
	requireCatalogConstraint(t, ctx, tx, "transcript_associations_relationship_key", "association relationship identity", "UNIQUE (owner_id, transcript_id, observed_commit_sha)")
	requireCatalogConstraint(t, ctx, tx, "transcript_associations_id_shape", "association ID check", "CHECK", "association_id")
	requireCatalogConstraint(t, ctx, tx, "transcript_associations_observed_commit_not_blank", "association observed-commit check", "CHECK", "observed_commit_sha")
	requireCatalogConstraint(t, ctx, tx, "annotations_target_association_exclusive", "association target exclusivity check", "CHECK", "target_kind")
	requireCatalogConstraint(t, ctx, tx, "annotations_target_association_id_shape", "association target shape check", "CHECK", "target_association_id")
	requireCatalogConstraint(t, ctx, tx, "annotations_owner_content_hash_key", "owner-scoped annotation hash identity", "UNIQUE (owner_id, content_hash)")
	requireCatalogIndex(t, ctx, tx, "idx_transcript_associations_transcript")
	requireCatalogIndex(t, ctx, tx, "idx_annotations_target_association")
	requireCatalogIndex(t, ctx, tx, "annotations_owner_content_hash_key")
	var triggerDefinition string
	if err := tx.QueryRow(ctx, `
		SELECT pg_get_triggerdef(t.oid)
		FROM pg_trigger t
		JOIN pg_class r ON r.oid = t.tgrelid
		WHERE r.relname = 'transcript_associations'
		  AND t.tgname = 'trg_transcript_associations_immutable'
	`).Scan(&triggerDefinition); err != nil {
		t.Fatalf("read immutable association trigger: %v", err)
	}
	if !strings.Contains(triggerDefinition, "BEFORE UPDATE") || !strings.Contains(triggerDefinition, "prevent_transcript_association_update") {
		t.Fatalf("immutable association trigger definition %q is incomplete", triggerDefinition)
	}

	seed := time.Now().UnixNano()
	ownerA := insertCatalogUser(t, ctx, tx, seed+1, "association-catalog-a")
	ownerB := insertCatalogUser(t, ctx, tx, seed+2, "association-catalog-b")
	transcriptA := insertTranscript(t, ctx, tx, ownerA, "catalog-session-a", "claude-code", "blob/catalog-a")
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcript_associations (owner_id, association_id, transcript_id, observed_commit_sha)
		VALUES ($1, $2, $3, $4)
	`, ownerA, "assoc-catalog-a", transcriptA, "commit-catalog-a"); err != nil {
		t.Fatalf("insert valid association fixture: %v", err)
	}

	requireCatalogMutationFailure(t, ctx, tx, "cross-owner transcript association FK", `
		INSERT INTO transcript_associations (owner_id, association_id, transcript_id, observed_commit_sha)
		VALUES ($1, $2, $3, $4)
	`, ownerB, "assoc-catalog-cross-owner", transcriptA, "commit-catalog-cross-owner")
	requireCatalogMutationFailure(t, ctx, tx, "association ID shape check", `
		INSERT INTO transcript_associations (owner_id, association_id, transcript_id, observed_commit_sha)
		VALUES ($1, $2, $3, $4)
	`, ownerA, "invalid association id", transcriptA, "commit-catalog-shape")
	requireCatalogMutationFailure(t, ctx, tx, "association observed-commit check", `
		INSERT INTO transcript_associations (owner_id, association_id, transcript_id, observed_commit_sha)
		VALUES ($1, $2, $3, $4)
	`, ownerA, "assoc-catalog-empty-hash", transcriptA, "   ")
	requireCatalogMutationFailure(t, ctx, tx, "association relationship uniqueness", `
		INSERT INTO transcript_associations (owner_id, association_id, transcript_id, observed_commit_sha)
		VALUES ($1, $2, $3, $4)
	`, ownerA, "assoc-catalog-alias", transcriptA, "commit-catalog-a")
	requireCatalogMutationFailure(t, ctx, tx, "association annotation owner FK", `
		INSERT INTO annotations (content_hash, owner_id, target_kind, target_association_id, type_id, value)
		VALUES ($1, $2, 'association', $3, 'catalog.type', 'value')
	`, "catalog-hash-cross-owner", ownerB, "assoc-catalog-a")
	requireCatalogMutationFailure(t, ctx, tx, "association target exclusivity", `
		INSERT INTO annotations (content_hash, owner_id, target_kind, target_association_id, session_id, type_id, value)
		VALUES ($1, $2, 'association', $3, 'session-must-be-null', 'catalog.type', 'value')
	`, "catalog-hash-exclusive", ownerA, "assoc-catalog-a")
	requireCatalogMutationFailure(t, ctx, tx, "immutable association update", `
		UPDATE transcript_associations
		SET observed_commit_sha = 'commit-mutated'
		WHERE owner_id = $1 AND association_id = $2
	`, ownerA, "assoc-catalog-a")

	const sameHash = "catalog-identical-payload-hash"
	for _, owner := range []string{ownerA, ownerB} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO annotations (content_hash, owner_id, target_kind, session_id, type_id, value)
			VALUES ($1, $2, 'session', 'catalog-session', 'catalog.type', 'same')
		`, sameHash, owner); err != nil {
			t.Fatalf("insert same annotation hash for owner %q: %v", owner, err)
		}
	}
	requireCatalogMutationFailure(t, ctx, tx, "same-owner annotation hash uniqueness", `
		INSERT INTO annotations (content_hash, owner_id, target_kind, session_id, type_id, value)
		VALUES ($1, $2, 'session', 'catalog-session-duplicate', 'catalog.type', 'same')
	`, sameHash, ownerA)
}

func catalogConstraintDefinition(t *testing.T, ctx context.Context, tx pgx.Tx, constraintName string) string {
	t.Helper()
	var definition string
	if err := tx.QueryRow(ctx, `
		SELECT pg_get_constraintdef(c.oid)
		FROM pg_constraint c
		WHERE c.conname = $1
	`, constraintName).Scan(&definition); err != nil {
		t.Fatalf("read catalog constraint %q: %v", constraintName, err)
	}
	return definition
}

func requireCatalogConstraint(t *testing.T, ctx context.Context, tx pgx.Tx, constraintName, label string, fragments ...string) {
	t.Helper()
	definition := catalogConstraintDefinition(t, ctx, tx, constraintName)
	for _, fragment := range fragments {
		if !strings.Contains(definition, fragment) {
			t.Fatalf("catalog constraint %q definition %q is missing %q", label, definition, fragment)
		}
	}
}

func requireCatalogIndex(t *testing.T, ctx context.Context, tx pgx.Tx, index string) {
	t.Helper()
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`, index).Scan(&count); err != nil {
		t.Fatalf("read catalog index %q: %v", index, err)
	}
	if count != 1 {
		t.Fatalf("catalog index %q count=%d, want 1", index, count)
	}
}

func insertCatalogUser(t *testing.T, ctx context.Context, tx pgx.Tx, githubID int64, username string) string {
	t.Helper()
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, githubID, username, fmt.Sprintf("provider-%d", githubID)).Scan(&id); err != nil {
		t.Fatalf("insert catalog user %q: %v", username, err)
	}
	return id
}

func requireCatalogMutationFailure(t *testing.T, ctx context.Context, tx pgx.Tx, operation, statement string, args ...any) {
	t.Helper()
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("begin savepoint for %s: %v", operation, err)
	}
	if _, err := savepoint.Exec(ctx, statement, args...); err == nil {
		_ = savepoint.Rollback(ctx)
		t.Fatalf("%s unexpectedly succeeded", operation)
	}
	if err := savepoint.Rollback(ctx); err != nil {
		t.Fatalf("rollback savepoint for %s: %v", operation, err)
	}
}
