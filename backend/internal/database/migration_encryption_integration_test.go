//go:build integration

package database

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrationRunner_RealPostgres(t *testing.T) {
	cases, err := loadMigrationFixtures("testdata/migration_runner/cases.yaml", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			pool := newMigrationScratchDatabase(t)
			switch tc.Operation {
			case "full_history":
				mustRunMigrations(t, pool)
				assertMigrationRegistry(t, pool)
			case "concurrent":
				assertConcurrentMigrations(t, pool)
			case "exact_version":
				mustRunMigrations(t, pool)
				assertExactVersionCheck(t, pool)
			case "registry_rollback":
				prepareRegistryFailure(t, pool)
				assertMigration32RolledBack(t, pool)
			case "retry":
				prepareRegistryFailure(t, pool)
				removeRegistryFailureAndRetry(t, pool)
			default:
				t.Fatalf("fixture operation %q is not implemented", tc.Operation)
			}
		})
	}
}

func TestMigration031AndWriterFence_RealPostgres(t *testing.T) {
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	assertWriterFenceCases(t, pool)
}

func TestMigration031Fixture_RealPostgres(t *testing.T) {
	cases, err := loadMigrationFixtures("testdata/migration_031/cases.yaml", 6)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			pool := newMigrationScratchDatabase(t)
			switch tc.Operation {
			case "empty_activation":
				mustRunMigrations(t, pool)
				assertEncryptionDescriptorSchema(t, pool, true)
			case "nonempty_refusal":
				assertNonemptyMigrationRefusal(t, pool)
			case "queued_insert":
				assertQueuedLegacyInsertExcluded(t, pool)
			case "valid_descriptor":
				mustRunMigrations(t, pool)
				assertValidDescriptorAccepted(t, pool)
			case "invalid_descriptor":
				mustRunMigrations(t, pool)
				assertInvalidDescriptorsRejected(t, pool)
			case "guarded_down":
				mustRunMigrations(t, pool)
				assertDownMigrationGuarded(t, pool)
			default:
				t.Fatalf("fixture operation %q is not implemented", tc.Operation)
			}
		})
	}
}

func newMigrationScratchDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://test:test@localhost:5432/village_test?sslmode=disable"
	}
	base, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL for real migration proof: %v", err)
	}
	adminCfg := base.Copy()
	adminCfg.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(ctx, adminCfg)
	if err != nil {
		t.Skipf("real PostgreSQL migration proof unavailable while opening admin connection: %v", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Skipf("real PostgreSQL migration proof unavailable; preflight TEST_DATABASE_URL before trusting this skip: %v", err)
	}
	name := "village_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create isolated migration proof database (TEST_DATABASE_URL user needs CREATEDB): %v", err)
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
		t.Fatalf("open isolated migration proof database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func mustRunMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := RunMigrations(pool); err != nil {
		t.Fatalf("run complete production migration history: %v", err)
	}
}

func assertMigrationRegistry(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var count, version31 int
	if err := pool.QueryRow(context.Background(), "SELECT count(*), count(*) FILTER (WHERE version=31) FROM schema_migrations").Scan(&count, &version31); err != nil {
		t.Fatal(err)
	}
	if count != len(migrations) || version31 != 1 {
		t.Fatalf("registry rows = %d and version-31 rows = %d, want %d and 1", count, version31, len(migrations))
	}
}

func assertConcurrentMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for worker := 0; worker < 2; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- RunMigrations(pool)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent production migrator failed: %v", err)
		}
	}
	assertMigrationRegistry(t, pool)
}

func assertExactVersionCheck(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), "DELETE FROM schema_migrations WHERE version=24"); err != nil {
		t.Fatal(err)
	}
	err := runMigration(pool, migrations[22])
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("missing exact version must re-run its body and expose schema drift, got %v", err)
	}
}

func runThrough30(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := migrateDatabaseThrough(context.Background(), pool, migrationBoundary030); err != nil {
		t.Fatalf("prepare history through migration 030: %v", err)
	}
}

