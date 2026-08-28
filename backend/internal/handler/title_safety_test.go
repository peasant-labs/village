package handler

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
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
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
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
	assertExactTitleFixtureNames(t, "title-write", seen, requiredTitleWriteCaseNames)
	return fixtures
}

// requiredTitleWriteCaseNames is the deletion-protection manifest for
// title_writes.yaml, asserted as EXACT membership in both directions.
//
// It replaces a bare row count. A count says only that somebody changed the
// file; it cannot say WHICH boundary stopped being covered, it goes stale on
// every legitimate addition, and two branches that each add a case conflict on
// the same integer. A name manifest names what must not be lost: the two
// project-path parity rows (the ones the redaction module rewrites), the three
// fallback rows, and the four PATCH refusal boundaries.
var requiredTitleWriteCaseNames = []string{
	"shared_project_path_parity",
	"missing_candidate_fallback",
	"empty_candidate_fallback",
	"republish_safe_generated",
	"raw_tag_generated_title_preserved_by_sanitize",
	"safe_patch_byte_preservation",
	"sensitive_category_no_write_no_echo",
	"validation_unavailable_no_write",
	"non_owner_denied_before_validation",
}

// assertExactTitleFixtureNames holds a fixture's case names to exactly the
// manifest: a missing name means a case was deleted, and an unexpected name
// means a case was added without recording why it must survive. Both are
// reported by NAME, so the failure says what changed rather than that a tally
// moved.
func assertExactTitleFixtureNames(t *testing.T, fixture string, present map[string]struct{}, required []string) {
	t.Helper()
	want := make(map[string]struct{}, len(required))
	for _, name := range required {
		want[name] = struct{}{}
	}
	var missing, unexpected []string
	for name := range want {
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name := range present {
		if _, ok := want[name]; !ok {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	if len(missing) > 0 {
		t.Fatalf("the %s fixture no longer contains %v. Each of those cases exists because losing it hides a real "+
			"failure; restore the row rather than removing it from the manifest.", fixture, missing)
	}
	if len(unexpected) > 0 {
		t.Fatalf("the %s fixture carries %v, which the manifest does not list. Add each new case to the manifest so a "+
			"later deletion is caught by name.", fixture, unexpected)
	}
}

// regenerateFreshTitleExpectation runs one `fresh`-mode fixture row through the
// SAME production path a publish takes — Handler.sanitizeGeneratedTitle over the
// real redaction title pipeline — and returns the title that path produces.
//
// It is the single definition of "what this row's expectation is", shared by the
// behavioral test below and by the zero-diff check in
// title_fixture_module_test.go, so the two can never disagree about how an
// expectation is derived.
func regenerateFreshTitleExpectation(titles *redact.TitlePipeline, fixture titleWriteFixture) (string, schema.PublishRequest) {
	candidate := fixture.Candidate
	req := schema.PublishRequest{
		Model:   schema.ModelInfo{Harness: schema.Harness(fixture.Harness)},
		Project: schema.ProjectContext{FilePath: fixture.ProjectPath},
	}
	// missing_candidate_fallback is the row where the publisher sent NO
	// quality block at all, so the fallback comes from the harness name
	// rather than from a candidate; every other row carries a candidate.
	if fixture.Name != "missing_candidate_fallback" {
		req.Quality = &schema.QualityMetrics{TitleGenerated: &candidate}
	}
	h := &Handler{titles: titles}
	h.sanitizeGeneratedTitle(&req)
	return *req.Quality.TitleGenerated, req
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
				got, req := regenerateFreshTitleExpectation(titles, fixture)
				if got != fixture.Expected {
					t.Fatalf("safe generated title = %q, want %q", got, fixture.Expected)
				}
				params := schemaToTranscriptParams(req, "blob", 1, "2", sessionorigin.Unknown)
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
