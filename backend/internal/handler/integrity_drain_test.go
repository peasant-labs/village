package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// blockingChecker answers health at once and holds every check until it is
// released, so a test can drain while a check is in flight.
type blockingChecker struct {
	started chan struct{}
	release chan struct{}
}

func (c *blockingChecker) Health(context.Context) (schema.ScryHealthResponse, error) {
	return schema.ScryHealthResponse{ContractVersion: schema.ScryContractVersion, ScryVersion: "1.0.0"}, nil
}

func (c *blockingChecker) Check(ctx context.Context, req schema.ScryCheckRequest) (schema.ScryCheckResponse, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	select {
	case <-c.release:
		return schema.ScryCheckResponse{ScryVersion: "1.0.0", Verdict: schema.ScryVerdictChecked, FlaggedTurns: []int{}}, nil
	case <-ctx.Done():
		return schema.ScryCheckResponse{}, ctx.Err()
	}
}

// drainTestHandler is a handler whose queue holds one public transcript with
// canonical content, checked by a blocking checker.
func drainTestHandler(t *testing.T) (*Handler, *blockingChecker, chan string) {
	t.Helper()
	const key = "transcripts/30000000-0000-4000-8000-000000000003.bin"
	stored := canonicalStoredContent(t)
	store := newFakeBlobStore()
	store.put(key, stored)
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	q := publicTranscriptQuerier(key)
	base := q.getTranscriptByID
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	row := func(ctx context.Context) sqlc.Transcript {
		r, _ := base(ctx, id)
		r.ContentHash = pgtype.Text{String: schema.ComputeTranscriptHash(stored), Valid: true}
		r.BlobSizeBytes = pgtype.Int8{Int64: int64(len(stored)), Valid: true}
		r.PublishedAt, r.UpdatedAt = now, now
		return r
	}
	q.getTranscriptByID = func(ctx context.Context, _ pgtype.UUID) (sqlc.Transcript, error) { return row(ctx), nil }
	listed := false
	q.listIntegrityCandidates = func(ctx context.Context, _ sqlc.ListIntegrityCandidatesParams) ([]sqlc.Transcript, error) {
		if listed {
			return nil, nil
		}
		listed = true
		return []sqlc.Transcript{row(ctx)}, nil
	}
	q.isInIntegrityCheckedSet = func(context.Context, pgtype.UUID) (bool, error) { return true, nil }
	q.claimIntegrityAttempt = func(context.Context, sqlc.ClaimIntegrityAttemptParams) (int32, error) { return 1, nil }
	completed := make(chan string, 1)
	q.completeIntegrityAttempt = func(_ context.Context, arg sqlc.CompleteIntegrityAttemptParams) error {
		completed <- arg.Status
		return nil
	}
	checker := &blockingChecker{started: make(chan struct{}, 1), release: make(chan struct{})}
	h := newTestHandler(q, store)
	h.integrity = newIntegrityServiceFor(checker)
	return h, checker, completed
}

func TestDrainIntegrityWaitsForACheckInFlight(t *testing.T) {
	t.Run("returns_after_the_check_is_released", func(t *testing.T) {
		h, checker, completed := drainTestHandler(t)
		h.StartIntegrityWorker()
		select {
		case <-checker.started:
		case <-time.After(10 * time.Second):
			t.Fatal("the worker never sent a check")
		}
		drained := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			drained <- h.DrainIntegrity(ctx)
		}()
		select {
		case err := <-drained:
			t.Fatalf("drain returned %v while a check was in flight", err)
		case <-time.After(200 * time.Millisecond):
		}
		close(checker.release)
		select {
		case err := <-drained:
			if err != nil {
				t.Fatalf("drain after release = %v, want nil", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("drain did not return after the check was released")
		}
		select {
		case status := <-completed:
			if status != "done" {
				t.Fatalf("the drained check completed as %q, want done", status)
			}
		default:
			t.Fatal("the check released during the drain recorded no completion")
		}
	})

	t.Run("returns_the_context_error_when_the_deadline_passes_first", func(t *testing.T) {
		h, checker, _ := drainTestHandler(t)
		h.StartIntegrityWorker()
		select {
		case <-checker.started:
		case <-time.After(10 * time.Second):
			t.Fatal("the worker never sent a check")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if err := h.DrainIntegrity(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("drain with a passed deadline = %v, want the context deadline error", err)
		}
		close(checker.release)
		final, cancelFinal := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelFinal()
		if err := h.DrainIntegrity(final); err != nil {
			t.Fatalf("second drain after release = %v, want nil", err)
		}
	})

	t.Run("an_unconfigured_service_drains_at_once", func(t *testing.T) {
		h := newTestHandler(&mockQuerier{}, nil)
		h.StartIntegrityWorker()
		h.nudgeIntegrity()
		if err := h.DrainIntegrity(context.Background()); err != nil {
			t.Fatalf("drain with no service = %v, want nil", err)
		}
	})
}