func prepareRegistryFailure(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := migrateDatabaseThrough(context.Background(), pool, migrationTestBoundary(31)); err != nil {
		t.Fatalf("prepare history through migration 031: %v", err)
	}
	_, err := pool.Exec(context.Background(), `
		CREATE FUNCTION reject_version_32() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.version = 32 THEN RAISE EXCEPTION 'forced registry rejection'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER reject_version_32 BEFORE INSERT ON schema_migrations FOR EACH ROW EXECUTE FUNCTION reject_version_32();`)
	if err != nil {
		t.Fatal(err)
	}
	if err := runMigration(pool, requireMigrationVersion(t, 32)); err == nil || !strings.Contains(err.Error(), "forced registry rejection") {
		t.Fatalf("forced registry failure = %v, want rejection", err)
	}
}

func assertMigration32RolledBack(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var receiptColumns, version31, version32 int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns WHERE table_name='transcripts' AND column_name='accepted_request_operation_fingerprint'`).Scan(&receiptColumns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE version=31), count(*) FILTER (WHERE version=32) FROM schema_migrations`).Scan(&version31, &version32); err != nil {
		t.Fatal(err)
	}
	if receiptColumns != 0 || version31 != 1 || version32 != 0 {
		t.Fatalf("failed migration 032 registry insert left receipt columns=%d, version-31 rows=%d, version-32 rows=%d; want encrypted boundary retained and receipt body atomically rolled back", receiptColumns, version31, version32)
	}
}

func removeRegistryFailureAndRetry(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	assertMigration32RolledBack(t, pool)
	if _, err := pool.Exec(context.Background(), "DROP TRIGGER reject_version_32 ON schema_migrations; DROP FUNCTION reject_version_32()"); err != nil {
		t.Fatal(err)
	}
	if err := runMigration(pool, requireMigrationVersion(t, 32)); err != nil {
		t.Fatalf("retry after known rollback: %v", err)
	}
	// The rolled-back migration is the LAST one this scenario applies by hand.
	// Everything registered after it is applied through the production runner so
	// the registry assertion below converges on the whole registry no matter how
	// many migrations ship later; without this the scenario would have to be
	// rewritten on every new migration.
	mustRunMigrations(t, pool)
	assertMigrationRegistry(t, pool)
}

func assertEncryptionDescriptorSchema(t *testing.T, pool *pgxpool.Pool, wantPresent bool) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns WHERE table_name='transcripts' AND column_name IN ('wrapped_data_key','encryption_algorithm','key_version')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	want := 0
	if wantPresent {
		want = 3
	}
	if count != want {
		t.Fatalf("encryption descriptor column count = %d, want %d", count, want)
	}
}

func assertNonemptyMigrationRefusal(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	runThrough30(t, pool)
	insertLegacyTranscript(t, pool)
	err := runMigration(pool, requireMigrationVersion(t, 31))
	if err == nil || !strings.Contains(err.Error(), "non-empty transcripts table") {
		t.Fatalf("migration 031 non-empty result = %v, want actionable refusal", err)
	}
	assertEncryptionMigrationRolledBack(t, pool)
}

func assertEncryptionMigrationRolledBack(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var descriptorColumns, version31 int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM information_schema.columns WHERE table_name='transcripts' AND column_name IN ('wrapped_data_key','encryption_algorithm','key_version')`).Scan(&descriptorColumns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM schema_migrations WHERE version=31`).Scan(&version31); err != nil {
		t.Fatal(err)
	}
	if descriptorColumns != 0 || version31 != 0 {
		t.Fatalf("refused migration 031 left descriptor columns=%d and version rows=%d; want atomic rollback", descriptorColumns, version31)
	}
}

func assertQueuedLegacyInsertExcluded(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	runThrough30(t, pool)
	ctx := context.Background()
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, "LOCK TABLE transcripts IN ACCESS SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	encryptionMigration := requireMigrationVersion(t, 31)
	migrationResult := make(chan error, 1)
	go func() { migrationResult <- runMigration(pool, encryptionMigration) }()
	if !waitForExclusiveLock(t, pool) {
		t.Fatal("migration 031 did not queue its ACCESS EXCLUSIVE lock")
	}
	insertResult := make(chan error, 1)
	go func() { insertResult <- tryLegacyInsert(pool) }()
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-migrationResult; err != nil {
		t.Fatalf("migration 031 after lock release: %v", err)
	}
	if err := <-insertResult; err == nil {
		t.Fatal("queued legacy insert succeeded after encryption activation")
	}
}

