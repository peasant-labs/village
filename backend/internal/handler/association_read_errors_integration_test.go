//go:build integration

package handler

import (
	"context"
	_ "embed"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/association-read-errors.yaml
var associationReadErrorsYAML []byte

type associationReadErrorCase struct {
	Name       string `yaml:"name"`
	Query      string `yaml:"query"`
	Route      string `yaml:"route"`
	Visibility string `yaml:"visibility"`
}

// Only the failing database boundary is injected. All other queries, grants and
// the mounted handler run on PostgreSQL; no policy or handler is mocked.
type failingAssociationQueries struct {
	Querier
	query string
}

var errAssociationLookup = errors.New("database association read unavailable")

func (q failingAssociationQueries) ListApprovedTranscriptShareGroups(ctx context.Context, id pgtype.UUID) ([]pgtype.UUID, error) {
	if q.query == "grant-groups" {
		return nil, errAssociationLookup
	}
	return q.Querier.ListApprovedTranscriptShareGroups(ctx, id)
}
func (q failingAssociationQueries) GetGroupMember(ctx context.Context, arg sqlc.GetGroupMemberParams) (sqlc.GroupMember, error) {
	if q.query == "grant-member" {
		return sqlc.GroupMember{}, errAssociationLookup
	}
	return q.Querier.GetGroupMember(ctx, arg)
}
func (q failingAssociationQueries) ListGroupOwnersForTranscript(ctx context.Context, id pgtype.UUID) ([]pgtype.UUID, error) {
	if q.query == "grant-owners" {
		return nil, errAssociationLookup
	}
	return q.Querier.ListGroupOwnersForTranscript(ctx, id)
}

func (q failingAssociationQueries) GetTranscriptByID(ctx context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
	if q.query == "transcript" {
		return sqlc.Transcript{}, errAssociationLookup
	}
	return q.Querier.GetTranscriptByID(ctx, id)
}

func (q failingAssociationQueries) ListTranscriptShares(ctx context.Context, id pgtype.UUID) ([]sqlc.ListTranscriptSharesRow, error) {
	if q.query == "shares" {
		return nil, errAssociationLookup
	}
	return q.Querier.ListTranscriptShares(ctx, id)
}
func (q failingAssociationQueries) ListSharesByTranscriptIDs(ctx context.Context, ids []pgtype.UUID) ([]sqlc.ListSharesByTranscriptIDsRow, error) {
	if q.query == "enriched" {
		return nil, errAssociationLookup
	}
	return q.Querier.ListSharesByTranscriptIDs(ctx, ids)
}
func (q failingAssociationQueries) ListTranscriptCollectivesForViewer(ctx context.Context, arg sqlc.ListTranscriptCollectivesForViewerParams) ([]sqlc.ListTranscriptCollectivesForViewerRow, error) {
	if q.query == "disclosure" {
		return nil, errAssociationLookup
	}
	return q.Querier.ListTranscriptCollectivesForViewer(ctx, arg)
}
func (q failingAssociationQueries) ListCollectivesByGitHubOrg(ctx context.Context, arg sqlc.ListCollectivesByGitHubOrgParams) ([]sqlc.ListCollectivesByGitHubOrgRow, error) {
	if q.query == "organization" {
		return nil, errAssociationLookup
	}
	return q.Querier.ListCollectivesByGitHubOrg(ctx, arg)
}

func TestAssociationReadErrorsAreNotEmptySnapshots(t *testing.T) {
	cases, err := decodeFixtureRows[associationReadErrorCase](associationReadErrorsYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"detail-share-read-failure-not-empty":                  false,
		"detail-enriched-read-failure-not-empty":               false,
		"detail-disclosure-read-failure-not-empty":             false,
		"dedicated-disclosure-read-failure-not-empty":          false,
		"organization-linked-collectives-failure-not-empty":    false,
		"dedicated-transcript-read-failure-not-known-negative": false,
		"detail-transcript-read-failure-not-known-negative":    false,
		"dedicated-grant-group-failure-not-known-negative":     false,
		"detail-grant-group-failure-not-known-negative":        false,
		"dedicated-grant-member-failure-not-known-negative":    false,
		"dedicated-review-grant-failure-not-known-negative":    false,
	}
	for _, c := range cases {
		if required[c.Name] {
			t.Fatalf("duplicate case %q", c.Name)
		}
		if _, ok := required[c.Name]; !ok {
			t.Fatalf("unrecognized case %q", c.Name)
		}
		required[c.Name] = true
	}
	for name, present := range required {
		if !present {
			t.Fatalf("missing required case %q", name)
		}
	}
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	world := newCollectiveWorld(t, ctx, pool, collectiveWorldSpec{caseName: "read-errors", dataAccess: "public", acceptanceMode: "open", discoverable: true, visibility: "public", transcripts: 1})
	defer world.cleanup(t, ctx)
	world.step(t, New(&config.Config{}, pool, nil), 0, "submit")
	if _, err := pool.Exec(ctx, "INSERT INTO user_github_orgs(user_id,org_login,org_id,visible) VALUES ($1,'association-test-org',900123,true)", world.owner); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			visibility := c.Visibility
			if visibility == "" {
				visibility = "public"
			}
			execAsSystem(t, ctx, pool, "UPDATE transcripts SET visibility=$2 WHERE id=$1", world.transcripts[0], visibility)
			h := New(&config.Config{}, pool, nil)
			h.queries = failingAssociationQueries{Querier: h.queries, query: c.Query}
			router := chi.NewRouter()
			router.Get("/transcripts/{id}", h.GetTranscript)
			router.Get("/transcripts/{id}/collectives", h.ListTranscriptCollectives)
			router.Get("/orgs/{login}", h.GetOrg)
			path := "/transcripts/" + uuid.UUID(world.transcripts[0].Bytes).String()
			switch c.Route {
			case "detail":
			case "dedicated":
				path += "/collectives"
			case "organization":
				path = "/orgs/association-test-org"
			default:
				t.Fatalf("unknown route %q", c.Route)
			}
			rec := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request = request.WithContext(context.WithValue(ctx, UserContextKey, &AuthUser{ID: uuid.UUID(world.member.Bytes)}))
			router.ServeHTTP(rec, request)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("database error became status %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
