package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// A committed delete removes exactly the objects the deleted row named. When
// those returned columns cannot be mapped, nothing is deleted on a guess: the
// delete answers 500, like an unmappable descriptor before the transaction.
func TestDeleteRefusesUnmappableReturnedObjects(t *testing.T) {
	const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
	owner, id := uuid.New(), uuid.New()
	q := publicTranscriptQuerier(key)
	base := q.getTranscriptByID
	q.getTranscriptByID = func(ctx context.Context, tid pgtype.UUID) (sqlc.Transcript, error) {
		row, err := base(ctx, tid)
		row.OwnerID = pgUUIDFrom(owner)
		return row, err
	}
	q.deleteTranscriptReturningDescriptor = func(context.Context, pgtype.UUID) (sqlc.DeleteTranscriptReturningDescriptorRow, error) {
		// A metadata key without its hash and size.
		return sqlc.DeleteTranscriptReturningDescriptorRow{BlobKey: key, WrappedDataKey: []byte("test-wrapped-key"), EncryptionAlgorithm: "aes-256-gcm-random-nonce-v1", KeyVersion: 1,
			PublishMetadataKey: pgtype.Text{String: "transcripts/20000000-0000-4000-8000-000000000002.bin", Valid: true}}, nil
	}
	store := &mockTranscriptBlobStore{}
	h := newTestHandler(q, store)
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/transcripts/"+id.String(), nil)
	r = withChiURLParam(r, "id", id.String())
	r = r.WithContext(withUserID(r.Context(), owner))
	w := httptest.NewRecorder()
	h.DeleteTranscript(w, r)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "only partly set") {
		t.Fatalf("status %d body %s, want the 500 naming the unmappable metadata columns", w.Code, w.Body.String())
	}
	if len(store.deletedKeys) != 0 {
		t.Fatalf("deleted %v on unmappable returned columns, want nothing deleted", store.deletedKeys)
	}
}
