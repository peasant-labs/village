package handler

import (
	"context"
	_ "embed"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/attachment-refresh-budget.yaml
var attachmentRefreshBudgetYAML []byte

type attachmentBudgetCase struct {
	Name          string   `yaml:"name"`
	Canceled      bool     `yaml:"canceled"`
	FailFirst     bool     `yaml:"fail_first"`
	TranscriptIDs []string `yaml:"transcript_ids"`
}

func TestAttachmentBatchRefreshHasOneCompletionBudget(t *testing.T) {
	rows, err := decodeFixtureRows[attachmentBudgetCase](attachmentRefreshBudgetYAML)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]struct{}{}
	for _, row := range rows {
		if _, ok := present[row.Name]; ok || row.Name == "" {
			t.Fatal("invalid refresh budget fixture name")
		}
		present[row.Name] = struct{}{}
		if len(row.TranscriptIDs) < 2 {
			t.Fatal("batch budget fixture must observe multiple reads")
		}
	}
	assertExactTitleFixtureNames(t, "attachment-refresh-budget", present, []string{"a_multi_transcript_refresh_uses_one_completion_budget", "caller_cancellation_does_not_renew_the_batch_budget", "a_failed_read_does_not_renew_the_next_read_budget"})
	for _, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			var seen []context.Context
			ids := make([]pgtype.UUID, 0, len(row.TranscriptIDs))
			for _, id := range row.TranscriptIDs {
				parsed, err := uuid.Parse(id)
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, pgtype.UUID{Bytes: parsed, Valid: true})
			}
			firstErr := errors.New("fixture database unavailable")
			q := &mockQuerier{listAttachmentsBindingTranscript: func(ctx context.Context, id pgtype.UUID) ([]sqlc.PullRequestAttachment, error) {
				if id != ids[len(seen)] {
					t.Fatal("batch read did not use the fixture's transcript")
				}
				if err := ctx.Err(); err != nil {
					t.Fatalf("completion inherited caller cancellation: %v", err)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("completion has no deadline")
				}
				seen = append(seen, ctx)
				if row.FailFirst && len(seen) == 1 {
					return nil, firstErr
				}
				return nil, nil
			}}
			request, cancel := context.WithCancel(context.Background())
			defer cancel()
			if row.Canceled {
				cancel()
			}
			h := &Handler{queries: q}
			err := h.refreshAttachmentsForTranscripts(request, ids)
			if errors.Is(err, firstErr) != row.FailFirst {
				t.Fatalf("batch failure=%v", err)
			}
			if len(seen) != len(ids) {
				t.Fatal("the fixture did not observe every batch read")
			}
			deadline, _ := seen[0].Deadline()
			for _, ctx := range seen {
				got, _ := ctx.Deadline()
				if ctx != seen[0] || !got.Equal(deadline) {
					t.Fatal("the batch renewed its completion budget per transcript")
				}
			}
			if seen[0].Err() != context.Canceled {
				t.Fatal("the completed batch left its timer active")
			}
		})
	}
}
