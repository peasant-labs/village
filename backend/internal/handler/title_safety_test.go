package handler

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/title_writes.yaml
var titleWriteFixtureBytes []byte

type titleWriteFixture struct {
	Name        string `yaml:"name"`
	Mode        string `yaml:"mode"`
	Harness     string `yaml:"harness"`
	ProjectPath string `yaml:"project_path"`
	Candidate   string `yaml:"candidate"`
	Expected    string `yaml:"expected"`
	Status      int    `yaml:"status"`
	Category    string `yaml:"category"`
}

func loadTitleWriteFixtures(t *testing.T) []titleWriteFixture {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(titleWriteFixtureBytes))
	decoder.KnownFields(true)
	var fixtures []titleWriteFixture
	if err := decoder.Decode(&fixtures); err != nil {
		t.Fatalf("decode strict title-write fixtures: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("title-write fixtures contain a trailing document: %v", err)
	}
	if len(fixtures) != 9 {
		t.Fatalf("title-write fixture count = %d, want 9", len(fixtures))
	}
	seen := make(map[string]struct{}, len(fixtures))
	for _, fixture := range fixtures {
		if fixture.Name == "" {
			t.Fatal("title-write fixture name is empty")
		}
		if _, exists := seen[fixture.Name]; exists {
			t.Fatalf("duplicate title-write fixture %q", fixture.Name)
		}
		seen[fixture.Name] = struct{}{}
	}
	return fixtures
}

func TestTitleWriteBoundariesFromFixtures(t *testing.T) {
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct real title pipeline: %v", err)
	}
	ownerID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	otherID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	transcriptID := uuid.MustParse("30000000-0000-0000-0000-000000000003")

	for _, fixture := range loadTitleWriteFixtures(t) {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			if fixture.Mode == "fresh" {
				candidate := fixture.Candidate
				req := schema.PublishRequest{Model: schema.ModelInfo{Harness: schema.Harness(fixture.Harness)}, Project: schema.ProjectContext{FilePath: fixture.ProjectPath}}
				if fixture.Name != "missing_candidate_fallback" {
					req.Quality = &schema.QualityMetrics{TitleGenerated: &candidate}
				}
				h := &Handler{titles: titles}
				h.sanitizeGeneratedTitle(&req)
				if got := *req.Quality.TitleGenerated; got != fixture.Expected {
					t.Fatalf("safe generated title = %q, want %q", got, fixture.Expected)
				}
				params := schemaToTranscriptParams(req, "blob", 1, "2")
				if params.Title.String != fixture.Expected || params.TitleGenerated.String != fixture.Expected {
					t.Fatalf("persisted title pair = %q/%q, want identical %q", params.Title.String, params.TitleGenerated.String, fixture.Expected)
				}
				return
			}

			writes := 0
			owner := pgtype.UUID{Bytes: ownerID, Valid: true}
			mq := &mockQuerier{
				getTranscriptByID: func(context.Context, pgtype.UUID) (sqlc.Transcript, error) {
					return sqlc.Transcript{ID: toPgUUID(transcriptID), OwnerID: owner, ModelProvider: fixture.Harness, ProjectPath: pgtype.Text{String: fixture.ProjectPath, Valid: fixture.ProjectPath != ""}, Visibility: "private"}, nil
				},
				getTranscriptGovernanceForUpdate: func(context.Context, pgtype.UUID) (sqlc.GetTranscriptGovernanceForUpdateRow, error) {
					return sqlc.GetTranscriptGovernanceForUpdateRow{Title: pgtype.Text{String: "old", Valid: true}, Visibility: "private"}, nil
				},
				updateTranscriptMetadata: func(_ context.Context, arg sqlc.UpdateTranscriptMetadataParams) (sqlc.Transcript, error) {
					writes++
					return sqlc.Transcript{ID: arg.ID, OwnerID: owner, Title: arg.Title, Visibility: arg.Visibility, UpdatedAt: pgtype.Timestamptz{Time: time.Unix(1, 0), Valid: true}}, nil
				},
			}
			h := &Handler{queries: mq, titles: titles}
			requestOwner := ownerID
			if fixture.Mode == "non_owner" {
				requestOwner = otherID
			}
			if fixture.Mode == "unavailable" {
				h.titles = nil
			}
			route := chi.NewRouteContext()
			route.URLParams.Add("id", transcriptID.String())
			ctx := context.WithValue(context.Background(), UserContextKey, &AuthUser{ID: requestOwner})
			ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
			body := strings.NewReader(`{"title":` + strconv.Quote(fixture.Candidate) + `}`)
			recorder := httptest.NewRecorder()
			h.UpdateTranscript(recorder, httptest.NewRequest(http.MethodPatch, "/api/v1/transcripts/"+transcriptID.String(), body).WithContext(ctx))
			if recorder.Code != fixture.Status {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, fixture.Status, recorder.Body.String())
			}
			if fixture.Status == http.StatusOK {
				if writes != 1 {
					t.Fatalf("safe patch writes = %d, want 1", writes)
				}
			} else if writes != 0 {
				t.Fatalf("rejected patch writes = %d, want 0", writes)
			}
			if fixture.Category != "" && !strings.Contains(recorder.Body.String(), fixture.Category) {
				t.Fatalf("response missing category %q: %s", fixture.Category, recorder.Body.String())
			}
			if fixture.Status != http.StatusOK && strings.Contains(recorder.Body.String(), fixture.Candidate) {
				t.Fatalf("rejection echoed candidate")
			}
		})
	}
}
