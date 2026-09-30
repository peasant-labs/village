//go:build integration

package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database"
)

//go:embed testdata/pull-request-summaries.yaml
var pullRequestSummariesYAML []byte

//go:embed testdata/pull-request-list-queries.yaml
var pullRequestListQueriesYAML []byte

// requiredPullRequestSummariesCases is the name manifest for
// testdata/pull-request-summaries.yaml. Exact membership, never a count.
var requiredPullRequestSummariesCases = []string{
	"the-author-sees-every-attached-pull-request-on-their-list",
	"a-stranger-sees-public-repository-pull-requests-on-the-list",
	"an-anonymous-reader-sees-public-repository-pull-requests-on-the-list",
	"a-repository-reader-outside-the-collective-sees-what-a-stranger-sees",
	"a-member-sees-private-repository-pull-requests-in-the-collective",
	"a-stranger-counts-only-public-repository-pull-requests-in-the-collective",
	"a-member-sees-the-same-in-the-grouped-collective-read",
	"an-anonymous-reader-sees-the-same-as-a-stranger-in-the-grouped-collective-read",
}

// requiredPullRequestListQueryCases is the name manifest for
// testdata/pull-request-list-queries.yaml: one case per surface that carries a
// pull request summary.
var requiredPullRequestListQueryCases = []string{
	"the-transcript-list-costs-the-same-for-one-row-and-fifty",
	"the-flat-collective-read-costs-the-same-for-one-row-and-fifty",
	"the-grouped-collective-read-costs-the-same-for-one-row-and-fifty",
}

type pullRequestSummaryExpected struct {
	Count  int32    `yaml:"count"`
	Recent []string `yaml:"recent"`
}

type pullRequestSummariesFixture struct {
	World prWorld `yaml:"world"`
	Cases []struct {
		Name    string                                `yaml:"name"`
		Viewer  string                                `yaml:"viewer"`
		Surface string                                `yaml:"surface"`
		Stats   *int32                                `yaml:"stats"`
		Rows    map[string]pullRequestSummaryExpected `yaml:"rows"`
	} `yaml:"cases"`
}

type pullRequestListQueriesFixture struct {
	PageSizes []int `yaml:"page_sizes"`
	Cases     []struct {
		Name    string `yaml:"name"`
		Surface string `yaml:"surface"`
	} `yaml:"cases"`
}

// summarySurfaceRead is what one surface answered: each row's summary by
// transcript id, and the collective's count when the surface carries one.
type summarySurfaceRead struct {
	rows  map[string]schema.VillagePullRequestsSummary
	stats *int32
}

// readSummarySurface drives one surface as the viewer and decodes the rows'
// summaries from the body the client receives.
func readSummarySurface(t *testing.T, world *builtPRWorld, h *Handler, routes http.Handler, viewer, surface, collective, owner string) summarySurfaceRead {
	t.Helper()
	groupID := uuid.UUID(world.collectives[collective].Bytes).String()
	var target string
	switch surface {
	case "list":
		target = "/api/v1/transcripts?limit=100&owner=" + world.logins[owner]
	case "collective":
		target = "/api/v1/groups/" + groupID + "?limit=100"
	case "collective_grouped":
		target = "/api/v1/groups/" + groupID + "?view=grouped&limit=100"
	default:
		t.Fatalf("unknown surface %q; add its route explicitly", surface)
	}
	rec := world.get(t, h, routes, viewer, target)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s answered %d (%s), want 200", target, rec.Code, rec.Body.String())
	}
	read := summarySurfaceRead{rows: map[string]schema.VillagePullRequestsSummary{}}
	add := func(id string, summary *schema.VillagePullRequestsSummary) {
		if summary == nil {
			t.Fatalf("%s row %s carries no pull_requests summary", surface, id)
		}
		if err := summary.Validate(); err != nil {
			t.Fatalf("%s row %s summary does not satisfy the contract: %v", surface, id, err)
		}
		read.rows[id] = *summary
	}
	switch surface {
	case "list":
		var body struct {
			Transcripts []struct {
				Transcript struct {
					ID string `json:"id"`
				} `json:"transcript"`
				PullRequests *schema.VillagePullRequestsSummary `json:"pull_requests"`
			} `json:"transcripts"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode the list: %v", err)
		}
		for _, row := range body.Transcripts {
			add(row.Transcript.ID, row.PullRequests)
		}
	case "collective":
		var body struct {
			Stats       schema.VillageGroupTranscriptStats `json:"stats"`
			Transcripts []struct {
				ID           string                             `json:"id"`
				PullRequests *schema.VillagePullRequestsSummary `json:"pull_requests"`
			} `json:"transcripts"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode the collective: %v", err)
		}
		for _, row := range body.Transcripts {
			add(row.ID, row.PullRequests)
		}
		read.stats = &body.Stats.PullRequestCount
	case "collective_grouped":
		var body schema.VillageGroupedGroupDetailResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode the grouped collective: %v", err)
		}
		if err := body.Validate(); err != nil {
			t.Fatalf("the grouped collective does not satisfy the contract: %v", err)
		}
		for _, item := range body.TranscriptList.Items {
			if item.Transcript == nil || item.Transcript.Collective == nil {
				t.Fatalf("a grouped collective item carries no collective row: %+v", item)
			}
			add(string(item.Transcript.Collective.ID), &item.Transcript.Collective.PullRequests)
		}
		read.stats = &body.Stats.PullRequestCount
	}
	return read
}

