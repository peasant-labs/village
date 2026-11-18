//go:build integration

package database

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// guardSQLState is the SQLSTATE the transition guard raises with.
const guardSQLState = "TR054"

// identifierLike matches a run that could be a hash or an object key fragment.
var identifierLike = regexp.MustCompile(`[0-9a-f]{16}`)

type guardRun struct {
	fixture revisionGuardFixture
	pool    *pgxpool.Pool
	id      string
	key     string
	newKey  string
}

func TestMigration054RevisionGuard(t *testing.T) {
	ctx := context.Background()
	fixture := loadRevisionGuardFixtures(t)
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	var latest int
	if err := pool.QueryRow(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&latest); err != nil || latest != wantLatestMigration {
		t.Fatalf("applied migration = %d (err %v), want %d", latest, err, wantLatestMigration)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			for _, trial := range c.Trials {
				t.Run(trial.Name, func(t *testing.T) {
					run := &guardRun{fixture: fixture, pool: pool, newKey: "transcripts/" + uuid.NewString() + ".bin"}
					run.seed(t, ctx, trial.Seed)
					switch {
					case trial.Concurrent != nil:
						run.concurrent(t, ctx, *trial.Concurrent)
					case trial.SingleTransaction:
						run.singleTransaction(t, ctx, trial.Steps)
					default:
						for _, step := range trial.Steps {
							run.separately(t, ctx, step)
						}
					}
					run.final(t, ctx, trial.Final)
				})
			}
		})
	}
}

