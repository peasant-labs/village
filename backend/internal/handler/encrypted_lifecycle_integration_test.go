//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

func TestMountedEncryptedTranscriptLifecycleRealPostgresMinIO(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("migrate mounted encrypted lifecycle database: %v", err)
	}
	owner := pullInsertUser(t, ctx, pool, 99887766, "encrypted-lifecycle-owner")
	defer cleanupOwners(t, ctx, pool, owner)

	cfg := &config.Config{S3Endpoint: os.Getenv("TEST_S3_ENDPOINT"), S3Bucket: os.Getenv("TEST_S3_BUCKET"), S3AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"), S3SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), S3UsePathStyle: true, BaseURL: "https://example.test", FrontendURL: "https://app.example.test", JWTSecret: "integration-only-jwt-secret-0123456789abcdef"}
	keyring, err := config.ParseTranscriptKeyring(os.Getenv("TRANSCRIPT_KEK_ACTIVE_VERSION"), os.Getenv("TRANSCRIPT_KEK_KEYRING"))
	if err != nil {
		t.Fatalf("load integration transcript keyring: %v", err)
	}
	objects, err := storage.NewS3ObjectStore(cfg)
	if err != nil {
		t.Fatalf("compose integration object store: %v", err)
	}
	blobs, err := storage.NewEncryptedTranscriptStore(objects, keyring)
	if err != nil {
		t.Fatalf("compose integration encrypted transcript store: %v", err)
	}
	h := New(cfg, pool, blobs)

	localID := uuid.New().String()
	metadata := schema.PublishRequest{Identity: schema.SessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2}, Model: schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "integration-model"}, Timestamp: schema.TimestampInfo{Start: 1700000000000, End: 1700000001000}, Source: schema.SourceInfo{FilePath: "/integration/transcript.jsonl", Format: "jsonl"}, Project: schema.ProjectContext{Hash: testProjectHash, Name: "integration-project"}}
	metadataJSON, _ := json.Marshal(metadata)
	content := currentEnvelopeJSON(t, string(schema.HarnessClaudeCode))
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadataJSON)}, string(content))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &AuthUser{ID: uuidFromPg(owner), Username: "encrypted-lifecycle-owner"}))
	w := httptest.NewRecorder()
	h.PublishTranscript(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("mounted encrypted publish status=%d body=%s", w.Code, w.Body.String())
	}
	var published struct {
		Transcript transcriptResponse `json:"transcript"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &published); err != nil {
		t.Fatalf("decode mounted publish response: %v", err)
	}
	id := uuidFromPg(published.Transcript.ID)
	defer purgeAuditRows(t, ctx, pool, []pgtype.UUID{toPgUUID(id)})

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin pending identity fixture transaction: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatalf("install fixed writer markers for pending identity fixture: %v", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE transcripts SET content_hash = NULL, blob_size_bytes = NULL WHERE id = $1", id); err != nil {
		t.Fatalf("create pending identity fixture row: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit pending identity fixture row: %v", err)
	}
	queries := sqlc.New(pool)
	pendingRows, err := queries.ListTranscriptsMissingContentHash(ctx)
	if err != nil {
		t.Fatalf("list complete pending identity rows: %v", err)
	}
	var foundPending bool
	for _, row := range pendingRows {
		if uuidFromPg(row.ID) == id {
			foundPending = row.BlobKey != "" && len(row.WrappedDataKey) > 0 && row.EncryptionAlgorithm != "" && row.KeyVersion > 0 && !row.BlobSizeBytes.Valid
		}
	}
	if !foundPending {
		t.Fatal("pending identity query omitted the complete descriptor or nullable prior size")
	}
	rewrapRows, err := queries.ListTranscriptDescriptorsForRewrap(ctx, sqlc.ListTranscriptDescriptorsForRewrapParams{ActiveKeyVersion: 2, AfterKeyVersion: 0, AfterID: toPgUUID(uuid.Nil), BatchSize: 100})
	if err != nil {
		t.Fatalf("list bounded rewrap descriptors: %v", err)
	}
	var foundRewrap bool
	for _, row := range rewrapRows {
		if uuidFromPg(row.ID) == id {
			foundRewrap = row.KeyVersion == 1 && row.BlobKey != "" && len(row.WrappedDataKey) > 0
		}
	}
	if !foundRewrap {
		t.Fatal("rewrap keyset query omitted the below-active complete descriptor")
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin identity restore transaction: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatalf("install fixed writer markers for identity restore: %v", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE transcripts SET content_hash = $2, blob_size_bytes = $3 WHERE id = $1", id, schema.ComputeTranscriptHash(content), len(content)); err != nil {
		t.Fatalf("restore known identity after query proof: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit identity restore: %v", err)
	}

	webReq := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id.String()+"/content", nil)
	webReq = withChiURLParam(webReq, "id", id.String())
	webReq = webReq.WithContext(withUserID(webReq.Context(), uuidFromPg(owner)))
	web := httptest.NewRecorder()
	h.GetTranscriptContent(web, webReq)
	if web.Code != http.StatusOK || web.Body.Len() == 0 {
		t.Fatalf("mounted authenticated web read status=%d body=%s", web.Code, web.Body.String())
	}

	pullReq := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+id.String()+"/content", nil)
	pullReq = withChiURLParam(pullReq, "id", id.String())
	pullReq = pullReq.WithContext(withUserID(pullReq.Context(), uuidFromPg(owner)))
	pull := httptest.NewRecorder()
	h.GetPullTranscriptContent(pull, pullReq)
	if pull.Code != http.StatusOK || pull.Header().Get("ETag") == "" {
		t.Fatalf("mounted pull status=%d etag=%q body=%s", pull.Code, pull.Header().Get("ETag"), pull.Body.String())
	}

	conditional := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+id.String()+"/content", nil)
	conditional.Header.Set("If-None-Match", pull.Header().Get("ETag"))
	conditional = withChiURLParam(conditional, "id", id.String())
	conditional = conditional.WithContext(withUserID(conditional.Context(), uuidFromPg(owner)))
	notModified := httptest.NewRecorder()
	h.GetPullTranscriptContent(notModified, conditional)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("mounted conditional pull status=%d body=%s", notModified.Code, notModified.Body.String())
	}

	var descriptorRow sqlc.Transcript
	descriptorRow, err = queries.GetTranscriptByID(ctx, toPgUUID(id))
	if err != nil {
		t.Fatalf("load descriptor before mounted delete: %v", err)
	}
	descriptor, err := descriptorFromTranscript(descriptorRow)
	if err != nil {
		t.Fatalf("map descriptor before mounted delete: %v", err)
	}
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/transcripts/"+id.String(), nil)
	deleteReq = withChiURLParam(deleteReq, "id", id.String())
	deleteReq = deleteReq.WithContext(withUserID(deleteReq.Context(), uuidFromPg(owner)))
	deleted := httptest.NewRecorder()
	h.DeleteTranscript(deleted, deleteReq)
	if deleted.Code != http.StatusOK {
		t.Fatalf("mounted row-first delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if _, err := queries.GetTranscriptByID(ctx, toPgUUID(id)); err == nil {
		t.Fatal("mounted delete left transcript row present")
	}
	if err := blobs.Delete(ctx, descriptor); err != nil {
		t.Fatalf("idempotent ciphertext cleanup after mounted delete: %v", err)
	}
}
