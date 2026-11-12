package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
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

//go:embed testdata/publish_harness_acceptance.yaml
var publishHarnessAcceptanceYAML []byte

//go:embed testdata/publish_harness_acceptance.manifest.yaml
var publishHarnessAcceptanceManifestYAML []byte

type publishHarnessCase struct {
	Name          string   `yaml:"name"`
	Harness       string   `yaml:"harness"`
	Status        int      `yaml:"status"`
	ErrorContains []string `yaml:"error_contains"`
}

type publishHarnessFixture struct {
	SessionID       string               `yaml:"session_id"`
	ContentTemplate string               `yaml:"content_template"`
	Cases           []publishHarnessCase `yaml:"cases"`
}

func loadPublishHarnessCases(t *testing.T) publishHarnessFixture {
	t.Helper()
	var fixture publishHarnessFixture
	var manifest struct {
		RequiredNames []string `yaml:"required_names"`
	}
	decode := func(raw []byte, dest any) {
		t.Helper()
		decoder := yaml.NewDecoder(bytes.NewReader(raw))
		decoder.KnownFields(true)
		if err := decoder.Decode(dest); err != nil {
			t.Fatal(err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			t.Fatalf("fixture must contain one YAML document: %v", err)
		}
	}
	decode(publishHarnessAcceptanceYAML, &fixture)
	decode(publishHarnessAcceptanceManifestYAML, &manifest)
	if fixture.SessionID == "" || strings.Count(fixture.ContentTemplate, "__SESSION_ID__") != 1 || strings.Count(fixture.ContentTemplate, "__HARNESS__") != 1 {
		t.Fatal("publish harness content must carry exactly one session and one harness placeholder")
	}
	if len(manifest.RequiredNames) == 0 {
		t.Fatal("publish harness manifest must name the required cases")
	}
	seen := make(map[string]bool, len(fixture.Cases))
	harnesses := make(map[string]bool, len(fixture.Cases))
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] || c.Harness == "" || harnesses[c.Harness] {
			t.Fatalf("invalid or duplicated publish harness case %q", c.Name)
		}
		if c.Status != http.StatusCreated && c.Status != http.StatusUnprocessableEntity {
			t.Fatalf("case %q has unsupported status %d", c.Name, c.Status)
		}
		if (c.Status == http.StatusUnprocessableEntity) != (len(c.ErrorContains) > 0) {
			t.Fatalf("case %q must state the refusal body only for a refusal", c.Name)
		}
		for _, needle := range c.ErrorContains {
			if needle == "" {
				t.Fatalf("case %q contains an empty refusal-body expectation", c.Name)
			}
		}
		seen[c.Name] = true
		harnesses[c.Harness] = true
	}
	required := make(map[string]bool, len(manifest.RequiredNames))
	for _, name := range manifest.RequiredNames {
		if name == "" || required[name] || !seen[name] {
			t.Fatalf("missing, empty, or duplicated required publish harness case %q", name)
		}
		required[name] = true
	}
	return fixture
}

// publishHarnessRequest sends the same strict envelope and authoritative
// metadata for each case, with only the harness (and its derived content hash)
// changed. Unlike the frozen legacy publish menu, the authoritative decoder
// consumes the current known harness set.
func publishHarnessRequest(t *testing.T, fixture publishHarnessFixture, harness string) *http.Request {
	t.Helper()
	sessionID, err := json.Marshal(fixture.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	harnessJSON, err := json.Marshal(harness)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Replace(fixture.ContentTemplate, "__SESSION_ID__", string(sessionID), 1)
	content = strings.Replace(content, "__HARNESS__", string(harnessJSON), 1)
	req := schema.AuthoritativePublishRequest{
		Identity:    schema.AuthoritativeSessionIdentity{SessionID: schema.SessionID(fixture.SessionID), SchemaVersion: 2},
		Model:       schema.AuthoritativeModelInfo{Harness: schema.Harness(harness), Model: "fixture-model"},
		Timestamp:   schema.AuthoritativeTimestampInfo{Start: 1700000000000, End: 1700000001000},
		Source:      schema.AuthoritativeSourceInfo{FilePath: "/fixture/session.jsonl", Format: schema.SourceFormatJSONL},
		Project:     schema.AuthoritativeProjectContext{Hash: testProjectHash, Name: "fixture"},
		ContentHash: schema.ComputeTranscriptContentHash([]byte(content)), VisibilityIntent: schema.VisibilityIntentPrivate,
	}
	metadata, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadata)}, content)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	return r
}

