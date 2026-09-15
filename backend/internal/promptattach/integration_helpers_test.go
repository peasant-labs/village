//go:build integration

package promptattach

// Real-PostgreSQL infrastructure for the attachment transition proof. It
// mirrors the database package's migration harness: each test gets an isolated
// database, the full production migration history, and a drop at cleanup.
// Transcript fixtures declare the fixed system actor and the encrypted-writer
// marker the migration-026 and migration-031 fences require.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/database"
)

// newScratchPool creates an isolated database, applies the migration history,
// and drops the database at cleanup. It requires CREATEDB on the
// TEST_DATABASE_URL user.
func newScratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://test:test@localhost:5432/village_test?sslmode=disable"
	}
	base, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL for the attachment proof: %v", err)
	}

	adminCfg := base.Copy()
	adminCfg.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(ctx, adminCfg)
	if err != nil {
		t.Skipf("real PostgreSQL proof unavailable while opening admin connection: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Skipf("real PostgreSQL proof unavailable; preflight TEST_DATABASE_URL before trusting this skip: %v", err)
	}

	name := "village_attach_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create isolated attachment proof database (TEST_DATABASE_URL user needs CREATEDB): %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		admin.Close()
	})

	targetCfg := base.Copy()
	targetCfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, targetCfg)
	if err != nil {
		t.Fatalf("open isolated attachment proof database: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("run the migration history for the attachment proof: %v", err)
	}
	return pool
}

// insertOwner inserts a minimal users row and returns its id.
func insertOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	seed := time.Now().UnixNano()
	var id pgtype.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (github_id, github_username, provider_user_id) VALUES ($1,$2,$3) RETURNING id`,
		seed, fmt.Sprintf("attach-%d", seed), fmt.Sprint(seed),
	).Scan(&id); err != nil {
		t.Fatalf("insert owner fixture: %v", err)
	}
	return id
}

// insertTranscript inserts one encrypted-descriptor transcript at the given
// visibility, attributed to the fixed system actor, and returns its id.
func insertTranscript(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID, visibility string) pgtype.UUID {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transcript fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		"SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)",
		database.SystemActorID); err != nil {
		t.Fatalf("declare the system actor and writer marker for the transcript fixture: %v", err)
	}

	var id pgtype.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcripts (
			owner_id, local_id, visibility, model_provider, blob_key, schema_version,
			project_hash, wrapped_data_key, encryption_algorithm, key_version
		) VALUES (
			$1, $2, $3, 'claude-code', $4, '1',
			'c4e19a2f0b73', decode('01','hex'), 'aes-256-gcm-random-nonce-v1', 1
		) RETURNING id`,
		owner, uuid.NewString(), visibility, "transcripts/"+uuid.NewString()+".bin",
	).Scan(&id); err != nil {
		t.Fatalf("insert transcript fixture: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit transcript fixture: %v", err)
	}
	return id
}