func formatPullRequestRefs(refs []schema.VillagePullRequestRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, fmt.Sprintf("%s/%s#%d", ref.Owner, ref.Name, ref.Number))
	}
	return out
}

func TestPullRequestSummaries_RealPostgres(t *testing.T) {
	fixture, err := decodeFixtureDocument[pullRequestSummariesFixture](pullRequestSummariesYAML)
	if err != nil {
		t.Fatalf("load testdata/pull-request-summaries.yaml: %v", err)
	}
	present := map[string]bool{}
	for _, c := range fixture.Cases {
		if present[c.Name] {
			t.Fatalf("testdata/pull-request-summaries.yaml repeats %q", c.Name)
		}
		present[c.Name] = true
		if (c.Surface == "list") != (c.Stats == nil) {
			t.Fatalf("case %q: stats is required on the collective surfaces and absent on the list", c.Name)
		}
	}
	assertExactCaseNames(t, "pull-request-summaries", present, requiredPullRequestSummariesCases)

	pool := govTestPool(t)
	t.Cleanup(pool.Close)
	world := buildPRWorld(t, pool, fixture.World, "pull-request-summaries")
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			h := world.handler(t, pool)
			read := readSummarySurface(t, world, h, prWorldRouter(h), c.Viewer, c.Surface, "team", "author")
			got := map[string]schema.VillagePullRequestsSummary{}
			for id, summary := range read.rows {
				got[world.transcriptName(t, id)] = summary
			}
			if len(got) != len(c.Rows) {
				t.Fatalf("%s returned rows %v, want exactly %v", c.Surface, got, c.Rows)
			}
			for name, want := range c.Rows {
				summary, ok := got[name]
				if !ok {
					t.Fatalf("%s returned no row for %s (rows: %v)", c.Surface, name, got)
				}
				recent := formatPullRequestRefs(summary.Recent)
				if summary.Count != want.Count || strings.Join(recent, ",") != strings.Join(want.Recent, ",") {
					t.Fatalf("%s summary of %s = %d %v, want %d %v", c.Surface, name, summary.Count, recent, want.Count, want.Recent)
				}
			}
			if c.Stats != nil && (read.stats == nil || *read.stats != *c.Stats) {
				t.Fatalf("%s pull_request_count = %v, want %d", c.Surface, read.stats, *c.Stats)
			}
			if _, asks := world.github.counts(); asks != 0 {
				t.Fatalf("the summaries asked GitHub %d permission question(s); they never ask what a viewer may read", asks)
			}
		})
	}
}

// queryCounter counts every statement a pool runs, so a test can compare what
// two requests cost.
type queryCounter struct {
	mu      sync.Mutex
	queries int
}

func (c *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	c.queries++
	c.mu.Unlock()
	return ctx
}

func (c *queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (c *queryCounter) take() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.queries
	c.queries = 0
	return n
}

