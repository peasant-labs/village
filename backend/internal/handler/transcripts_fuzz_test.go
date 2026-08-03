package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/handler/testfixtures"
)

func FuzzPublishTranscriptMetadata(f *testing.F) {
	// Seed corpus with valid JSON from fixtures
	minimalJSON, _ := json.Marshal(ValidMinimal.ToSchema())
	fullJSON, _ := json.Marshal(ValidFull.ToSchema())
	f.Add(string(minimalJSON))
	f.Add(string(fullJSON))

	// Load adversarial payloads from fixtures/adversarial.yaml
	adv, err := testfixtures.LoadAdversarialFixtures("testfixtures/testdata/fixtures/adversarial.yaml")
	if err == nil {
		for _, p := range adv.InjectionPayloads {
			f.Add(p.Payload)
		}
		for _, p := range adv.BoundaryPayloads {
			f.Add(fmt.Sprintf("%v", p.Value))
		}
		for _, p := range adv.JSONPayloads {
			f.Add(p.Value)
		}
		for _, p := range adv.SpecialValues {
			f.Add(p.Value)
		}
	}

	f.Fuzz(func(t *testing.T, metadataStr string) {
		// Mock setup
		mq := &mockQuerier{
			getTranscriptIDByOwnerAndLocalID: func(ctx context.Context, arg sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
				return pgtype.UUID{}, errors.New("not found")
			},
			createTranscript: func(ctx context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
				return sqlc.Transcript{}, nil
			},
		}
		mockS3 := &mockTranscriptBlobStore{}
		h := newTestHandler(mq, mockS3)

		// Create multipart request
		transcriptContent := `[{"role": "user", "content": "hello"}]`
		body, boundary := multipartBody(t, map[string]string{
			"metadata": metadataStr,
		}, transcriptContent)

		r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
		r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
		r = r.WithContext(withTestUser(r.Context()))
		w := httptest.NewRecorder()

		// Execute handler
		h.PublishTranscript(w, r)

		// Verification: The handler should not panic.
		if w.Code == http.StatusInternalServerError {
			t.Errorf("metadata processed but returned 500 Internal Server Error: %s", metadataStr)
		}
	})
}
