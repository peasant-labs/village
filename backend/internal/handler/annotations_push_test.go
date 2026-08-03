package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// decodePushResp decodes the AnnotationPushResponse body.
func decodePushResp(t *testing.T, w *httptest.ResponseRecorder) schema.AnnotationPushResponse {
	t.Helper()
	var resp schema.AnnotationPushResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode push response: %v (body: %s)", err, w.Body.String())
	}
	return resp
}

// TestUploadAnnotations_BulkResultsParity asserts the bulk path produces the
// SAME observable per-item Results[] the old per-row loop did: one result per
// input item, in order, with the correct content_hash and created/updated status
// derived from the query's RETURNING.
func TestUploadAnnotations_BulkResultsParity(t *testing.T) {
	var gotItems []byte
	var gotOwner pgtype.UUID
	q := &mockQuerier{
		bulkUpsertAnnotations: func(_ context.Context, arg sqlc.BulkUpsertAnnotationsParams) ([]sqlc.BulkUpsertAnnotationsRow, error) {
			gotItems = arg.Items
			gotOwner = arg.OwnerID
			// h1 + h3 inserted (created), h2 already existed (updated). Order is
			// intentionally different from input to prove the handler maps by hash.
			return []sqlc.BulkUpsertAnnotationsRow{
				{ContentHash: "h3", Created: true},
				{ContentHash: "h1", Created: true},
				{ContentHash: "h2", Created: false},
			}, nil
		},
	}
	h := newTestHandler(q, nil)

	w := postAnnotationPush(t, h, schema.AnnotationPushRequest{
		Annotations: []schema.AnnotationPushItem{
			{ContentHash: "h1", TargetKind: schema.TargetSession, SessionID: strPtr("s"), TypeID: "t", Value: "v"},
			{ContentHash: "h2", TargetKind: schema.TargetSession, SessionID: strPtr("s"), TypeID: "t", Value: "v"},
			{ContentHash: "h3", TargetKind: schema.TargetEntry, TypeID: "t", Value: "v",
				EntryTarget: &schema.AnnotationEntryTarget{SessionID: "s", EntryIndex: 2, EndIndex: 3}},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	resp := decodePushResp(t, w)
	if resp.Created != 2 || resp.Updated != 1 || resp.Errors != 0 {
		t.Errorf("counts: got created=%d updated=%d errors=%d, want 2/1/0", resp.Created, resp.Updated, resp.Errors)
	}
	// One result per input item, IN INPUT ORDER, with the right status.
	want := []struct {
		hash   string
		status schema.AnnotationPushStatus
	}{
		{"h1", schema.PushStatusCreated},
		{"h2", schema.PushStatusUpdated},
		{"h3", schema.PushStatusCreated},
	}
	if len(resp.Results) != len(want) {
		t.Fatalf("results: got %d, want %d", len(resp.Results), len(want))
	}
	for i, wnt := range want {
		if resp.Results[i].ContentHash != wnt.hash || resp.Results[i].Status != wnt.status {
			t.Errorf("result[%d]: got {%s,%s}, want {%s,%s}", i,
				resp.Results[i].ContentHash, resp.Results[i].Status, wnt.hash, wnt.status)
		}
	}

	// Owner is applied as the scalar, not per-record.
	if !gotOwner.Valid {
		t.Error("bulk upsert must be owner-scoped (valid owner id)")
	}
	// The batched payload carries the records with the null/entry mapping intact.
	var recs []bulkAnnotationRecord
	if err := json.Unmarshal(gotItems, &recs); err != nil {
		t.Fatalf("decode items payload: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("payload records: got %d, want 3", len(recs))
	}
	byHash := map[string]bulkAnnotationRecord{}
	for _, r := range recs {
		byHash[r.ContentHash] = r
	}
	if byHash["h1"].SessionID == nil || *byHash["h1"].SessionID != "s" {
		t.Errorf("h1 session target not mapped: %+v", byHash["h1"])
	}
	h3 := byHash["h3"]
	if h3.EntryIndex == nil || *h3.EntryIndex != 2 || h3.EntryEndIndex == nil || *h3.EntryEndIndex != 3 {
		t.Errorf("h3 entry target not mapped: %+v", h3)
	}
	if h3.EntrySessionID == nil || *h3.EntrySessionID != "s" {
		t.Errorf("h3 entry session id not mapped: %+v", h3)
	}
}

// TestUploadAnnotations_BulkBatchError: a batch-level DB failure marks every
// batched item as an error (client re-pushes, fail-safe) — the push still 200s.
func TestUploadAnnotations_BulkBatchError(t *testing.T) {
	q := &mockQuerier{
		bulkUpsertAnnotations: func(_ context.Context, _ sqlc.BulkUpsertAnnotationsParams) ([]sqlc.BulkUpsertAnnotationsRow, error) {
			return nil, errors.New("db: connection reset")
		},
	}
	h := newTestHandler(q, nil)

	w := postAnnotationPush(t, h, schema.AnnotationPushRequest{
		Annotations: []schema.AnnotationPushItem{
			{ContentHash: "h1", TargetKind: schema.TargetSession, SessionID: strPtr("s"), TypeID: "t", Value: "v"},
			{ContentHash: "h2", TargetKind: schema.TargetSession, SessionID: strPtr("s"), TypeID: "t", Value: "v"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	resp := decodePushResp(t, w)
	if resp.Errors != 2 || resp.Created != 0 || resp.Updated != 0 {
		t.Errorf("counts on batch error: got created=%d updated=%d errors=%d, want 0/0/2",
			resp.Created, resp.Updated, resp.Errors)
	}
	for i, res := range resp.Results {
		if res.Status != schema.PushStatusError {
			t.Errorf("result[%d] status: got %s, want error", i, res.Status)
		}
	}
}

// TestUploadAnnotations_DedupesDuplicateContentHash: a duplicate content_hash in
// one request is collapsed to a single record for the statement (so the single
// ON CONFLICT can't touch a row twice), but a Result is still emitted per input
// item.
func TestUploadAnnotations_DedupesDuplicateContentHash(t *testing.T) {
	var recCount int
	q := &mockQuerier{
		bulkUpsertAnnotations: func(_ context.Context, arg sqlc.BulkUpsertAnnotationsParams) ([]sqlc.BulkUpsertAnnotationsRow, error) {
			var recs []bulkAnnotationRecord
			if err := json.Unmarshal(arg.Items, &recs); err != nil {
				t.Fatalf("decode items: %v", err)
			}
			recCount = len(recs)
			return []sqlc.BulkUpsertAnnotationsRow{{ContentHash: "dup", Created: true}}, nil
		},
	}
	h := newTestHandler(q, nil)

	w := postAnnotationPush(t, h, schema.AnnotationPushRequest{
		Annotations: []schema.AnnotationPushItem{
			{ContentHash: "dup", TargetKind: schema.TargetSession, SessionID: strPtr("s"), TypeID: "t", Value: "a"},
			{ContentHash: "dup", TargetKind: schema.TargetSession, SessionID: strPtr("s"), TypeID: "t", Value: "b"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if recCount != 1 {
		t.Errorf("batched records: got %d, want 1 (deduped by content_hash)", recCount)
	}
	resp := decodePushResp(t, w)
	if len(resp.Results) != 2 {
		t.Errorf("results: got %d, want 2 (one per input item)", len(resp.Results))
	}
}

// TestUploadAnnotations_EmptyAnnotationsSkipsBulk: no annotations → the bulk
// query is never called (the mock panics if it were).
func TestUploadAnnotations_EmptyAnnotationsSkipsBulk(t *testing.T) {
	h := newTestHandler(&mockQuerier{}, nil)
	w := postAnnotationPush(t, h, schema.AnnotationPushRequest{Annotations: []schema.AnnotationPushItem{}})
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	resp := decodePushResp(t, w)
	if len(resp.Results) != 0 || resp.Created != 0 {
		t.Errorf("empty push: got %d results / %d created, want 0/0", len(resp.Results), resp.Created)
	}
}
