package database

import (
	"context"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	migrationTimeout         = 5 * time.Minute
	migrationCleanupTimeout  = 10 * time.Second
	migrationAdvisoryLockKey = int64(0x56494c4c414745) // "VILLAGE"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type migration struct {
	version int
	file    string
}

var migrations = []migration{
	{version: 1, file: "migrations/001_initial_schema.up.sql"},
	{version: 2, file: "migrations/002_transcript_metadata_v2.up.sql"},
	{version: 3, file: "migrations/003_cli_auth_sessions.up.sql"},
	{version: 4, file: "migrations/004_transcript_metrics_v2.up.sql"},
	{version: 5, file: "migrations/005_github_org_affiliations.up.sql"},
	{version: 6, file: "migrations/006_attestations.up.sql"},
	{version: 7, file: "migrations/007_backfill_titles.up.sql"},
	{version: 8, file: "migrations/008_tiered_access.up.sql"},
	{version: 9, file: "migrations/009_annotations.up.sql"},
	{version: 10, file: "migrations/010_account_deletion_cascade.up.sql"},
	{version: 11, file: "migrations/011_collective_github_org.up.sql"},
	{version: 12, file: "migrations/012_collective_display_members.up.sql"},
	{version: 13, file: "migrations/013_group_member_pending.up.sql"},
	{version: 14, file: "migrations/014_user_discoverable.up.sql"},
	{version: 15, file: "migrations/015_multi_provider_auth.up.sql"},
	{version: 16, file: "migrations/016_transcript_deletion_policy.up.sql"},
	{version: 17, file: "migrations/017_annotation_annotator_kind.up.sql"},
	{version: 18, file: "migrations/018_user_username.up.sql"},
	{version: 20, file: "migrations/020_transcript_commits.up.sql"},
	{version: 21, file: "migrations/021_collective_repositories.up.sql"},
	{version: 22, file: "migrations/022_repository_commits.up.sql"},
	{version: 23, file: "migrations/023_backfill_harness_values.up.sql"},
	{version: 24, file: "migrations/024_transcript_content_hash.up.sql"},
	// 025 is deliberately unregistered: both in-branch generations of that
	// never-merged migration are superseded by the guarded, fixpoint 026 (see its
	// header). Branch DBs may carry a stale schema_migrations version=25 row —
	// harmless, and kept as a forensic trace. Version gaps are fine (18→20 above).
	{version: 26, file: "migrations/026_license_governance.up.sql"},
	{version: 27, file: "migrations/027_reserve_system_uuid_prefix.up.sql"},
	{version: 28, file: "migrations/028_association_annotation_ingress.up.sql"},
	{version: 29, file: "migrations/029_owner_scoped_annotation_hashes.up.sql"},
	{version: 30, file: "migrations/030_manual_annotation_transcript_targets.up.sql"},
	{version: 31, file: "migrations/031_transcript_encryption.up.sql"},
	{version: 32, file: "migrations/032_authoritative_publication_receipts.up.sql"},
	{version: 33, file: "migrations/033_session_origin.up.sql"},
	{version: 34, file: "migrations/034_owner_overrides.up.sql"},
	{version: 35, file: "migrations/035_project_hash_required.up.sql"},
	{version: 36, file: "migrations/036_transcript_share_attempts.up.sql"},
}

func RunMigrations(pool *pgxpool.Pool) error {
	for _, m := range migrations {
		if err := runMigration(pool, m); err != nil {
			return err
		}
	}

	return nil
}

func runMigration(pool *pgxpool.Pool, m migration) error {
	sql, err := migrationsFS.ReadFile(m.file)
	if err != nil {
		return fmt.Errorf("migration %d could not read embedded file %q before database execution; the binary is incomplete, no schema change was attempted; rebuild Village with all migration files included: %w", m.version, m.file, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), migrationTimeout)
	defer cancel()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("migration %d could not begin its atomic database transaction; no migration body or registry row was committed; verify PostgreSQL connectivity and transaction capacity, then retry: %w", m.version, err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), migrationCleanupTimeout)
		defer cleanupCancel()
		_ = tx.Rollback(cleanupCtx)
	}()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationAdvisoryLockKey); err != nil {
		return fmt.Errorf("migration %d could not acquire the Village migration advisory lock inside its transaction; no migration body or registry row was committed; check for blocked or unhealthy migrators, then retry: %w", m.version, err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("migration %d could not create or verify schema_migrations inside the locked transaction; no migration body or registry row was committed; repair database permissions or the registry schema, then retry: %w", m.version, err)
	}

	var applied bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", m.version).Scan(&applied); err != nil {
		return fmt.Errorf("migration %d could not check its exact registry version inside the locked transaction; no migration body or registry row was committed; verify schema_migrations is readable, then retry: %w", m.version, err)
	}
	if !applied {
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %d body failed inside the atomic transaction; its DDL and registry row were rolled back together; correct the reported database precondition and retry: %w", m.version, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", m.version); err != nil {
			return fmt.Errorf("migration %d registry insert failed after its body ran; the transaction rolls back both the body and registry update, so retry is safe after correcting the registry failure: %w", m.version, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migration %d transaction commit did not return success; the outcome may be ambiguous, so do not edit schema_migrations manually; restore connectivity and rerun migrations, which will serialize and check this exact version: %w", m.version, err)
	}
	return nil
}
