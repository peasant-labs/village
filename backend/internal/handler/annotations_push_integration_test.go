//go:build integration

package handler

// Integration parity test for the batched annotation upsert:
// drives the REAL UploadAnnotations handler (and thus the real
// BulkUpsertAnnotations jsonb_to_recordset SQL) against a real Postgres, and
// asserts the per-item Results[] + counts behave exactly like the prior per-row
// loop — created on first push, updated on re-push, idempotent row count.
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:55432/village_test?sslmode=disable" \
//	  go test -tags=integration ./internal/handler/...

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func bulkTestDatabaseURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return "postgres://test:test@localhost:55432/village_test?sslmode=disable"
}

// postBulkAnnotations drives UploadAnnotations for a specific owner against the
// real handler.
func postBulkAnnotations(t *testing.T, h *Handler, ownerID uuid.UUID, req schema.AnnotationPushRequest) schema.AnnotationPushResponse {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/annotations", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: ownerID, Username: "bulk-owner"}))
	w := httptest.NewRecorder()
	h.UploadAnnotations(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp schema.AnnotationPushResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestUploadAnnotations_BulkParity_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, bulkTestDatabaseURL(t))
	if err != nil {
		t.Skipf("cannot create pool (%v); set TEST_DATABASE_URL", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("cannot reach test database (%v); set TEST_DATABASE_URL", err)
	}
	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Owner for the FK (annotations.owner_id REFERENCES users).
	var ownerPg pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES ($1, $2, $3) RETURNING id
	`, int64(770001), "bulk-owner", "770001").Scan(&ownerPg); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	ownerID := uuid.UUID(ownerPg.Bytes)
	defer func() {
		if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", ownerPg); err != nil {
			t.Errorf("cleanup owner: %v", err)
		}
	}()

	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	conf := 0.9
	items := []schema.AnnotationPushItem{
		{ContentHash: "bulk-h1", TargetKind: schema.TargetSession, TypeID: "quality.session_outcome", Value: "resolved", SessionID: strPtr("sess-1")},
		{ContentHash: "bulk-h2", TargetKind: schema.TargetEntry, TypeID: "quality.frustration_signal", Value: "detected",
			EntryTarget: &schema.AnnotationEntryTarget{SessionID: "sess-1", EntryIndex: 3, EndIndex: 4}},
		{ContentHash: "bulk-h3", TargetKind: schema.TargetSession, TypeID: "quality.session_outcome", Value: "partial",
			SessionID: strPtr("sess-2"), Confidence: &conf, AnnotatorName: "agent-x"},
	}
	req := schema.AnnotationPushRequest{Annotations: items}

	// Run #1: all created.
	r1 := postBulkAnnotations(t, h, ownerID, req)
	if r1.Created != 3 || r1.Updated != 0 || r1.Errors != 0 {
		t.Errorf("run1 counts: got created=%d updated=%d errors=%d, want 3/0/0", r1.Created, r1.Updated, r1.Errors)
	}
	assertResultStatuses(t, r1, map[string]schema.AnnotationPushStatus{
		"bulk-h1": schema.PushStatusCreated, "bulk-h2": schema.PushStatusCreated, "bulk-h3": schema.PushStatusCreated,
	})

	if n := countOwnerAnnotations(t, ctx, pool, ownerPg); n != 3 {
		t.Fatalf("after run1: %d annotations, want 3", n)
	}

	// Run #2: same payload → all updated (manifest skip-gate relies on this).
	r2 := postBulkAnnotations(t, h, ownerID, req)
	if r2.Updated != 3 || r2.Created != 0 || r2.Errors != 0 {
		t.Errorf("run2 counts: got created=%d updated=%d errors=%d, want 0/3/0", r2.Created, r2.Updated, r2.Errors)
	}
	assertResultStatuses(t, r2, map[string]schema.AnnotationPushStatus{
		"bulk-h1": schema.PushStatusUpdated, "bulk-h2": schema.PushStatusUpdated, "bulk-h3": schema.PushStatusUpdated,
	})

	// Idempotent: re-push did not grow the row set.
	if n := countOwnerAnnotations(t, ctx, pool, ownerPg); n != 3 {
		t.Errorf("after run2: %d annotations, want 3 (idempotent)", n)
	}

	// Spot-check a stored row: the entry-target columns + value persisted.
	var value string
	var entryIdx pgtype.Int4
	if err := pool.QueryRow(ctx,
		"SELECT value, entry_index FROM annotations WHERE content_hash = $1 AND owner_id = $2",
		"bulk-h2", ownerPg).Scan(&value, &entryIdx); err != nil {
		t.Fatalf("read bulk-h2: %v", err)
	}
	if value != "detected" || !entryIdx.Valid || entryIdx.Int32 != 3 {
		t.Errorf("bulk-h2 stored wrong: value=%q entry_index=%+v", value, entryIdx)
	}
}

func assertResultStatuses(t *testing.T, resp schema.AnnotationPushResponse, want map[string]schema.AnnotationPushStatus) {
	t.Helper()
	got := map[string]schema.AnnotationPushStatus{}
	for _, r := range resp.Results {
		got[r.ContentHash] = r.Status
	}
	for hash, wantStatus := range want {
		if got[hash] != wantStatus {
			t.Errorf("result %s: got %q, want %q", hash, got[hash], wantStatus)
		}
	}
}

func countOwnerAnnotations(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM annotations WHERE owner_id = $1", owner).Scan(&n); err != nil {
		t.Fatalf("count annotations: %v", err)
	}
	return n
}
