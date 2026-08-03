package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/authoritative_publication/cases.yaml
var authoritativePublicationCases []byte

type authoritativePublicationFixture struct {
	Publish      []authoritativePublishCase `yaml:"publish"`
	Patch        []authoritativePatchCase   `yaml:"patch"`
	LegacyUpdate authoritativeLegacyCase    `yaml:"legacy_update"`
}

type authoritativeLegacyCase struct {
	Name       string `yaml:"name"`
	Content    string `yaml:"content"`
	WantStatus int    `yaml:"want_status"`
}

type authoritativePublishCase struct {
	Name               string `yaml:"name"`
	Content            string `yaml:"content"`
	ParentSessionID    string `yaml:"parent_session_id"`
	AssociationID      string `yaml:"association_id"`
	ObservedCommitHash string `yaml:"observed_commit_hash"`
	WantStatus         int    `yaml:"want_status"`
}

type authoritativePatchCase struct {
	Name           string   `yaml:"name"`
	Body           string   `yaml:"body"`
	WantStatus     int      `yaml:"want_status"`
	WantTitle      string   `yaml:"want_title"`
	WantVisibility string   `yaml:"want_visibility"`
	WantTags       []string `yaml:"want_tags"`
}

func loadAuthoritativePublicationFixture(t *testing.T) authoritativePublicationFixture {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(authoritativePublicationCases))
	decoder.KnownFields(true)
	var fixture authoritativePublicationFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("fixture must contain exactly one YAML document: %v", err)
	}
	if len(fixture.Publish) != 7 || len(fixture.Patch) != 1 {
		t.Fatalf("fixture row guard failed: publish=%d patch=%d", len(fixture.Publish), len(fixture.Patch))
	}
	wantParent := fixture.Publish[0].ParentSessionID
	if wantParent == "" {
		t.Fatal("fixture coverage guard failed: every mounted authoritative publish must carry one stable parent session ID")
	}
	for _, row := range fixture.Publish {
		if row.ParentSessionID != wantParent {
			t.Fatalf("fixture coverage guard failed: authoritative publish %q parent=%q want stable parent %q", row.Name, row.ParentSessionID, wantParent)
		}
	}
	seen := map[string]bool{}
	names := []string{fixture.LegacyUpdate.Name}
	for _, row := range fixture.Patch {
		names = append(names, row.Name)
	}
	for _, row := range fixture.Publish {
		names = append(names, row.Name)
	}
	for _, name := range names {
		if name == "" || seen[name] {
			t.Fatalf("empty or duplicate fixture name %q", name)
		}
		seen[name] = true
	}
	return fixture
}

func TestPublishSaveErrorMessageRecognizesWrappedStagedCleanupFailure(t *testing.T) {
	saveErr := errors.New("database transaction failed")
	cleanupErr := errors.New("object deletion failed")
	typed := &stagedObjectCleanupError{key: "transcripts/owner/id/objects/hash.json", saveErr: saveErr, cleanupErr: cleanupErr}
	message := publishSaveErrorMessage(fmt.Errorf("publish operation: %w", typed), true)
	if !strings.Contains(message, "orphaned staged object") || !errors.Is(typed, saveErr) || !errors.Is(typed, cleanupErr) {
		t.Fatalf("wrapped staged cleanup mapping lost recovery or causes: %q", message)
	}
}

func TestPublishSaveErrorMessageKeepsOrdinarySaveFailureGeneric(t *testing.T) {
	err := errors.New("uncommitted staged object also failed as plain text")
	if got := publishSaveErrorMessage(err, true); got != "Failed to save transcript" {
		t.Fatalf("ordinary save error mapped to %q, want generic response", got)
	}
}

func TestPublishSaveErrorMessageHidesLegacyStagedObjectKey(t *testing.T) {
	typed := &stagedObjectCleanupError{key: "transcripts/11111111-1111-4111-8111-111111111111.bin", saveErr: errors.New("database transaction failed"), cleanupErr: errors.New("object deletion failed")}
	message := publishSaveErrorMessage(typed, false)
	if strings.Contains(message, typed.key) || strings.Contains(message, "transcripts/") || strings.Contains(message, ".bin") || !strings.Contains(message, "operator cleanup") || !strings.Contains(message, "publish retry") {
		t.Fatalf("legacy cleanup failure mapping disclosed a locator or lost recovery guidance: %q", message)
	}
}

