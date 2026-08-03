package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// shasInPayload decodes the JSONB batch payload sent to InsertTranscriptCommits
// and returns the SHA set, so tests can assert the POST-STATE (which SHAs the
// village will hold) rather than call ordering.
func shasInPayload(t *testing.T, payload []byte) []string {
	t.Helper()
	var recs []commitJSONRecord
	if err := json.Unmarshal(payload, &recs); err != nil {
		t.Fatalf("decode commit batch payload: %v", err)
	}
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.Sha)
	}
	return out
}

// TestPersistCommits_ShrinkPrunesDroppedSHA proves the re-publish shrink case:
// when a transcript previously had {a,b,c} and the new payload has only {a,b},
// the batched insert carries exactly {a,b} (c dropped) AND the prune DELETE runs.
func TestPersistCommits_ShrinkPrunesDroppedSHA(t *testing.T) {
	transcriptID := pgtype.UUID{Bytes: uuid.New(), Valid: true}

	var deletedID pgtype.UUID
	deleteCalled := false
	var insertPayload []byte
	insertCalled := false

	q := &mockQuerier{
		deleteTranscriptCommits: func(_ context.Context, id pgtype.UUID) error {
			deleteCalled = true
			deletedID = id
			return nil
		},
		insertTranscriptCommits: func(_ context.Context, arg sqlc.InsertTranscriptCommitsParams) error {
			insertCalled = true
			insertPayload = arg.Commits
			return nil
		},
	}

	// New (shrunk) payload — 'c' is gone.
	newCommits := []schema.CommitInfo{
		{Hash: "a", Message: "first", AuthorName: "x", AuthorEmail: "x@e", AuthorTime: 1, CommitTime: 2},
		{Hash: "b", Message: "second", AuthorName: "y", AuthorEmail: "y@e", AuthorTime: 3, CommitTime: 4},
	}

	if err := persistCommits(context.Background(), q, transcriptID, newCommits); err != nil {
		t.Fatalf("persistCommits: %v", err)
	}

	// Prune is load-bearing: the DELETE must run, scoped to this transcript.
	if !deleteCalled {
		t.Fatal("DeleteTranscriptCommits must be called to prune the stale set")
	}
	if deletedID != transcriptID {
		t.Errorf("prune scoped to wrong transcript: got %v, want %v", deletedID, transcriptID)
	}

	if !insertCalled {
		t.Fatal("InsertTranscriptCommits must be called with the new set")
	}
	got := shasInPayload(t, insertPayload)
	if len(got) != 2 {
		t.Fatalf("batch payload SHAs: got %v, want [a b]", got)
	}
	set := map[string]bool{}
	for _, s := range got {
		set[s] = true
	}
	if !set["a"] || !set["b"] {
		t.Errorf("batch must contain a,b: got %v", got)
	}
	if set["c"] {
		t.Errorf("dropped SHA 'c' must be absent from the batch payload: got %v", got)
	}
}

// TestPersistCommits_DedupesBySHALastWins proves the regression fix: a payload
// with a repeated SHA is collapsed to one record (last-wins) BEFORE the batched
// INSERT, so the single-statement ON CONFLICT can never try to touch the same
// (transcript_id, sha) row twice (which Postgres rejects).
func TestPersistCommits_DedupesBySHALastWins(t *testing.T) {
	transcriptID := pgtype.UUID{Bytes: uuid.New(), Valid: true}

	var payload []byte
	q := &mockQuerier{
		deleteTranscriptCommits: func(_ context.Context, _ pgtype.UUID) error { return nil },
		insertTranscriptCommits: func(_ context.Context, arg sqlc.InsertTranscriptCommitsParams) error {
			payload = arg.Commits
			return nil
		},
	}

	commits := []schema.CommitInfo{
		{Hash: "x", Message: "old", AuthorTime: 1, CommitTime: 1},
		{Hash: "y", Message: "keep", AuthorTime: 2, CommitTime: 2},
		{Hash: "x", Message: "new", AuthorTime: 3, CommitTime: 3},
	}
	if err := persistCommits(context.Background(), q, transcriptID, commits); err != nil {
		t.Fatalf("persistCommits: %v", err)
	}

	var recs []commitJSONRecord
	if err := json.Unmarshal(payload, &recs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("records: got %d, want 2 (duplicate SHA collapsed)", len(recs))
	}
	bySha := map[string]commitJSONRecord{}
	for _, r := range recs {
		if _, dup := bySha[r.Sha]; dup {
			t.Errorf("SHA %q appears more than once after dedup", r.Sha)
		}
		bySha[r.Sha] = r
	}
	// last-wins: the second "x" (message "new") must be the survivor.
	x, ok := bySha["x"]
	if !ok || x.Message == nil || *x.Message != "new" {
		t.Errorf("dup SHA must keep its LAST occurrence (message 'new'): %+v", x)
	}
}

// TestPersistCommits_EmptyClearsWithoutInsert verifies that an empty payload
// prunes (DELETE) but issues no batched INSERT (nothing to write).
func TestPersistCommits_EmptyClearsWithoutInsert(t *testing.T) {
	transcriptID := pgtype.UUID{Bytes: uuid.New(), Valid: true}

	deleteCalled := false
	insertCalled := false
	q := &mockQuerier{
		deleteTranscriptCommits: func(_ context.Context, _ pgtype.UUID) error {
			deleteCalled = true
			return nil
		},
		insertTranscriptCommits: func(_ context.Context, _ sqlc.InsertTranscriptCommitsParams) error {
			insertCalled = true
			return nil
		},
	}

	if err := persistCommits(context.Background(), q, transcriptID, nil); err != nil {
		t.Fatalf("persistCommits: %v", err)
	}
	if !deleteCalled {
		t.Error("DELETE must run even for an empty payload (clears stale set)")
	}
	if insertCalled {
		t.Error("no batched INSERT should run for an empty payload")
	}
}

// TestPersistCommits_NullFieldsPreserved proves the JSONB encoding carries SQL
// NULLs (empty message/author and unset additions/deletions) as JSON null rather
// than zero values — the reason the batch uses jsonb_to_recordset over parallel
// typed arrays.
func TestPersistCommits_NullFieldsPreserved(t *testing.T) {
	transcriptID := pgtype.UUID{Bytes: uuid.New(), Valid: true}

	var payload []byte
	q := &mockQuerier{
		deleteTranscriptCommits: func(_ context.Context, _ pgtype.UUID) error { return nil },
		insertTranscriptCommits: func(_ context.Context, arg sqlc.InsertTranscriptCommitsParams) error {
			payload = arg.Commits
			return nil
		},
	}

	// Empty message/author and negative (=> unset) timestamps map to NULL.
	commits := []schema.CommitInfo{
		{Hash: "only", Message: "", AuthorName: "", AuthorEmail: "", AuthorTime: -1, CommitTime: -1},
	}
	if err := persistCommits(context.Background(), q, transcriptID, commits); err != nil {
		t.Fatalf("persistCommits: %v", err)
	}

	var recs []commitJSONRecord
	if err := json.Unmarshal(payload, &recs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("records: got %d, want 1", len(recs))
	}
	r := recs[0]
	if r.Message != nil || r.AuthorName != nil || r.AuthorEmail != nil {
		t.Errorf("empty string fields must marshal to null: %+v", r)
	}
	if r.Additions != nil || r.Deletions != nil {
		t.Errorf("unset additions/deletions must be null: %+v", r)
	}
	if r.AuthoredAt != nil || r.CommittedAt != nil {
		t.Errorf("unset timestamps must be null: %+v", r)
	}
}