func mountPublishHarnessHandler(h *Handler, user *AuthUser) http.Handler {
	routes := chi.NewRouter()
	routes.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, user)))
		})
	})
	routes.Post("/api/v1/transcripts/publish", h.PublishTranscript)
	return routes
}

func assertPublishHarnessResponse(t *testing.T, c publishHarnessCase, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != c.Status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, c.Status, w.Body.String())
	}
	for _, needle := range c.ErrorContains {
		if !strings.Contains(w.Body.String(), needle) {
			t.Fatalf("refusal body %q does not contain %q", w.Body.String(), needle)
		}
	}
}

func TestPublishHarnessAcceptance(t *testing.T) {
	fixture := loadPublishHarnessCases(t)
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ownerID := uuid.MustParse("00000000-0000-4000-8000-000000000111")
			owner, tid := toPgUUID(ownerID), toPgUUID(uuid.MustParse("00000000-0000-4000-8000-000000000222"))
			now := pgtype.Timestamptz{Time: time.Unix(1700000000, 0), Valid: true}
			var saved *sqlc.CreateTranscriptParams
			q := &mockQuerier{
				getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
					return pgtype.UUID{}, pgx.ErrNoRows
				},
				createTranscript: func(_ context.Context, arg sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
					saved = &arg
					return sqlc.Transcript{ID: tid, OwnerID: owner, LocalID: arg.LocalID, Title: arg.Title, Visibility: "private", ModelProvider: arg.ModelProvider, BlobKey: arg.BlobKey, BlobSizeBytes: arg.BlobSizeBytes, SchemaVersion: "2", PublishedAt: now, UpdatedAt: now, LicenseID: arg.LicenseID}, nil
				},
				setTranscriptContentHash: func(context.Context, sqlc.SetTranscriptContentHashParams) error { return nil },
				setAcceptedRequestOperationFingerprint: func(context.Context, sqlc.SetAcceptedRequestOperationFingerprintParams) error {
					return nil
				},
				deleteTranscriptCommits: func(context.Context, pgtype.UUID) error { return nil },
				listTranscriptAssociationsByTranscript: func(context.Context, pgtype.UUID) ([]sqlc.TranscriptAssociation, error) {
					return nil, nil
				},
			}
			blobs := &mockTranscriptBlobStore{}
			h := newTestHandler(q, blobs)
			w := httptest.NewRecorder()
			mountPublishHarnessHandler(h, &AuthUser{ID: ownerID, Username: "fixture-owner"}).ServeHTTP(w, publishHarnessRequest(t, fixture, c.Harness))
			assertPublishHarnessResponse(t, c, w)
			if c.Status != http.StatusCreated {
				if saved != nil || len(blobs.uploadedKeys) != 0 {
					t.Fatal("refused harness reached a transcript or object write")
				}
				return
			}
			if saved == nil {
				t.Fatal("successful publish did not write a transcript")
			}
			if saved.ModelProvider != c.Harness || len(blobs.uploadedKeys) != 1 {
				t.Fatalf("stored harness=%q want=%q objects=%d", saved.ModelProvider, c.Harness, len(blobs.uploadedKeys))
			}
			var response schema.AuthoritativePublishResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if err := response.Validate(); err != nil {
				t.Fatalf("invalid publish receipt: %v", err)
			}
		})
	}
}