func TestMigration054RevisionGuardCatalog(t *testing.T) {
	ctx := context.Background()
	pool := newMigrationScratchDatabase(t)
	mustRunMigrations(t, pool)
	rows, err := pool.Query(ctx, `
		SELECT tgname, (tgtype & 2) <> 0 AS before_row, pg_get_triggerdef(oid)
		FROM pg_trigger
		WHERE tgrelid = 'transcripts'::regclass AND tgname LIKE 'trg_transcript_writer_version%'
		ORDER BY tgname COLLATE "C"`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name, definition string
		var beforeRow bool
		if err := rows.Scan(&name, &beforeRow, &definition); err != nil {
			t.Fatal(err)
		}
		if !beforeRow || (strings.Contains(name, "_revision_") && !strings.Contains(definition, "BEFORE UPDATE OF")) {
			t.Fatalf("trigger %s is not a BEFORE row trigger (the revision triggers: BEFORE UPDATE OF): %s", name, definition)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// Triggers fire in name order (byte order), so the writer fence comes first, then
	// the signal, then the guard.
	want := "trg_transcript_writer_version,trg_transcript_writer_version_revision_aware,trg_transcript_writer_version_revision_guard"
	if strings.Join(names, ",") != want {
		t.Fatalf("writer-fence and revision triggers = %v, want %s", names, want)
	}
	down, err := migrationsFS.ReadFile("migrations/054_transcript_revision_guard.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(down)); err == nil {
		t.Fatal("forward-only migration accepted a downgrade")
	}
	var guards int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE tgrelid='transcripts'::regclass AND tgname LIKE 'trg_transcript_writer_version_revision_%'`).Scan(&guards); err != nil || guards != 2 {
		t.Fatalf("the refused down migration must leave both triggers: count=%d err=%v", guards, err)
	}
}

func (r *guardRun) seed(t *testing.T, ctx context.Context, seed revisionGuardSeed) {
	t.Helper()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	owner := insertFenceOwner(t, ctx, tx)
	mustAbsenteeExec(t, ctx, tx, `SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)`, owner)
	r.key = "transcripts/" + uuid.NewString() + ".bin"
	expression := func(value *string) string {
		if value == nil {
			return "NULL"
		}
		return *value
	}
	revision := int64(1)
	if seed.Revision != nil {
		revision = *seed.Revision
	}
	statement := fmt.Sprintf(`
		INSERT INTO transcripts (owner_id, local_id, visibility, model_provider, blob_key, schema_version, project_hash,
			wrapped_data_key, encryption_algorithm, key_version,
			content_hash, blob_size_bytes, segmented_identity_hash, segmented_identity_size, content_revision)
		VALUES ($1, $2, 'private', 'claude-code', $3, '1', 'c4e19a2f0b73',
			decode('01', 'hex'), 'aes-256-gcm-random-nonce-v1', 1,
			%s, %s, %s, %s, %d)
		RETURNING id::text`,
		expression(seed.Hash), expression(seed.Size), expression(seed.SegmentedHash), expression(seed.SegmentedSize), revision)
	if err := tx.QueryRow(ctx, statement, owner, uuid.NewString(), r.key).Scan(&r.id); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func (r *guardRun) begin(t *testing.T, ctx context.Context) pgx.Tx {
	t.Helper()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := tx.QueryRow(ctx, `SELECT owner_id::text FROM transcripts WHERE id=$1`, r.id).Scan(&owner); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	mustAbsenteeExec(t, ctx, tx, `SELECT set_config('app.actor_id',$1,true), set_config('app.transcript_writer_version','1',true)`, owner)
	return tx
}

func (r *guardRun) separately(t *testing.T, ctx context.Context, step revisionGuardStep) {
	t.Helper()
	tx := r.begin(t, ctx)
	defer tx.Rollback(ctx)
	affected, err := r.exec(ctx, tx, step)
	r.expect(t, step, affected, err)
	if step.Expect == guardRefused {
		return
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func (r *guardRun) singleTransaction(t *testing.T, ctx context.Context, steps []revisionGuardStep) {
	t.Helper()
	tx := r.begin(t, ctx)
	defer tx.Rollback(ctx)
	for _, step := range steps {
		affected, err := r.exec(ctx, tx, step)
		r.expect(t, step, affected, err)
		if step.Expect == guardRefused {
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// concurrent runs the first step in a transaction that stays open, starts the
// second step in another transaction, proves the second is waiting on the first's
// row lock, and only then commits the first.
func (r *guardRun) concurrent(t *testing.T, ctx context.Context, pair revisionGuardConcurrent) {
	t.Helper()
	first := r.begin(t, ctx)
	defer first.Rollback(ctx)
	affected, err := r.exec(ctx, first, pair.First)
	r.expect(t, pair.First, affected, err)

	second := r.begin(t, ctx)
	defer second.Rollback(ctx)
	var pid int32
	if err := second.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	type result struct {
		affected int64
		err      error
	}
	done := make(chan result, 1)
	go func() {
		n, err := r.exec(ctx, second, pair.Second)
		done <- result{n, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case early := <-done:
			t.Fatalf("the second statement finished before the first committed (affected=%d err=%v): the pair is not concurrent", early.affected, early.err)
		default:
		}
		var waiting string
		if err := r.pool.QueryRow(ctx, `SELECT COALESCE(wait_event_type, '') FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == "Lock" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the second statement never waited on the first's row lock (wait event type %q)", waiting)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatalf("commit first: %v", err)
	}
	select {
	case got := <-done:
		r.expect(t, pair.Second, got.affected, got.err)
		if pair.Second.Expect != guardRefused {
			if err := second.Commit(ctx); err != nil {
				t.Fatalf("commit second: %v", err)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second statement did not finish after the first committed")
	}
}

func (r *guardRun) final(t *testing.T, ctx context.Context, final revisionGuardFinal) {
	t.Helper()
	var revision int64
	var matches bool
	predicate := "TRUE"
	if final.Row != "" {
		predicate = r.expand(final.Row, revisionGuardStep{})
	}
	if err := r.pool.QueryRow(ctx, `SELECT content_revision, (`+predicate+`) FROM transcripts WHERE id=$1`, r.id).Scan(&revision, &matches); err != nil {
		t.Fatalf("read final row: %v", err)
	}
	if revision != final.Revision {
		t.Fatalf("final revision = %d, want %d", revision, final.Revision)
	}
	if !matches {
		t.Fatalf("final row does not satisfy %q", final.Row)
	}
}

func (r *guardRun) expect(t *testing.T, step revisionGuardStep, affected int64, err error) {
	t.Helper()
	switch step.Expect {
	case guardCommit:
		if err != nil || affected != 1 {
			t.Fatalf("want one row committed, got affected=%d err=%v", affected, err)
		}
	case guardUnmatched:
		if err != nil || affected != 0 {
			t.Fatalf("want the compare-and-swap to match no row, got affected=%d err=%v", affected, err)
		}
	case guardRefused:
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != guardSQLState {
			t.Fatalf("want a %s guard exception, got %v", guardSQLState, err)
		}
		if !strings.Contains(pgErr.Message, "("+step.Rule+")") {
			t.Fatalf("guard exception does not name %q: %s", step.Rule, pgErr.Message)
		}
		text := pgErr.Message + " " + pgErr.Detail + " " + pgErr.Hint
		for _, leaked := range []string{r.id, r.key, r.newKey} {
			if strings.Contains(text, leaked) {
				t.Fatalf("guard exception leaks an identifier: %s", text)
			}
		}
		if identifierLike.MatchString(text) {
			t.Fatalf("guard exception carries an identifier-like token: %s", text)
		}
	default:
		t.Fatalf("unknown outcome %q", step.Expect)
	}
}

func (r *guardRun) expand(sql string, step revisionGuardStep) string {
	hash := "NULL"
	if step.Hash != "" {
		hash = "repeat('" + step.Hash + "', 64)"
	}
	size := "NULL"
	if step.Size != nil {
		size = strconv.FormatInt(*step.Size, 10)
	}
	sql = strings.ReplaceAll(sql, "{first_read_install_sql}", r.fixture.FirstReadInstallSQL)
	return strings.NewReplacer(
		"{id}", r.id, "{blob_key}", r.key, "{new_key}", r.newKey, "{hash}", hash, "{size}", size,
	).Replace(sql)
}

type guardRowState struct {
	blobKey             string
	wrappedDataKey      []byte
	encryptionAlgorithm string
	keyVersion          int32
	contentHash         pgtype.Text
	blobSize            pgtype.Int8
	segmentedHash       pgtype.Text
	segmentedSize       pgtype.Int8
	contentRevision     int64
}

func (r *guardRun) read(ctx context.Context, tx pgx.Tx) (guardRowState, error) {
	var row guardRowState
	err := tx.QueryRow(ctx, `
		SELECT blob_key, wrapped_data_key, encryption_algorithm, key_version, content_hash, blob_size_bytes,
		       segmented_identity_hash, segmented_identity_size, content_revision
		FROM transcripts WHERE id=$1`, r.id).Scan(
		&row.blobKey, &row.wrappedDataKey, &row.encryptionAlgorithm, &row.keyVersion, &row.contentHash, &row.blobSize,
		&row.segmentedHash, &row.segmentedSize, &row.contentRevision)
	return row, err
}

// exec runs a step and returns the rows it changed. A writer step reads the row
// first and passes it as the statement's expectation, as the production writers do.
func (r *guardRun) exec(ctx context.Context, tx pgx.Tx, step revisionGuardStep) (int64, error) {
	hash := pgtype.Text{}
	if step.Hash != "" {
		hash = pgtype.Text{String: strings.Repeat(step.Hash, 64), Valid: true}
	}
	size := pgtype.Int8{}
	if step.Size != nil {
		size = pgtype.Int8{Int64: *step.Size, Valid: true}
	}
	parsed, err := uuid.Parse(r.id)
	if err != nil {
		return 0, err
	}
	id := pgtype.UUID{Bytes: parsed, Valid: true}
	q := sqlc.New(tx)
	switch step.Writer {
	case guardWriterRaw:
		tag, err := tx.Exec(ctx, r.expand(step.SQL, step))
		return tag.RowsAffected(), err
	case guardWriterLegacy:
		tag, err := tx.Exec(ctx, r.expand(r.fixture.LegacyReplaceSQL, step))
		return tag.RowsAffected(), err
	case guardWriterInstall:
		row, err := r.read(ctx, tx)
		if err != nil {
			return 0, err
		}
		return q.CompareAndSwapContentIdentity(ctx, sqlc.CompareAndSwapContentIdentityParams{
			ContentHash: hash, PlaintextSize: size, ID: id,
			ExpectedBlobKey: row.blobKey, ExpectedWrappedDataKey: row.wrappedDataKey,
			ExpectedEncryptionAlgorithm: row.encryptionAlgorithm, ExpectedKeyVersion: row.keyVersion,
			ExpectedPriorContentHash: row.contentHash, ExpectedPriorBlobSizeBytes: row.blobSize,
			ExpectedSegmentedIdentityHash: row.segmentedHash, ExpectedSegmentedIdentitySize: row.segmentedSize,
			ExpectedContentRevision: row.contentRevision,
		})
	case guardWriterReplace:
		row, err := r.read(ctx, tx)
		if err != nil {
			return 0, err
		}
		_, err = q.CompareAndSwapTranscriptBlob(ctx, sqlc.CompareAndSwapTranscriptBlobParams{
			BlobKey: r.newKey, WrappedDataKey: []byte{2}, EncryptionAlgorithm: row.encryptionAlgorithm, KeyVersion: row.keyVersion,
			ContentHash: hash, PlaintextSize: size, ContentRevision: step.Supplies, ID: id,
			ExpectedBlobKey: row.blobKey, ExpectedWrappedDataKey: row.wrappedDataKey,
			ExpectedEncryptionAlgorithm: row.encryptionAlgorithm, ExpectedKeyVersion: row.keyVersion,
			ExpectedContentRevision: row.contentRevision,
			ExpectedContentHash:     row.contentHash, ExpectedBlobSizeBytes: row.blobSize,
			ExpectedSegmentedIdentityHash: row.segmentedHash, ExpectedSegmentedIdentitySize: row.segmentedSize,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		if err != nil {
			return 0, err
		}
		return 1, nil
	case guardWriterRepair:
		row, err := r.read(ctx, tx)
		if err != nil {
			return 0, err
		}
		return q.RepairTranscriptContentIdentity(ctx, sqlc.RepairTranscriptContentIdentityParams{
			ContentHash: hash, BlobSizeBytes: size, ContentRevision: step.Supplies, ID: id,
			ExpectedContentRevision: row.contentRevision,
			ExpectedHash:            row.contentHash, ExpectedSize: row.blobSize,
			ExpectedSegmentedHash: row.segmentedHash, ExpectedSegmentedSize: row.segmentedSize,
		})
	default:
		return 0, fmt.Errorf("fixture writer %q is not implemented", step.Writer)
	}
}
