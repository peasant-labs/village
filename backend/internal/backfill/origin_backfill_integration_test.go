//go:build integration

package backfill

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// originBlobStore serves one fixture payload, or an injected authenticated
// read failure, to the origin backfill.
type originBlobStore struct {
	maintenanceBlobStore
	raw        []byte
	beforeRead func()
}

func (s *originBlobStore) Read(context.Context, uuid.UUID, storage.BlobDescriptor, storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	if s.beforeRead != nil {
		s.beforeRead()
	}
	if s.readErr != nil {
		return nil, storage.ContentIdentity{}, s.readErr
	}
	return append([]byte(nil), s.raw...), storage.ContentIdentity{}, nil
}

func originBackfillPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL is required; refusing to skip the real PostgreSQL origin backfill proof")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("real PostgreSQL unavailable: %v", err)
	}
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

func assertStoredOrigin(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, want sessionorigin.Origin) {
	t.Helper()
	var stored string
	if err := pool.QueryRow(ctx, "SELECT session_origin FROM transcripts WHERE id=$1", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if sessionorigin.Origin(stored) != want {
		t.Fatalf("stored session_origin=%q, want %q", stored, want)
	}
}

// TestOriginBackfillFixturesRealPostgres runs every fixture row through the
// full Run path: dry-run must decide without writing, apply must install the
// decision, and a second apply must be a no-op.
func TestOriginBackfillFixturesRealPostgres(t *testing.T) {
	ctx := context.Background()
	pool := originBackfillPool(t)
	fixtures, err := loadOriginBackfillFixtures(originBackfillCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			id, owner, _ := insertMaintenanceRow(t, ctx, pool, false, 1)
			defer deleteMaintenanceOwner(t, ctx, pool, owner)
			markedExec(t, ctx, pool, "UPDATE transcripts SET session_origin=$2 WHERE id=$1", id, fixture.StoredOrigin)

			store := &originBlobStore{raw: fixture.rawPayload(t)}
			if fixture.ReadFails {
				store.readErr = errors.New("injected authenticated read failure")
			}
			job, err := NewOriginBackfill(pool, store, nil, 1)
			if err != nil {
				t.Fatal(err)
			}

			dry, dryErr := job.Run(ctx, OriginBackfillModeDryRun)
			switch fixture.Outcome {
			case "updated":
				if dryErr != nil || dry.WouldUpdate != 1 || dry.Updated != 0 {
					t.Fatalf("dry-run result=%+v err=%v; want one pending update and no write", dry, dryErr)
				}
			case "unchanged":
				if dryErr != nil || dry.Unchanged != 1 || dry.WouldUpdate != 0 {
					t.Fatalf("dry-run result=%+v err=%v; want the row reported unchanged", dry, dryErr)
				}
			case "failed":
				if dryErr == nil || dry.Failed != 1 {
					t.Fatalf("dry-run result=%+v err=%v; want one failed row and an aggregate error", dry, dryErr)
				}
			}
			// Dry-run never writes, whatever it decided.
			assertStoredOrigin(t, ctx, pool, id, sessionorigin.Origin(fixture.StoredOrigin))

			applied, applyErr := job.Run(ctx, OriginBackfillModeApply)
			switch fixture.Outcome {
			case "updated":
				if applyErr != nil || applied.Updated != 1 {
					t.Fatalf("apply result=%+v err=%v; want one installed decision", applied, applyErr)
				}
			case "unchanged":
				if applyErr != nil || applied.Updated != 0 || applied.Unchanged != 1 {
					t.Fatalf("apply result=%+v err=%v; want no write", applied, applyErr)
				}
			case "failed":
				if applyErr == nil || applied.Failed != 1 || applied.Updated != 0 {
					t.Fatalf("apply result=%+v err=%v; want the row failed closed and unchanged", applied, applyErr)
				}
			}
			assertStoredOrigin(t, ctx, pool, id, sessionorigin.Origin(fixture.ExpectedOrigin))

			second, secondErr := job.Run(ctx, OriginBackfillModeApply)
			if fixture.Outcome == "failed" {
				if secondErr == nil || second.Failed != 1 {
					t.Fatalf("rerun result=%+v err=%v; a failing row must keep failing rather than being written", second, secondErr)
				}
			} else if secondErr != nil || second.Updated != 0 || second.Unchanged != 1 {
				t.Fatalf("rerun result=%+v err=%v; apply must be idempotent", second, secondErr)
			}
			assertStoredOrigin(t, ctx, pool, id, sessionorigin.Origin(fixture.ExpectedOrigin))
		})
	}
}

// TestOriginBackfillConcurrentPublishWins proves the compare-and-set: a value
// written between the batch read and the update keeps its place, and the run
// reports no update instead of clobbering it.
func TestOriginBackfillConcurrentPublishWins(t *testing.T) {
	ctx := context.Background()
	pool := originBackfillPool(t)
	fixtures, err := loadOriginBackfillFixtures(originBackfillCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	var reclassify originBackfillFixture
	for _, fixture := range fixtures {
		if fixture.Arm == "reclassify-to-agent" {
			reclassify = fixture
		}
	}
	if reclassify.Name == "" {
		t.Fatal("fixture set no longer carries a reclassify-to-agent row")
	}

	id, owner, _ := insertMaintenanceRow(t, ctx, pool, false, 1)
	defer deleteMaintenanceOwner(t, ctx, pool, owner)
	markedExec(t, ctx, pool, "UPDATE transcripts SET session_origin=$2 WHERE id=$1", id, reclassify.StoredOrigin)

	store := &originBlobStore{raw: reclassify.rawPayload(t)}
	store.beforeRead = func() {
		markedExec(t, ctx, pool, "UPDATE transcripts SET session_origin='user',updated_at=clock_timestamp() WHERE id=$1", id)
	}
	job, err := NewOriginBackfill(pool, store, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	result, err := job.Run(ctx, OriginBackfillModeApply)
	if err != nil || result.WouldUpdate != 1 || result.Updated != 0 || result.Failed != 0 {
		t.Fatalf("concurrent result=%+v err=%v; the concurrent write must win with no failure", result, err)
	}
	assertStoredOrigin(t, ctx, pool, id, sessionorigin.User)
}
