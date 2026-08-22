//go:build integration

package backfill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/storage"
)

type titleBlobStore struct {
	maintenanceBlobStore
	raw        []byte
	beforeRead func()
}

func (s *titleBlobStore) Read(context.Context, uuid.UUID, storage.BlobDescriptor, storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	if s.beforeRead != nil {
		s.beforeRead()
	}
	return append([]byte(nil), s.raw...), storage.ContentIdentity{}, s.readErr
}

type maintenanceBlobStore struct {
	readIdentity storage.ContentIdentity
	readErr      error
	rewrapErr    error
	beforeRead   func()
	beforeRewrap func()
}

func (s *maintenanceBlobStore) Write(context.Context, uuid.UUID, []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	return storage.BlobDescriptor{}, storage.ContentIdentity{}, errors.New("write is outside maintenance test")
}
func (s *maintenanceBlobStore) Read(context.Context, uuid.UUID, storage.BlobDescriptor, storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	if s.beforeRead != nil {
		s.beforeRead()
	}
	return []byte("fixture"), s.readIdentity, s.readErr
}
func (s *maintenanceBlobStore) Rewrap(_ context.Context, _ uuid.UUID, d storage.BlobDescriptor) (storage.BlobDescriptor, error) {
	if s.beforeRewrap != nil {
		s.beforeRewrap()
	}
	if s.rewrapErr != nil {
		return storage.BlobDescriptor{}, s.rewrapErr
	}
	return storage.NewBlobDescriptor(d.ObjectKey(), []byte("rewrapped-key"), d.Algorithm(), 2)
}
func (*maintenanceBlobStore) Delete(context.Context, storage.BlobDescriptor) error { return nil }

func TestMaintenanceFixturesRealPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL is required; refusing to skip real PostgreSQL maintenance proof")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("real PostgreSQL unavailable: %v", err)
	}
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	identity, _ := storage.NewContentIdentity(schema.ComputeTranscriptHash([]byte("fixture")), 7)
	for _, tc := range loadCases(t, identityCasesYAML, 8) {
		t.Run("identity/"+tc.Name, func(t *testing.T) {
			id, owner, descriptor := insertMaintenanceRow(t, ctx, pool, true, 1)
			defer deleteMaintenanceOwner(t, ctx, pool, owner)
			store := &maintenanceBlobStore{readIdentity: identity}
			switch tc.Outcome {
			case "stale":
				store.beforeRead = func() { markedExec(t, ctx, pool, "UPDATE transcripts SET blob_size_bytes=99 WHERE id=$1", id) }
			case "failed":
				store.readErr = errors.New("injected authenticated read failure")
			}
			result, err := ContentIdentity(ctx, pool, store)
			if err != nil {
				t.Fatal(err)
			}
			switch tc.Outcome {
			case "installed":
				if result.Installed != 1 {
					t.Fatalf("result=%+v descriptor=%v", result, descriptor.ObjectKey())
				}
			case "stale":
				if result.Stale != 1 {
					t.Fatalf("result=%+v", result)
				}
			case "failed":
				if result.Failed != 1 {
					t.Fatalf("result=%+v", result)
				}
			}
		})
	}
	for _, tc := range loadCases(t, rewrapCasesYAML, 6) {
		t.Run("rewrap/"+tc.Name, func(t *testing.T) {
			version := int32(1)
			if tc.Outcome == "remaining-zero" {
				version = 2
			}
			id, owner, descriptor := insertMaintenanceRow(t, ctx, pool, false, version)
			defer deleteMaintenanceOwner(t, ctx, pool, owner)
			store := &maintenanceBlobStore{}
			if tc.Outcome == "failed" {
				store.rewrapErr = errors.New("injected custodian failure")
			}
			if tc.Outcome == "stale" {
				store.beforeRewrap = func() { markedExec(t, ctx, pool, "UPDATE transcripts SET wrapped_data_key='stale' WHERE id=$1", id) }
			}
			limit := 100
			if tc.Outcome == "remaining" {
				insertMaintenanceRowForOwner(t, ctx, pool, owner, false, 1)
				limit = 1
			}
			originalCommit := commitRewrapTransaction
			if tc.Outcome == "uncertain" {
				commitRewrapTransaction = func(context.Context, pgx.Tx) error { return errors.New("injected ambiguous commit") }
			}
			defer func() { commitRewrapTransaction = originalCommit }()
			result, err := Rewrap(ctx, pool, store, 2, limit)
			if err != nil {
				t.Fatal(err)
			}
			var key string
			var wrapped []byte
			var storedVersion int32
			var size pgtype.Int8
			var hash pgtype.Text
			if err := pool.QueryRow(ctx, "SELECT blob_key,wrapped_data_key,key_version,blob_size_bytes,content_hash FROM transcripts WHERE id=$1", id).Scan(&key, &wrapped, &storedVersion, &size, &hash); err != nil {
				t.Fatal(err)
			}
			if key != string(descriptor.ObjectKey()) {
				t.Fatalf("object key changed")
			}
			if size.Int64 != 7 || hash.String != schema.ComputeTranscriptHash([]byte("fixture")) {
				t.Fatalf("plaintext identity changed: size=%v hash=%v", size, hash)
			}
			switch tc.Outcome {
			case "installed":
				if result.Installed != 1 {
					t.Fatalf("result=%+v", result)
				}
				if string(wrapped) != "rewrapped-key" || storedVersion != 2 {
					t.Fatalf("wrapped key/version not installed")
				}
			case "stale":
				if result.Stale != 1 {
					t.Fatalf("result=%+v", result)
				}
			case "failed":
				if result.Failed != 1 {
					t.Fatalf("result=%+v", result)
				}
			case "uncertain":
				if result.Uncertain != 1 {
					t.Fatalf("result=%+v", result)
				}
			case "remaining":
				if result.Remaining != 1 {
					t.Fatalf("result=%+v", result)
				}
			case "remaining-zero":
				if result.Scanned != 0 {
					t.Fatalf("result=%+v", result)
				}
			}
		})
	}
}

func TestTitleBackfillRealPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL is required; refusing to skip real PostgreSQL title backfill proof")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("real PostgreSQL unavailable: %v", err)
	}
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	fixtures, err := loadTitleBackfillFixtures(titleBackfillCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatal(err)
	}
	var parity titleBackfillFixture
	for _, fixture := range fixtures {
		if fixture.Name == "shared_project_path_parity" {
			parity = fixture
		}
	}
	raw := buildTitleBackfillRawPayload(t, parity.FirstUserTurns)

	t.Run("dry-run-apply-idempotence", func(t *testing.T) {
		id, owner, _ := insertMaintenanceRow(t, ctx, pool, false, 1)
		defer deleteMaintenanceOwner(t, ctx, pool, owner)
		markedExec(t, ctx, pool, "UPDATE transcripts SET title='claude session',title_generated=NULL,model_provider='claude-code',project_path='/Users/developer/work/sample-app' WHERE id=$1", id)
		store := &titleBlobStore{raw: raw}
		job, err := NewTitleBackfill(pool, store, pipeline, nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		dry, err := job.Run(ctx, TitleBackfillModeDryRun)
		if err != nil || dry.WouldUpdate != 1 || dry.Updated != 0 || dry.Derived != 1 {
			t.Fatalf("dry result=%+v err=%v", dry, err)
		}
		assertStoredTitles(t, ctx, pool, id, "claude session", "")
		applied, err := job.Run(ctx, TitleBackfillModeApply)
		if err != nil || applied.Updated != 1 || applied.Derived != 1 {
			t.Fatalf("apply result=%+v err=%v", applied, err)
		}
		assertStoredTitles(t, ctx, pool, id, parity.Expected, parity.Expected)
		second, err := job.Run(ctx, TitleBackfillModeApply)
		if err != nil || second.Updated != 0 || second.Unchanged != 1 {
			t.Fatalf("second result=%+v err=%v", second, err)
		}
	})

	t.Run("concurrent-owner-edit-wins", func(t *testing.T) {
		id, owner, _ := insertMaintenanceRow(t, ctx, pool, false, 1)
		defer deleteMaintenanceOwner(t, ctx, pool, owner)
		markedExec(t, ctx, pool, "UPDATE transcripts SET title='claude session',title_generated=NULL,model_provider='claude-code',project_path='/Users/developer/work/sample-app' WHERE id=$1", id)
		store := &titleBlobStore{raw: raw}
		store.beforeRead = func() {
			markedExec(t, ctx, pool, "UPDATE transcripts SET title='Owner edit after snapshot',updated_at=clock_timestamp() WHERE id=$1", id)
		}
		job, err := NewTitleBackfill(pool, store, pipeline, nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		result, err := job.Run(ctx, TitleBackfillModeApply)
		if err != nil || result.WouldUpdate != 1 || result.Updated != 0 || result.Failed != 0 {
			t.Fatalf("concurrent result=%+v err=%v", result, err)
		}
		assertStoredTitles(t, ctx, pool, id, "Owner edit after snapshot", "")
	})

	// derive-multi-turn-selection runs every fixture row that carries two or
	// more first_user_turns through the full Run path against real
	// PostgreSQL and a stubbed encrypted blob, proving the injected-then-prose
	// turn selection (and the only-injected error path) end to end rather than
	// only through the pure unit-level function.
	t.Run("derive-multi-turn-selection", func(t *testing.T) {
		for _, fixture := range fixtures {
			if len(fixture.FirstUserTurns) < 2 {
				continue
			}
			fixture := fixture
			t.Run(fixture.Name, func(t *testing.T) {
				id, owner, _ := insertMaintenanceRow(t, ctx, pool, false, 1)
				defer deleteMaintenanceOwner(t, ctx, pool, owner)
				markedExec(t, ctx, pool, "UPDATE transcripts SET title='claude session',title_generated=NULL,model_provider='claude-code',project_path='/Users/developer/work/sample-app' WHERE id=$1", id)
				store := &titleBlobStore{raw: buildTitleBackfillRawPayload(t, fixture.FirstUserTurns)}
				job, err := NewTitleBackfill(pool, store, pipeline, nil, 1)
				if err != nil {
					t.Fatal(err)
				}
				result, err := job.Run(ctx, TitleBackfillModeApply)
				if fixture.ExpectedErrorContains != "" {
					// Run's aggregate error only summarizes counts (see
					// TitleBackfillResult.Err); the per-row actionable
					// message asserted by fixture.ExpectedErrorContains is
					// proven directly against deriveTitleFromPayload in
					// TestDeriveTitleFromPayloadFixtures. Here the
					// observable end-to-end contract is: the row fails
					// closed, stays unchanged, and the batch reports it.
					if err == nil || result.Failed != 1 || result.Updated != 0 {
						t.Fatalf("result=%+v err=%v, want Failed=1, Updated=0, and a non-nil aggregate error", result, err)
					}
					assertStoredTitles(t, ctx, pool, id, "claude session", "")
					return
				}
				if err != nil || result.Updated != 1 || result.Derived != 1 {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				assertStoredTitles(t, ctx, pool, id, fixture.Expected, fixture.Expected)
			})
		}
	})

	t.Run("failure-continues-with-safe-log", func(t *testing.T) {
		badID, owner, _ := insertMaintenanceRow(t, ctx, pool, false, 1)
		goodID, _, _ := insertMaintenanceRowForOwner(t, ctx, pool, owner, false, 1)
		defer deleteMaintenanceOwner(t, ctx, pool, owner)
		markedExec(t, ctx, pool, "UPDATE transcripts SET title='session',title_generated=NULL,model_provider='claude-code',project_path='/Users/developer/confidential/project' WHERE id=$1", badID)
		markedExec(t, ctx, pool, "UPDATE transcripts SET title='Fix /Users/developer/confidential/notes.txt',title_generated='Safe generated title',model_provider='claude-code',project_path='/Users/developer/work/sample-app' WHERE id=$1", goodID)
		var logs bytes.Buffer
		store := &titleBlobStore{raw: []byte("malformed-sensitive-content")}
		job, err := NewTitleBackfill(pool, store, pipeline, slog.New(slog.NewTextHandler(&logs, nil)), 1)
		if err != nil {
			t.Fatal(err)
		}
		result, err := job.Run(ctx, TitleBackfillModeApply)
		if err == nil || result.Failed != 1 || result.Updated != 1 || result.Scanned != 2 {
			t.Fatalf("continuation result=%+v err=%v", result, err)
		}
		if text := logs.String(); strings.Contains(text, "malformed-sensitive-content") || strings.Contains(text, "/Users/developer") || strings.Contains(text, "confidential") {
			t.Fatalf("safe log leaked controlled sensitive data: %s", text)
		}
	})
}

func assertStoredTitles(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, wantTitle, wantGenerated string) {
	t.Helper()
	var title, generated pgtype.Text
	if err := pool.QueryRow(ctx, "SELECT title,title_generated FROM transcripts WHERE id=$1", id).Scan(&title, &generated); err != nil {
		t.Fatal(err)
	}
	if title.String != wantTitle || title.Valid != (wantTitle != "") || generated.String != wantGenerated || generated.Valid != (wantGenerated != "") {
		t.Fatalf("stored title=%v generated=%v, want %q/%q", title, generated, wantTitle, wantGenerated)
	}
}

func insertMaintenanceRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pending bool, version int32) (uuid.UUID, uuid.UUID, storage.BlobDescriptor) {
	owner := uuid.New()
	markedExec(t, ctx, pool, "INSERT INTO users(id,github_id,github_username,provider,provider_user_id) VALUES($1,$2,$3,'github',$4)", owner, int64(owner[0])+900000, owner.String(), owner.String())
	return insertMaintenanceRowForOwner(t, ctx, pool, owner, pending, version)
}
func insertMaintenanceRowForOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner uuid.UUID, pending bool, version int32) (uuid.UUID, uuid.UUID, storage.BlobDescriptor) {
	id := uuid.New()
	key := storage.ObjectKey("transcripts/" + uuid.NewString() + ".bin")
	d, err := storage.NewBlobDescriptor(key, []byte("wrapped-key"), storage.EncryptionAES256GCMRandomNonceV1, storage.KeyVersion(version))
	if err != nil {
		t.Fatal(err)
	}
	hash := any(schema.ComputeTranscriptHash([]byte("fixture")))
	size := any(int64(7))
	if pending {
		hash = nil
		size = nil
	}
	markedExec(t, ctx, pool, "INSERT INTO transcripts(id,owner_id,local_id,visibility,model_provider,blob_key,blob_size_bytes,schema_version,content_hash,wrapped_data_key,encryption_algorithm,key_version) VALUES($1,$2,$3,'private','claude',$4,$5,'2',$6,$7,$8,$9)", id, owner, id.String(), key, size, hash, d.WrappedDEK(), d.Algorithm(), version)
	return id, owner, d
}
func markedExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT set_config('app.actor_id',$1,true),set_config('app.transcript_writer_version','1',true)", database.SystemActorID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
func deleteMaintenanceOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner uuid.UUID) {
	t.Helper()
	rows, err := pool.Query(ctx, "SELECT id FROM transcripts WHERE owner_id=$1", owner)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	markedExec(t, ctx, pool, "DELETE FROM users WHERE id=$1", owner)
	if len(ids) == 0 {
		return
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT set_config('app.audit_maintenance','on',true)"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "DELETE FROM transcript_governance_events_audit WHERE transcript_id = ANY($1)", ids); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// buildTitleBackfillRawPayload builds a stored-content envelope carrying the
// supplied ordered strings as RoleUser turns, matching what a fixture row's
// first_user_turns describes. It is the single place that shapes the raw blob
// bytes for title-backfill integration tests, so multi-turn cases and the
// single-turn parity case share the same envelope construction.
func buildTitleBackfillRawPayload(t *testing.T, turns []string) []byte {
	t.Helper()
	type turnJSON struct {
		Index   int    `json:"index"`
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	details := make([]turnJSON, 0, len(turns))
	for i, content := range turns {
		details = append(details, turnJSON{Index: i, Role: "user", Content: content})
	}
	turnsJSON, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(fmt.Sprintf(`{"contractVersion":"0.1.1","kind":"session_detail","sessionDetail":{"id":"fixture","harness":"claude-code","turns":%s}}`, turnsJSON))
}
