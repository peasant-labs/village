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
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/storage"
)

func TestObservedModelRealPostgresMinIOLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("migrate observed-model lifecycle database: %v", err)
	}
	owner := pullInsertUser(t, ctx, pool, 99887767, "observed-model-lifecycle-owner")
	defer cleanupOwners(t, ctx, pool, owner)

	cfg := &config.Config{S3Endpoint: os.Getenv("TEST_S3_ENDPOINT"), S3Bucket: os.Getenv("TEST_S3_BUCKET"), S3AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"), S3SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), S3UsePathStyle: true, BaseURL: "https://example.test", FrontendURL: "https://app.example.test", JWTSecret: "integration-only-jwt-secret-0123456789abcdef"}
	keyring, err := config.ParseTranscriptKeyring(os.Getenv("TRANSCRIPT_KEK_ACTIVE_VERSION"), os.Getenv("TRANSCRIPT_KEK_KEYRING"))
	if err != nil {
		t.Fatalf("load observed-model lifecycle keyring: %v", err)
	}
	objects, err := storage.NewS3ObjectStore(cfg)
	if err != nil {
		t.Fatalf("compose observed-model lifecycle object store: %v", err)
	}
	blobs, err := storage.NewEncryptedTranscriptStore(objects, keyring)
	if err != nil {
		t.Fatalf("compose observed-model lifecycle encrypted store: %v", err)
	}
	h := New(cfg, pool, blobs)

	localID := uuid.NewString()
	metadata := schema.PublishRequest{Identity: schema.SessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2}, Model: schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "anthropic/Claude-Fable-5"}, Timestamp: schema.TimestampInfo{Start: 1700000000000, End: 1700000001000}, Source: schema.SourceInfo{FilePath: "/integration/observed-model.jsonl", Format: "jsonl"}, Project: schema.ProjectContext{Hash: testProjectHash, Name: "observed-model-project"}}
	metadataJSON, _ := json.Marshal(metadata)
	content := observedModelFixtureContent(t, "enriched_repeated_change_and_omission")
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadataJSON)}, string(content))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	request.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	request = request.WithContext(context.WithValue(request.Context(), UserContextKey, &AuthUser{ID: uuidFromPg(owner), Username: "observed-model-lifecycle-owner"}))
	response := httptest.NewRecorder()
	h.PublishTranscript(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("observed-model mounted publish status=%d body=%s", response.Code, response.Body.String())
	}
	var published struct {
		Transcript transcriptResponse `json:"transcript"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &published); err != nil {
		t.Fatalf("decode observed-model mounted publish response: %v", err)
	}
	id := uuidFromPg(published.Transcript.ID)
	defer func() {
		row, queryErr := h.queries.GetTranscriptByID(ctx, published.Transcript.ID)
		if queryErr != nil {
			t.Errorf("load current observed-model transcript descriptor for object cleanup: %v", queryErr)
			return
		}
		descriptor, descriptorErr := descriptorFromTranscript(row)
		if descriptorErr != nil {
			t.Errorf("decode current observed-model transcript descriptor for object cleanup: %v", descriptorErr)
			return
		}
		if deleteErr := blobs.Delete(ctx, descriptor); deleteErr != nil {
			t.Errorf("delete current observed-model transcript object during integration cleanup: %v", deleteErr)
		}
	}()

	webRequest := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id.String()+"/content", nil)
	webRequest = withChiURLParam(webRequest, "id", id.String())
	webRequest = webRequest.WithContext(withUserID(webRequest.Context(), uuidFromPg(owner)))
	webResponse := httptest.NewRecorder()
	h.GetTranscriptContent(webResponse, webRequest)
	if webResponse.Code != http.StatusOK {
		t.Fatalf("observed-model mounted web read status=%d body=%s", webResponse.Code, webResponse.Body.String())
	}
	var payload schema.SessionDetailPayload
	if err := json.Unmarshal(webResponse.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode observed-model mounted web payload: %v", err)
	}
	fixtureCase := requireObservedModelPreservationCase(t, "enriched_repeated_change_and_omission")
	if err := fixtureCase.assertObservedModels(&payload); err != nil {
		t.Fatal(err)
	}

	pullRequest := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+id.String()+"/content", nil)
	pullRequest = withChiURLParam(pullRequest, "id", id.String())
	pullRequest = pullRequest.WithContext(withUserID(pullRequest.Context(), uuidFromPg(owner)))
	pullResponse := httptest.NewRecorder()
	h.GetPullTranscriptContent(pullResponse, pullRequest)
	if pullResponse.Code != http.StatusOK {
		t.Fatalf("observed-model mounted pull status=%d body=%s", pullResponse.Code, pullResponse.Body.String())
	}
	var envelope schema.TranscriptContent
	if err := json.Unmarshal(pullResponse.Body.Bytes(), &envelope); err != nil || envelope.SessionDetail == nil {
		t.Fatalf("decode observed-model mounted pull envelope: %v", err)
	}
	if err := fixtureCase.assertObservedModels(envelope.SessionDetail); err != nil {
		t.Fatal(err)
	}
}