// summaryGuardWorld is the page the guard reads: size public transcripts, each
// shared with a public collective and bound to one attached pull request in a
// private repository and one in a public repository.
func summaryGuardWorld(size int) prWorld {
	w := prWorld{
		People: []string{"author", "member"},
		Collectives: []prWorldCollective{{
			Name: "team", CreatedBy: "author", DataAccess: "public", Members: map[string]string{"member": "member"},
			Repositories: []prWorldRepo{{Repo: "acme/app", Private: true}, {Repo: "acme/site", Private: false}},
		}},
	}
	for i := 1; i <= size; i++ {
		name := fmt.Sprintf("session-%02d", i)
		w.Transcripts = append(w.Transcripts, prWorldTranscript{Name: name, Owner: "author", Visibility: dbVisibilityPublic,
			Shares: map[string]string{"team": "approved"}})
		w.Attachments = append(w.Attachments,
			prWorldAttachment{Collective: "team", Repo: "acme/app", Number: i, Author: "author", State: "attached", Transcripts: []string{name}},
			prWorldAttachment{Collective: "team", Repo: "acme/site", Number: 1000 + i, Author: "author", State: "attached", Transcripts: []string{name}})
	}
	return w
}

// TestPullRequestSummariesCostTheSameForAnyPageSize is the no-N+1 guard: a page
// of one row and a page of fifty rows issue the same number of queries on every
// surface that carries a pull request summary.
func TestPullRequestSummariesCostTheSameForAnyPageSize(t *testing.T) {
	fixture, err := decodeFixtureDocument[pullRequestListQueriesFixture](pullRequestListQueriesYAML)
	if err != nil {
		t.Fatalf("load testdata/pull-request-list-queries.yaml: %v", err)
	}
	if len(fixture.PageSizes) != 2 || fixture.PageSizes[0] != 1 || fixture.PageSizes[1] < 50 {
		t.Fatalf("page_sizes = %v; the guard compares a page of one row with a page of at least fifty", fixture.PageSizes)
	}
	present := map[string]bool{}
	for _, c := range fixture.Cases {
		if present[c.Name] {
			t.Fatalf("testdata/pull-request-list-queries.yaml repeats %q", c.Name)
		}
		present[c.Name] = true
	}
	assertExactCaseNames(t, "pull-request-list-queries", present, requiredPullRequestListQueryCases)

	writer := govTestPool(t)
	t.Cleanup(writer.Close)
	counter := &queryCounter{}
	config, err := database.PoolConfig(pullTestDatabaseURL(t))
	if err != nil {
		t.Fatalf("build the counting pool: %v", err)
	}
	config.ConnConfig.Tracer = counter
	reader, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open the counting pool: %v", err)
	}
	t.Cleanup(reader.Close)

	worlds := make([]*builtPRWorld, len(fixture.PageSizes))
	for i, size := range fixture.PageSizes {
		worlds[i] = buildPRWorld(t, writer, summaryGuardWorld(size), "pull-request-list-queries")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			costs := make([]int, len(fixture.PageSizes))
			for i, size := range fixture.PageSizes {
				world := worlds[i]
				h := world.handler(t, reader)
				routes := prWorldRouter(h)
				counter.take()
				read := readSummarySurface(t, world, h, routes, "member", c.Surface, "team", "author")
				costs[i] = counter.take()

				if len(read.rows) != size {
					t.Fatalf("a page of %d returned %d row(s)", size, len(read.rows))
				}
				for id, summary := range read.rows {
					if summary.Count != 2 || len(summary.Recent) != 2 {
						t.Fatalf("row %s of the page of %d carries summary %+v, want the two pull requests bound to it", id, size, summary)
					}
				}
				if c.Surface != "list" && (read.stats == nil || *read.stats != int32(2*size)) {
					t.Fatalf("the page of %d reported pull_request_count %v, want %d", size, read.stats, 2*size)
				}
			}
			for i := 1; i < len(costs); i++ {
				if costs[i] != costs[0] {
					t.Fatalf("%s issued %d queries for a page of %d and %d for a page of %d; a surface must not read once per row",
						c.Surface, costs[0], fixture.PageSizes[0], costs[i], fixture.PageSizes[i])
				}
			}
		})
	}
}