func TestAuthoritativePublishMountedResponse(t *testing.T) {
	row := loadAuthoritativePublicationFixture(t).Publish[0]
	content := []byte(row.Content)
	sessionID := schema.SessionID("550e8400-e29b-41d4-a716-446655440000")
	parentSessionID := schema.SessionID(row.ParentSessionID)
	associationID, err := schema.NewAssociationID(row.AssociationID)
	if err != nil {
		t.Fatal(err)
	}
	req := schema.AuthoritativePublishRequest{
		Identity:    schema.AuthoritativeSessionIdentity{SessionID: sessionID, ParentSessionID: &parentSessionID, SchemaVersion: 2},
		Model:       schema.AuthoritativeModelInfo{Harness: schema.HarnessClaudeCode, Model: "fixture-model"},
		Timestamp:   schema.AuthoritativeTimestampInfo{Start: 1700000000000, End: 1700000001000},
		Source:      schema.AuthoritativeSourceInfo{FilePath: "/fixture/session.jsonl", Format: schema.SourceFormatJSONL},
		Project:     schema.AuthoritativeProjectContext{Hash: schema.ProjectHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), Name: "fixture"},
		Git:         schema.AuthoritativeGitContext{Associations: []schema.PublishedAssociation{{ID: associationID, ObservedCommitHash: row.ObservedCommitHash}}},
		ContentHash: schema.ComputeTranscriptContentHash(content), VisibilityIntent: schema.VisibilityIntentPrivate,
	}
	metadata, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	owner := pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-4000-8000-000000000111"), Valid: true}
	tid := pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-4000-8000-000000000222"), Valid: true}
	now := pgtype.Timestamptz{Time: time.Unix(1700000000, 0), Valid: true}
	q := &mockQuerier{
		getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
			return pgtype.UUID{}, pgx.ErrNoRows
		},
		listTranscriptAssociationsByOwnerAndIDs: func(context.Context, sqlc.ListTranscriptAssociationsByOwnerAndIDsParams) ([]sqlc.TranscriptAssociation, error) {
			return nil, nil
		},
		createTranscript: func(_ context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
			if !arg.ParentSessionID.Valid || arg.ParentSessionID.String != row.ParentSessionID {
				return sqlc.Transcript{}, fmt.Errorf("mapped parent=%+v want %q", arg.ParentSessionID, row.ParentSessionID)
			}
			return sqlc.Transcript{ID: tid, OwnerID: owner, LocalID: string(sessionID), Title: arg.Title, Visibility: "private", ModelProvider: string(schema.HarnessClaudeCode), BlobKey: arg.BlobKey, BlobSizeBytes: arg.BlobSizeBytes, SchemaVersion: "2", PublishedAt: now, UpdatedAt: now, LicenseID: arg.LicenseID}, nil
		},
		insertTranscriptAssociations: func(context.Context, sqlc.InsertTranscriptAssociationsParams) error { return nil },
		setTranscriptContentHash:     func(context.Context, sqlc.SetTranscriptContentHashParams) error { return nil },
		setAcceptedRequestOperationFingerprint: func(_ context.Context, arg sqlc.SetAcceptedRequestOperationFingerprintParams) error {
			if !arg.AcceptedRequestOperationFingerprint.Valid {
				return fmt.Errorf("missing fingerprint")
			}
			return nil
		},
		deleteTranscriptCommits: func(context.Context, pgtype.UUID) error { return nil },
		listTranscriptAssociationsByTranscript: func(context.Context, pgtype.UUID) ([]sqlc.TranscriptAssociation, error) {
			return []sqlc.TranscriptAssociation{{OwnerID: owner, TranscriptID: tid, AssociationID: row.AssociationID, ObservedCommitSha: row.ObservedCommitHash}}, nil
		},
	}
	h := newTestHandler(q, &mockTranscriptBlobStore{})
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadata)}, row.Content)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "fixture-owner"}))
	w := httptest.NewRecorder()
	h.PublishTranscript(w, r)
	if w.Code != row.WantStatus {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response schema.AuthoritativePublishResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if err := response.Validate(); err != nil {
		t.Fatalf("invalid authoritative response: %v", err)
	}
	if len(response.Applied.Associations) != 1 || response.Applied.Associations[0].ID != associationID {
		t.Fatalf("incomplete association response: %+v", response.Applied.Associations)
	}
}

func TestOwnerPatchMountedSuccessorResponse(t *testing.T) {
	row := loadAuthoritativePublicationFixture(t).Patch[0]
	ownerID := uuid.MustParse("00000000-0000-4000-8000-000000000311")
	tid := uuid.MustParse("00000000-0000-4000-8000-000000000312")
	owner := toPgUUID(ownerID)
	now := pgtype.Timestamptz{Time: time.Unix(1700000000, 0), Valid: true}
	q := &mockQuerier{
		getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) {
			return sqlc.Transcript{ID: toPgUUID(tid), OwnerID: owner, LocalID: "fixture-session", Visibility: "private"}, nil
		},
		getTranscriptGovernanceForUpdate: func(context.Context, pgtype.UUID) (sqlc.GetTranscriptGovernanceForUpdateRow, error) {
			return sqlc.GetTranscriptGovernanceForUpdateRow{ID: toPgUUID(tid), Visibility: "private"}, nil
		},
		updateTranscriptMetadata: func(_ context.Context, arg sqlc.UpdateTranscriptMetadataParams) (sqlc.Transcript, error) {
			return sqlc.Transcript{ID: arg.ID, OwnerID: owner, Title: arg.Title, Description: arg.Description, Visibility: arg.Visibility, LicenseID: arg.LicenseID, UpdatedAt: now}, nil
		},
		unlinkTranscriptTags: func(context.Context, pgtype.UUID) error { return nil },
		getOrCreateTag: func(_ context.Context, name string) (sqlc.Tag, error) {
			return sqlc.Tag{ID: toPgUUID(uuid.New()), Name: name}, nil
		},
		linkTranscriptTag: func(context.Context, sqlc.LinkTranscriptTagParams) error { return nil },
		getTranscriptTags: func(context.Context, pgtype.UUID) ([]sqlc.Tag, error) {
			return []sqlc.Tag{{Name: row.WantTags[0]}, {Name: row.WantTags[1]}}, nil
		},
	}
	h := newTestHandler(q, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", tid.String())
	ctx := context.WithValue(context.Background(), UserContextKey, &AuthUser{ID: ownerID, Username: "owner"})
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/transcripts/"+tid.String(), bytes.NewBufferString(row.Body)).WithContext(ctx)
	w := httptest.NewRecorder()
	h.UpdateTranscript(w, r)
	if w.Code != row.WantStatus {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response schema.OwnerTranscriptUpdateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	if response.Title == nil || *response.Title != row.WantTitle || response.Visibility.String() != row.WantVisibility {
		t.Fatalf("unexpected response: %+v", response)
	}
}
