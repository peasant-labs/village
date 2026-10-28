//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

type countedEncryptedStore struct {
	storage.TranscriptBlobStore
	reads, writes int
}

func (s *countedEncryptedStore) Read(ctx context.Context, id uuid.UUID, descriptor storage.BlobDescriptor, loaded storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	s.reads++
	return s.TranscriptBlobStore.Read(ctx, id, descriptor, loaded)
}

func (s *countedEncryptedStore) Write(ctx context.Context, id uuid.UUID, content []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	s.writes++
	return s.TranscriptBlobStore.Write(ctx, id, content)
}

// The real publication route stores the already-redacted payload encrypted;
// the existing pull-list route must only read that generation after access.
func TestTranscriptUnverifiedPullsEncryptedPublication(t *testing.T) {
	fixture, err := decodeFixtureDocument[struct {
		Content map[string]string    `yaml:"content"`
		Cases   []unverifiedPullCase `yaml:"cases"`
	}](transcriptUnverifiedPullsYAML)
	if err != nil {
		t.Fatal(err)
	}
	pool := govTestPool(t)
	defer pool.Close()
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	owner := pullInsertUser(t, ctx, pool, 99887765, "unverified-evidence-owner")
	defer cleanupOwners(t, ctx, pool, owner)
	blobs := &countedEncryptedStore{TranscriptBlobStore: authoritativeTestBlobStore(t)}
	h := New(minimalConfig(), pool, blobs)
	metadata := schema.PublishRequest{Identity: schema.SessionIdentity{SessionID: schema.SessionID(uuid.NewString()), SchemaVersion: 2}, Model: schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "integration-model"}, Timestamp: schema.TimestampInfo{Start: 1700000000000, End: 1700000001000}, Source: schema.SourceInfo{FilePath: "/synthetic/transcript.jsonl", Format: "jsonl"}, Project: schema.ProjectContext{Hash: testProjectHash, Name: "synthetic-project"}}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(encoded)}, fixture.Content["current"])
	req := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req = req.WithContext(withUserID(req.Context(), uuidFromPg(owner)))
	w := httptest.NewRecorder()
	h.PublishTranscript(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("publish status %d: %s", w.Code, w.Body.String())
	}
	var published struct {
		Transcript transcriptResponse `json:"transcript"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	id := uuidFromPg(published.Transcript.ID)
	defer purgeAuditRows(t, ctx, pool, []pgtype.UUID{toPgUUID(id)})
	queries := sqlc.New(pool)
	before, err := queries.GetTranscriptByID(ctx, toPgUUID(id))
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := descriptorFromTranscript(before)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := blobs.Delete(ctx, descriptor); err != nil {
			t.Errorf("clean up encrypted fixture: %v", err)
		}
	}()
	read := func(user *AuthUser) *httptest.ResponseRecorder {
		r := withChiURLParam(httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id.String()+"/pulls", nil), "id", id.String())
		if user != nil {
			r = r.WithContext(context.WithValue(r.Context(), UserContextKey, user))
		}
		out := httptest.NewRecorder()
		h.ListTranscriptPullRequests(out, r)
		return out
	}
	reads, writes := blobs.reads, blobs.writes
	denied := read(nil)
	if denied.Code != http.StatusNotFound || blobs.reads != reads {
		t.Fatalf("denied status=%d reads=%d, want 404 and no blob read", denied.Code, blobs.reads-reads)
	}
	allowed := read(&AuthUser{ID: uuidFromPg(owner)})
	if allowed.Code != http.StatusOK {
		t.Fatalf("owner read status %d: %s", allowed.Code, allowed.Body.String())
	}
	var response schema.VillageTranscriptPullRequestsResponse
	if err := json.Unmarshal(allowed.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.UnverifiedPullRequests == nil || len(*response.UnverifiedPullRequests) != 1 || (*response.UnverifiedPullRequests)[0].Source.TurnIndex != 42 || (*response.UnverifiedPullRequests)[0].Source.ToolCallIndex != 1 {
		t.Fatalf("wrong source: %s", allowed.Body.String())
	}
	after, err := queries.GetTranscriptByID(ctx, toPgUUID(id))
	if err != nil {
		t.Fatal(err)
	}
	if blobs.writes != writes || before.BlobKey != after.BlobKey || before.ContentHash != after.ContentHash {
		t.Fatal("projection changed the stored generation")
	}
}
