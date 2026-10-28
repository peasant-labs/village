package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/transcript-unverified-pulls.yaml
var transcriptUnverifiedPullsYAML []byte

type unverifiedPullCase struct {
	Name       string `yaml:"name"`
	Visibility string `yaml:"visibility"`
	Content    string `yaml:"content"`
	Status     int    `yaml:"status"`
	Reads      int    `yaml:"reads"`
	Evidence   bool   `yaml:"evidence"`
	CallIndex  int    `yaml:"call_index"`
	CallID     string `yaml:"call_id"`
	FailRead   bool   `yaml:"fail_read"`
}

type failingEvidenceStore struct{ *fakeBlobStore }

func (s failingEvidenceStore) Read(context.Context, uuid.UUID, storage.BlobDescriptor, storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	s.downloads++
	return nil, storage.ContentIdentity{}, errors.New("SECRET-MARKER")
}

func TestTranscriptUnverifiedPullsMountedRead(t *testing.T) {
	fixture, err := decodeFixtureDocument[struct {
		Content map[string]string    `yaml:"content"`
		Cases   []unverifiedPullCase `yaml:"cases"`
	}](transcriptUnverifiedPullsYAML)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]struct{}{}
	for _, c := range fixture.Cases {
		if _, exists := names[c.Name]; exists {
			t.Fatalf("duplicate case %q", c.Name)
		}
		names[c.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "transcript-unverified-pulls", names, []string{"denied-before-blob-read", "anonymous-published-evidence-and-attachment", "legacy-normalized-without-write", "corrupt-content-is-retryable", "blob-failure-is-retryable"})
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			id, owner := uuid.New(), uuid.New()
			key := "transcripts/" + id.String() + ".bin"
			blobs := newFakeBlobStore()
			blobs.put(key, []byte(c.Content))
			q := &mockQuerier{
				getTranscriptByID: func(_ context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
					return sqlc.Transcript{ID: id, OwnerID: toPgUUID(owner), Visibility: c.Visibility, BlobKey: key, WrappedDataKey: []byte("test-wrapped-key"), EncryptionAlgorithm: "aes-256-gcm-random-nonce-v1", KeyVersion: 1}, nil
				},
				listPullRequestCandidatesByTranscripts: func(context.Context, sqlc.ListPullRequestCandidatesByTranscriptsParams) ([]sqlc.ListPullRequestCandidatesByTranscriptsRow, error) {
					return []sqlc.ListPullRequestCandidatesByTranscriptsRow{{TranscriptID: toPgUUID(id), RepoOwner: "other", RepoName: "attached", Number: 7, State: "attached", AuthorID: toPgUUID(owner)}}, nil
				},
			}
			var store storage.TranscriptBlobStore = blobs
			if c.FailRead {
				store = failingEvidenceStore{blobs}
			}
			h := newTestHandler(q, store)
			r := withChiURLParam(httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id.String()+"/pulls", nil), "id", id.String())
			w := httptest.NewRecorder()
			h.ListTranscriptPullRequests(w, r)
			if w.Code != c.Status {
				t.Fatalf("status %d, want %d: %s", w.Code, c.Status, w.Body.String())
			}
			if blobs.downloads != c.Reads || blobs.uploadCount() != 0 {
				t.Fatalf("blob reads=%d writes=%d, want %d reads and no writes", blobs.downloads, blobs.uploadCount(), c.Reads)
			}
			if strings.Contains(w.Body.String(), "SECRET-MARKER") {
				t.Fatal("stored content leaked in error")
			}
			if c.Status != http.StatusOK {
				return
			}
			var got schema.VillageTranscriptPullRequestsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.UnverifiedPullRequests == nil || len(*got.UnverifiedPullRequests) != 1 {
				t.Fatalf("evidence missing: %s", w.Body.String())
			}
			row := (*got.UnverifiedPullRequests)[0]
			if row.Owner != "example" || row.Repo != "widgets" || row.Number != 42 || row.Source.TurnIndex != 42 || row.Source.ToolCallIndex != c.CallIndex || row.Source.ToolCallID != c.CallID {
				t.Fatalf("wrong evidence: %+v", row)
			}
			if len(got.PullRequests) != 1 || got.PullRequests[0].Owner != "other" {
				t.Fatalf("attachment did not coexist: %s", w.Body.String())
			}
		})
	}
}
