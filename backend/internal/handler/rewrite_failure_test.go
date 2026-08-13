package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/observed_model_preservation/rewrite_failures.yaml
var rewriteFailureFixtureYAML []byte

var rewriteFailureNames = [...]string{"legacy_blob_write_failure_serves", "enriched_blob_write_failure_denies", "legacy_cas_failure_serves", "enriched_cas_failure_denies"}

type rewriteFailureFixture struct {
	Cases []rewriteFailureCase `yaml:"cases"`
}
type rewriteFailureCase struct {
	Name         string `yaml:"name"`
	ContentCase  string `yaml:"contentCase"`
	Failure      string `yaml:"failure"`
	WantStatus   int    `yaml:"wantStatus"`
	WantObserved bool   `yaml:"wantObserved"`
	WantLog      string `yaml:"wantLog"`
	WantError    string `yaml:"wantError"`
}

type failingRewriteStore struct {
	*fakeBlobStore
	failWrite bool
}

func (s failingRewriteStore) Write(ctx context.Context, id uuid.UUID, body []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	if s.failWrite {
		return storage.BlobDescriptor{}, storage.ContentIdentity{}, errors.New("injected object write failure")
	}
	return generationTestBlobStore{s.fakeBlobStore}.Write(ctx, id, body)
}

func TestMountedRewriteFailurePolicy(t *testing.T) {
	for _, tc := range loadRewriteFailureFixtures(t) {
		t.Run(tc.Name, func(t *testing.T) {
			const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
			store := newFakeBlobStore()
			content := observedModelFixtureContent(t, tc.ContentCase)
			var envelope schema.TranscriptContent
			if err := json.Unmarshal(content, &envelope); err != nil || envelope.SessionDetail == nil {
				t.Fatalf("decode fixture envelope: %v", err)
			}
			bare, err := json.Marshal(envelope.SessionDetail)
			if err != nil {
				t.Fatal(err)
			}
			store.put(key, bare)
			q := publicTranscriptQuerier(key)
			q.compareAndSwapTranscriptBlob = func(context.Context, sqlc.CompareAndSwapTranscriptBlobParams) (sqlc.Transcript, error) {
				if tc.Failure == "cas" {
					return sqlc.Transcript{}, errors.New("injected CAS failure")
				}
				return sqlc.Transcript{}, nil
			}
			h := newTestHandler(q, failingRewriteStore{fakeBlobStore: store, failWrite: tc.Failure == "blob_write"})
			var logs bytes.Buffer
			old := log.Writer()
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(old) })
			response := getContent(t, h, mustFixtureUUID(t))
			if response.Code != tc.WantStatus {
				t.Fatalf("status=%d body=%s want=%d", response.Code, response.Body.String(), tc.WantStatus)
			}
			if tc.WantLog != "" && !strings.Contains(logs.String(), tc.WantLog) {
				t.Fatalf("retryable signal missing from %q", logs.String())
			}
			if tc.WantError != "" && !strings.Contains(decodeError(t, response.Body.Bytes()), tc.WantError) {
				t.Fatalf("actionable error missing %q: %s", tc.WantError, response.Body.String())
			}
			if tc.WantStatus == 200 {
				var payload schema.SessionDetailPayload
				if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				if payloadCarriesObservedModels(&payload) != tc.WantObserved {
					t.Fatalf("observed evidence=%v want=%v", payloadCarriesObservedModels(&payload), tc.WantObserved)
				}
			}
			q.getTranscriptByID(context.Background(), pgtype.UUID{Bytes: mustFixtureUUID(t), Valid: true})
			if _, ok := store.blobs[key]; !ok {
				t.Fatal("old generation was not retained")
			}
		})
	}
}

func loadRewriteFailureFixtures(t *testing.T) []rewriteFailureCase {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(rewriteFailureFixtureYAML))
	decoder.KnownFields(true)
	var fixture rewriteFailureFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("fixture must have one document: %v", err)
	}
	if len(fixture.Cases) != len(rewriteFailureNames) {
		t.Fatalf("case count=%d want=%d", len(fixture.Cases), len(rewriteFailureNames))
	}
	required := map[string]bool{}
	for _, name := range rewriteFailureNames {
		required[name] = true
	}
	seen := map[string]bool{}
	for _, tc := range fixture.Cases {
		if !required[tc.Name] || seen[tc.Name] {
			t.Fatalf("unknown or duplicate case %q", tc.Name)
		}
		seen[tc.Name] = true
	}
	return fixture.Cases
}