func assertValidDescriptorAccepted(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	owner := insertFenceOwner(t, ctx, tx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
		t.Fatal(err)
	}
	id, err := insertFenceTranscript(ctx, tx, owner)
	if err != nil {
		t.Fatalf("valid encrypted descriptor rejected: %v", err)
	}
	var wrapped []byte
	var algorithm string
	var version int
	if err := tx.QueryRow(ctx, "SELECT wrapped_data_key,encryption_algorithm,key_version FROM transcripts WHERE id=$1", id).Scan(&wrapped, &algorithm, &version); err != nil {
		t.Fatal(err)
	}
	if len(wrapped) != 1 || algorithm != "aes-256-gcm-random-nonce-v1" || version != 1 {
		t.Fatalf("stored descriptor = wrapped-length %d, algorithm %q, version %d", len(wrapped), algorithm, version)
	}
}

func assertInvalidDescriptorsRejected(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	assertInvalidDescriptor(t, pool, "decode('','hex')", "'aes-256-gcm-random-nonce-v1'", "1", "wrapped_data_key")
	assertInvalidDescriptor(t, pool, "decode('01','hex')", "'unsupported'", "1", "encryption_algorithm")
	assertInvalidDescriptor(t, pool, "decode('01','hex')", "'aes-256-gcm-random-nonce-v1'", "0", "key_version")
}

func assertInvalidDescriptor(t *testing.T, pool *pgxpool.Pool, wrappedSQL, algorithmSQL, versionSQL, wantConstraint string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	owner := insertFenceOwner(t, ctx, tx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
		t.Fatal(err)
	}
	statement := fmt.Sprintf(`INSERT INTO transcripts (owner_id,local_id,visibility,model_provider,blob_key,schema_version,wrapped_data_key,encryption_algorithm,key_version) VALUES ($1,$2,'private','claude-code',$3,'1',%s,%s,%s)`, wrappedSQL, algorithmSQL, versionSQL)
	_, err = tx.Exec(ctx, statement, owner, uuid.NewString(), "transcripts/"+uuid.NewString()+".bin")
	if err == nil || !strings.Contains(err.Error(), wantConstraint) {
		t.Fatalf("invalid %s descriptor result = %v, want named constraint rejection", wantConstraint, err)
	}
}

func assertDownMigrationGuarded(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	insertCommittedEncryptedTranscript(t, pool)
	down, err := migrationsFS.ReadFile("migrations/031_transcript_encryption.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), string(down)); err == nil || !strings.Contains(err.Error(), "cannot be reversed while transcripts exist") {
		t.Fatalf("down migration with live encrypted row = %v, want safety refusal", err)
	}
	assertEncryptionDescriptorSchema(t, pool, true)
	deleteAllTranscriptsMarked(t, pool)
	if _, err := pool.Exec(context.Background(), string(down)); err != nil {
		t.Fatalf("down migration on verified empty table: %v", err)
	}
	assertEncryptionDescriptorSchema(t, pool, false)
}

func insertCommittedEncryptedTranscript(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	owner := insertFenceOwner(t, ctx, tx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
		t.Fatal(err)
	}
	if _, err := insertFenceTranscript(ctx, tx, owner); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func deleteAllTranscriptsMarked(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true), set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM transcripts"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertWriterFenceCases(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	cases, err := loadMigrationFixtures("testdata/transcript_writer_fence/cases.yaml", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assertWriterFenceOperation(t, pool, tc.Operation)
		})
	}
}

