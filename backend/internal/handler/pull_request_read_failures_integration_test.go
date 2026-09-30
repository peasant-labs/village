//go:build integration

package handler

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/pull-request-read-failures.yaml
var pullRequestReadFailuresYAML []byte

// requiredPullRequestReadFailureCases is the name manifest for
// testdata/pull-request-read-failures.yaml. Exact membership, never a count.
var requiredPullRequestReadFailureCases = []string{
	"the-list-refuses-a-failed-tags-read",
	"the-list-refuses-a-failed-owners-read",
	"the-list-refuses-failed-summaries",
	"the-collective-refuses-a-failed-count",
	"the-collective-refuses-failed-summaries",
	"the-grouped-collective-refuses-a-failed-count",
	"the-grouped-collective-refuses-failed-summaries",
	"the-transcript-list-of-pull-requests-refuses-a-failed-read",
	"personal-totals-refuse-a-failed-totals-read",
	"personal-totals-refuse-a-failed-pull-request-read",
	"the-repository-picker-refuses-a-failed-publisher-count",
}

// failingReadQuerier is the real query layer with exactly one read made to
// fail, so a case proves what the handler does with that one failure while
// every other read runs against the database.
type failingReadQuerier struct {
	Querier
	fails string
}

var errInjectedRead = errors.New("injected read failure")

func (q failingReadQuerier) ListTagsByTranscriptIDs(ctx context.Context, ids []pgtype.UUID) ([]sqlc.ListTagsByTranscriptIDsRow, error) {
	if q.fails == "ListTagsByTranscriptIDs" {
		return nil, errInjectedRead
	}
	return q.Querier.ListTagsByTranscriptIDs(ctx, ids)
}

func (q failingReadQuerier) ListUsersByIDs(ctx context.Context, ids []pgtype.UUID) ([]sqlc.User, error) {
	if q.fails == "ListUsersByIDs" {
		return nil, errInjectedRead
	}
	return q.Querier.ListUsersByIDs(ctx, ids)
}

func (q failingReadQuerier) ListPullRequestCandidatesByTranscripts(ctx context.Context, arg sqlc.ListPullRequestCandidatesByTranscriptsParams) ([]sqlc.ListPullRequestCandidatesByTranscriptsRow, error) {
	if q.fails == "ListPullRequestCandidatesByTranscripts" {
		return nil, errInjectedRead
	}
	return q.Querier.ListPullRequestCandidatesByTranscripts(ctx, arg)
}

func (q failingReadQuerier) ListAttachedPullRequestCandidatesByGroup(ctx context.Context, arg sqlc.ListAttachedPullRequestCandidatesByGroupParams) ([]sqlc.ListAttachedPullRequestCandidatesByGroupRow, error) {
	if q.fails == "ListAttachedPullRequestCandidatesByGroup" {
		return nil, errInjectedRead
	}
	return q.Querier.ListAttachedPullRequestCandidatesByGroup(ctx, arg)
}

func (q failingReadQuerier) ListAttachedPullRequestCandidatesByOwner(ctx context.Context, arg sqlc.ListAttachedPullRequestCandidatesByOwnerParams) ([]sqlc.ListAttachedPullRequestCandidatesByOwnerRow, error) {
	if q.fails == "ListAttachedPullRequestCandidatesByOwner" {
		return nil, errInjectedRead
	}
	return q.Querier.ListAttachedPullRequestCandidatesByOwner(ctx, arg)
}

func (q failingReadQuerier) GetOwnerTranscriptTotals(ctx context.Context, ownerID pgtype.UUID) (sqlc.GetOwnerTranscriptTotalsRow, error) {
	if q.fails == "GetOwnerTranscriptTotals" {
		return sqlc.GetOwnerTranscriptTotalsRow{}, errInjectedRead
	}
	return q.Querier.GetOwnerTranscriptTotals(ctx, ownerID)
}

func (q failingReadQuerier) ListCollectiveSharedRemotes(ctx context.Context, groupID pgtype.UUID) ([]sqlc.ListCollectiveSharedRemotesRow, error) {
	if q.fails == "ListCollectiveSharedRemotes" {
		return nil, errInjectedRead
	}
	return q.Querier.ListCollectiveSharedRemotes(ctx, groupID)
}

type pullRequestReadFailuresFixture struct {
	World prWorld `yaml:"world"`
	Cases []struct {
		Name    string `yaml:"name"`
		Surface string `yaml:"surface"`
		Viewer  string `yaml:"viewer"`
		Fails   string `yaml:"fails"`
		Error   string `yaml:"error"`
	} `yaml:"cases"`
}

func TestPullRequestReadsRefuseAFailedRead_RealPostgres(t *testing.T) {
	fixture, err := decodeFixtureDocument[pullRequestReadFailuresFixture](pullRequestReadFailuresYAML)
	if err != nil {
		t.Fatalf("load testdata/pull-request-read-failures.yaml: %v", err)
	}
	present := map[string]struct{}{}
	for _, c := range fixture.Cases {
		if _, repeated := present[c.Name]; repeated {
			t.Fatalf("testdata/pull-request-read-failures.yaml repeats %q", c.Name)
		}
		present[c.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "pull-request-read-failures", present, requiredPullRequestReadFailureCases)

	pool := govTestPool(t)
	t.Cleanup(pool.Close)
	world := buildPRWorld(t, pool, fixture.World, "pull-request-read-failures")
	groupID := uuid.UUID(world.collectives["team"].Bytes).String()
	transcriptID := uuid.UUID(world.transcripts["session"].Bytes).String()
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			h := world.handler(t, pool)
			if c.Surface == "available" {
				installation := &fakeGitHub{
					installationsBody:     fmt.Sprintf(`[{"id":99,"account":{"login":"acme","id":%d,"type":"User"}}]`, world.githubIDs[c.Viewer]),
					installationReposBody: `{"total_count":1,"repositories":[{"name":"site","private":false,"owner":{"login":"acme"}}]}`,
				}
				newFakeGitHub(t, installation)
				h.gh = newRepoHandler(t, h.queries, installation).gh
			}
			h.queries = failingReadQuerier{Querier: h.queries, fails: c.Fails}
			target := map[string]string{
				"list":               "/api/v1/transcripts?limit=100&owner=" + world.logins["author"],
				"collective":         "/api/v1/groups/" + groupID,
				"collective_grouped": "/api/v1/groups/" + groupID + "?view=grouped",
				"transcript_pulls":   "/api/v1/transcripts/" + transcriptID + "/pulls",
				"stats":              "/api/v1/users/me/stats",
				"available":          "/api/v1/groups/" + groupID + "/repositories/available",
			}[c.Surface]
			if target == "" {
				t.Fatalf("case names unknown surface %q", c.Surface)
			}
			rec := world.get(t, h, prWorldRouter(h), c.Viewer, target)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("with %s failing, %s answered %d (%s), want 500", c.Fails, target, rec.Code, rec.Body.String())
			}
			if message := decodeError(t, rec.Body.Bytes()); !strings.Contains(message, c.Error) {
				t.Fatalf("error = %q, want it to say %q", message, c.Error)
			}
		})
	}
}
