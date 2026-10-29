package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func TestServedIntegrityReadsOnceAndNeverServesNilTurns(t *testing.T) {
	calls := 0
	var seen sqlc.ServedIntegrityForContentParams
	var row sqlc.ServedIntegrityForContentRow
	var failure error
	h := &Handler{queries: &mockQuerier{servedIntegrityForContent: func(_ context.Context, arg sqlc.ServedIntegrityForContentParams) (sqlc.ServedIntegrityForContentRow, error) {
		calls++
		seen = arg
		return row, failure
	}}}
	transcript := sqlc.Transcript{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, ContentHash: pgtype.Text{String: "abc", Valid: true}}

	row = sqlc.ServedIntegrityForContentRow{Open: true}
	got, err := h.servedIntegrity(context.Background(), transcript)
	if err != nil || got == nil || got.FlaggedTurns == nil || len(got.FlaggedTurns) != 0 {
		t.Fatalf("open flag with no positions: %+v, %v; want a non-nil empty list", got, err)
	}
	if calls != 1 || seen.ContentHash != "abc" || seen.TranscriptID != transcript.ID {
		t.Fatalf("calls=%d params=%+v; want one read for the row's own content hash", calls, seen)
	}

	row = sqlc.ServedIntegrityForContentRow{Open: false, FlaggedTurns: []int32{3}}
	if got, err := h.servedIntegrity(context.Background(), transcript); err != nil || got != nil {
		t.Fatalf("no open flag: %+v, %v; want nil", got, err)
	}

	calls = 0
	if got, err := h.servedIntegrity(context.Background(), sqlc.Transcript{ID: transcript.ID}); err != nil || got != nil || calls != 0 {
		t.Fatalf("no content hash: %+v, %v, %d reads; want nil and no read", got, err, calls)
	}

	failure = errors.New("down")
	if _, err := h.servedIntegrity(context.Background(), transcript); err == nil {
		t.Fatal("a failed read must be an error, not an absent flag")
	}
}