func assertWriterFenceOperation(t *testing.T, pool *pgxpool.Pool, operation string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	owner := insertFenceOwner(t, ctx, tx)
	marker := "1"
	if operation == "old_insert" {
		marker = ""
	} else if operation == "wrong_marker" {
		marker = "0"
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version',$1,true), set_config('app.actor_id',$2,true)", marker, SystemActorID); err != nil {
		t.Fatal(err)
	}
	id, insertErr := insertFenceTranscript(ctx, tx, owner)
	if operation == "old_insert" || operation == "wrong_marker" {
		if insertErr == nil || !strings.Contains(insertErr.Error(), "writer marker") {
			t.Fatalf("legacy insert error = %v, want writer-marker rejection", insertErr)
		}
		return
	}
	if insertErr != nil {
		t.Fatal(insertErr)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','',true)"); err != nil {
		t.Fatal(err)
	}

	wantReject := strings.HasPrefix(operation, "old_")
	var mutationErr error
	switch operation {
	case "marked_insert":
		return
	case "old_blob_update", "marked_update":
		if operation == "marked_update" {
			_, _ = tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true)")
		}
		_, mutationErr = tx.Exec(ctx, "UPDATE transcripts SET blob_key=blob_key||'-next' WHERE id=$1", id)
	case "old_identity_update":
		_, mutationErr = tx.Exec(ctx, "UPDATE transcripts SET content_hash='abcd' WHERE id=$1", id)
	case "old_delete", "marked_delete", "marked_cascade":
		if operation != "old_delete" {
			_, _ = tx.Exec(ctx, "SELECT set_config('app.transcript_writer_version','1',true)")
		}
		if operation == "marked_cascade" {
			_, mutationErr = tx.Exec(ctx, "DELETE FROM users WHERE id=$1", owner)
		} else {
			_, mutationErr = tx.Exec(ctx, "DELETE FROM transcripts WHERE id=$1", id)
		}
	case "metadata_update":
		_, mutationErr = tx.Exec(ctx, "UPDATE transcripts SET title='safe metadata' WHERE id=$1", id)
	default:
		t.Fatalf("fixture operation %q is not implemented", operation)
	}
	if wantReject {
		if mutationErr == nil || !strings.Contains(mutationErr.Error(), "writer marker") {
			t.Fatalf("legacy mutation error = %v, want writer-marker rejection", mutationErr)
		}
	} else if mutationErr != nil {
		t.Fatalf("encryption-aware or metadata-only mutation failed: %v", mutationErr)
	}
}

func insertFenceOwner(t *testing.T, ctx context.Context, tx pgx.Tx) string {
	t.Helper()
	var id string
	seed := time.Now().UnixNano()
	if err := tx.QueryRow(ctx, `INSERT INTO users (github_id, github_username, provider_user_id) VALUES ($1,$2,$3) RETURNING id::text`, seed, fmt.Sprintf("fence-%d", seed), fmt.Sprint(seed)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertFenceTranscript(ctx context.Context, tx pgx.Tx, owner string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO transcripts (owner_id,local_id,visibility,model_provider,blob_key,schema_version,wrapped_data_key,encryption_algorithm,key_version)
		VALUES ($1,$2,'private','claude-code',$3,'1',decode('01','hex'),'aes-256-gcm-random-nonce-v1',1)
		RETURNING id::text`, owner, uuid.NewString(), "transcripts/"+uuid.NewString()+".bin").Scan(&id)
	return id, err
}

func insertLegacyTranscript(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := tryLegacyInsert(pool); err != nil {
		t.Fatalf("insert pre-encryption transcript fixture: %v", err)
	}
}

func tryLegacyInsert(pool *pgxpool.Pool) error {
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owner string
	seed := time.Now().UnixNano()
	if err := tx.QueryRow(ctx, `INSERT INTO users (github_id,github_username,provider_user_id) VALUES ($1,$2,$3) RETURNING id::text`, seed, fmt.Sprintf("legacy-%d", seed), fmt.Sprint(seed)).Scan(&owner); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id',$1,true)", SystemActorID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO transcripts (owner_id,local_id,visibility,model_provider,blob_key,schema_version) VALUES ($1,$2,'private','claude-code',$3,'1')`, owner, uuid.NewString(), "transcripts/"+uuid.NewString()+".json"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func requireMigrationVersion(t *testing.T, version int) migration {
	t.Helper()
	for _, candidate := range migrations {
		if candidate.version == version {
			return candidate
		}
	}
	t.Fatalf("production migration registry does not contain required version %03d", version)
	return migration{}
}

func waitForExclusiveLock(t *testing.T, pool *pgxpool.Pool) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := pool.QueryRow(context.Background(), `
			SELECT EXISTS (
				SELECT 1 FROM pg_locks l
				JOIN pg_class c ON c.oid=l.relation
				WHERE c.relname='transcripts' AND l.mode='AccessExclusiveLock' AND NOT l.granted
			)`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
