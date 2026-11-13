//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
)

// The contribution project picker, driven through the real handler over a real
// PostgreSQL. Every count is asserted against the sessions the fixture wrote, so
// a count computed in Go from the same rules would not be trusted on its own.

//go:embed testdata/contribution-projects.yaml
var contributionProjectsYAML []byte

type cpTranscript struct {
	Key      string `yaml:"key"`
	Project  string `yaml:"project"`
	Parent   string `yaml:"parent"`
	Owner    string `yaml:"owner"`
	Pre      string `yaml:"pre"`
	Provider string `yaml:"provider"`
	Title    string `yaml:"title"`
	Path     string `yaml:"path"`
}

type cpExpectProject struct {
	Project    string `yaml:"project"`
	Name       string `yaml:"name"`
	Source     string `yaml:"source"`
	Matching   int    `yaml:"matching"`
	Selectable int    `yaml:"selectable"`
}

type cpExpectProvider struct {
	Provider string `yaml:"provider"`
	Count    int    `yaml:"count"`
}

type contributionProjectsCase struct {
	Name             string             `yaml:"name"`
	Why              string             `yaml:"why"`
	Member           *bool              `yaml:"member"`
	Collective       string             `yaml:"collective"`
	Names            map[string]string  `yaml:"names"`
	Transcripts      []cpTranscript     `yaml:"transcripts"`
	Query            string             `yaml:"query"`
	QueryIDOf        string             `yaml:"query_id_of"`
	Provider         string             `yaml:"provider"`
	RawQuery         string             `yaml:"raw_query"`
	Walk             int                `yaml:"walk"`
	ForeignCursor    bool               `yaml:"foreign_cursor"`
	ExpectStatus     int                `yaml:"expect_status"`
	ExpectMatching   int                `yaml:"expect_matching_total"`
	ExpectSelectable int                `yaml:"expect_selectable_total"`
	ExpectProjects   []cpExpectProject  `yaml:"expect_projects"`
	ExpectPageSizes  []int              `yaml:"expect_page_sizes"`
	ExpectProviders  []cpExpectProvider `yaml:"expect_providers"`
}

func loadContributionProjectsCases(t *testing.T) []contributionProjectsCase {
	t.Helper()
	cases, err := decodeFixtureRows[contributionProjectsCase](contributionProjectsYAML)
	if err != nil {
		t.Fatalf("load contribution projects fixture: %v", err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" {
			t.Fatalf("case %q needs a name and a why", c.Name)
		}
		if seen[c.Name] {
			t.Fatalf("duplicate case %q", c.Name)
		}
		seen[c.Name] = true
		if c.ExpectStatus == 0 {
			t.Fatalf("case %q omits expect_status", c.Name)
		}
		if c.ExpectStatus == http.StatusOK && (c.ExpectProjects == nil || c.ExpectProviders == nil) {
			t.Fatalf("case %q answers 200 and must state expect_projects and expect_providers", c.Name)
		}
	}
	return cases
}

func TestListContributionProjects(t *testing.T) {
	pool := govTestPool(t)
	ctx := context.Background()
	h := New(&config.Config{}, pool, nil)
	for _, c := range loadContributionProjectsCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			runContributionProjectsCase(t, ctx, pool, h, c)
		})
	}
}

func runContributionProjectsCase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, h *Handler, c contributionProjectsCase) {
	t.Helper()
	member := c.Member == nil || *c.Member
	w := newContributeWorld(t, ctx, pool, c.Name, "open", member)
	defer w.cleanup(t, ctx)

	for key, name := range c.Names {
		if _, err := pool.Exec(ctx, `
			INSERT INTO owner_overrides (owner_id, target_kind, target_key, field, value)
			VALUES ($1, 'project', $2, 'display_name', $3)
		`, w.member, w.projectHash(key), name); err != nil {
			t.Fatalf("name project %s: %v", key, err)
		}
	}
	for i, tr := range c.Transcripts {
		w.insertTranscript(t, ctx, i+1, tr.Key, tr.Project, "", tr.Owner, tr.Parent)
		if tr.Provider != "" || tr.Title != "" || tr.Path != "" {
			w.updateContributionFixture(t, ctx, tr)
		}
		if tr.Pre == "live" {
			w.submitSingle(t, h, tr.Key)
		}
	}

	collective := w.groupID()
	switch c.Collective {
	case "unknown":
		collective = uuid.NewString()
	case "malformed":
		collective = "not-an-identifier"
	}

	if c.ForeignCursor {
		first := listProjectPage(t, h, w, collective, contributionProjectsRawQuery(contributionProjectsCase{}, w, 1, ""))
		if first.NextCursor == nil {
			t.Fatalf("the foreign cursor needs a second project to page to")
		}
		c.RawQuery = "cursor=" + url.QueryEscape(*first.NextCursor)
		status, body := listContributionProjectsFor(h, w, collective, contributionProjectsRawQuery(c, w, 0, ""))
		if status != http.StatusBadRequest {
			t.Fatalf("a cursor from another search answered %d, want 400: %s", status, body)
		}
		return
	}

	if c.ExpectStatus != http.StatusOK {
		status, body := listContributionProjectsFor(h, w, collective, contributionProjectsRawQuery(c, w, 0, ""))
		if status != c.ExpectStatus {
			t.Fatalf("status = %d, want %d, body %s", status, c.ExpectStatus, body)
		}
		if c.ExpectStatus == http.StatusBadRequest || c.ExpectStatus == http.StatusForbidden {
			var refusal map[string]string
			if err := json.Unmarshal(body, &refusal); err != nil || refusal["error"] == "" {
				t.Fatalf("refusal body %s must carry a non-empty error", body)
			}
		}
		return
	}

	if c.Walk > 0 {
		walkContributionProjects(t, h, w, collective, c)
		return
	}

	page := listProjectPage(t, h, w, collective, contributionProjectsRawQuery(c, w, 0, ""))
	assertProjectPage(t, w, c, page, page.Projects)
	if page.HasMore || page.NextCursor != nil {
		t.Fatalf("a page holding every project must not continue: has_more=%v next_cursor=%v", page.HasMore, page.NextCursor)
	}
}

// walkContributionProjects follows next_cursor until has_more is false, and
// asserts the pages together hold every project exactly once, in the picker's
// order, with totals and providers unchanged from page to page.
func walkContributionProjects(t *testing.T, h *Handler, w *contributeWorld, collective string, c contributionProjectsCase) {
	t.Helper()
	var all []schema.VillageContributionProjectSummary
	var sizes []int
	var first *schema.VillageContributionProjectPage
	cursor := ""
	for i := 0; ; i++ {
		if i > 100 {
			t.Fatalf("the cursor never ended")
		}
		page := listProjectPage(t, h, w, collective, contributionProjectsRawQuery(c, w, c.Walk, cursor))
		if len(page.Projects) > c.Walk {
			t.Fatalf("page %d holds %d projects, over the limit %d", i+1, len(page.Projects), c.Walk)
		}
		if first == nil {
			p := page
			first = &p
		} else if page.MatchingTotal != first.MatchingTotal || page.SelectableTotal != first.SelectableTotal ||
			!sameProviders(page.Providers, first.Providers) {
			t.Fatalf("page %d changed the totals or provider counts of page 1", i+1)
		}
		all = append(all, page.Projects...)
		sizes = append(sizes, len(page.Projects))
		if !page.HasMore {
			if page.NextCursor != nil {
				t.Fatalf("the last page must not carry a next_cursor")
			}
			break
		}
		if page.NextCursor == nil {
			t.Fatalf("page %d says has_more but carries no next_cursor", i+1)
		}
		cursor = *page.NextCursor
	}
	if c.ExpectPageSizes != nil && !equalInts(sizes, c.ExpectPageSizes) {
		t.Fatalf("page sizes = %v, want %v", sizes, c.ExpectPageSizes)
	}
	assertProjectPage(t, w, c, *first, all)
}

func assertProjectPage(t *testing.T, w *contributeWorld, c contributionProjectsCase, page schema.VillageContributionProjectPage, projects []schema.VillageContributionProjectSummary) {
	t.Helper()
	if page.MatchingTotal != c.ExpectMatching {
		t.Errorf("matching_total = %d, want %d", page.MatchingTotal, c.ExpectMatching)
	}
	if page.SelectableTotal != c.ExpectSelectable {
		t.Errorf("selectable_total = %d, want %d", page.SelectableTotal, c.ExpectSelectable)
	}

	want := make([]cpExpectProject, len(c.ExpectProjects))
	copy(want, c.ExpectProjects)
	wantSummaries := make([]schema.VillageContributionProjectSummary, 0, len(want))
	for _, ep := range want {
		hash := w.projectHash(ep.Project)
		name := ep.Name
		if name == "label" {
			name = "project-" + hash[:12]
		}
		wantSummaries = append(wantSummaries, schema.VillageContributionProjectSummary{
			ProjectHash:        schema.ProjectHash(hash),
			ProjectDisplayName: name,
			ProjectNameSource:  schema.VillageProjectNameSource(ep.Source),
			MatchingCount:      ep.Matching,
			SelectableCount:    ep.Selectable,
		})
	}
	sort.SliceStable(wantSummaries, func(i, j int) bool {
		a, b := wantSummaries[i], wantSummaries[j]
		if a.ProjectDisplayName != b.ProjectDisplayName {
			return a.ProjectDisplayName < b.ProjectDisplayName
		}
		return a.ProjectHash < b.ProjectHash
	})
	if len(projects) != len(wantSummaries) {
		t.Fatalf("projects = %s, want %d entries", summariesJSON(projects), len(wantSummaries))
	}
	for i := range wantSummaries {
		if projects[i] != wantSummaries[i] {
			t.Errorf("project %d = %+v, want %+v", i, projects[i], wantSummaries[i])
		}
	}

	if len(page.Providers) != len(c.ExpectProviders) {
		t.Fatalf("providers = %+v, want %+v", page.Providers, c.ExpectProviders)
	}
	for i, ep := range c.ExpectProviders {
		if page.Providers[i].Provider != ep.Provider || page.Providers[i].Count != ep.Count {
			t.Errorf("provider %d = %+v, want %s:%d", i, page.Providers[i], ep.Provider, ep.Count)
		}
	}
}

func summariesJSON(projects []schema.VillageContributionProjectSummary) string {
	raw, _ := json.Marshal(projects)
	return string(raw)
}

func sameProviders(a, b []schema.VillageContributionProviderFacet) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// contributionProjectsRawQuery builds the query string a case asks for. limit
// zero omits the parameter; cursor empty omits it.
func contributionProjectsRawQuery(c contributionProjectsCase, w *contributeWorld, limit int, cursor string) string {
	q := url.Values{}
	switch {
	case c.QueryIDOf != "":
		q.Set("q", uuid.UUID(w.transcripts[c.QueryIDOf].Bytes).String())
	case c.Query != "":
		q.Set("q", c.Query)
	}
	if c.Provider != "" {
		q.Set("provider", c.Provider)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	encoded := q.Encode()
	if c.RawQuery != "" {
		if encoded != "" {
			encoded += "&"
		}
		encoded += c.RawQuery
	}
	return encoded
}

func listProjectPage(t *testing.T, h *Handler, w *contributeWorld, collective, rawQuery string) schema.VillageContributionProjectPage {
	t.Helper()
	status, body := listContributionProjectsFor(h, w, collective, rawQuery)
	if status != http.StatusOK {
		t.Fatalf("list projects answered %d: %s", status, body)
	}
	if !bytes.Contains(body, []byte(`"projects":[`)) || !bytes.Contains(body, []byte(`"providers":[`)) {
		t.Fatalf("projects and providers must be arrays, never null: %s", body)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var page schema.VillageContributionProjectPage
	if err := dec.Decode(&page); err != nil {
		t.Fatalf("response does not match the contract: %v: %s", err, body)
	}
	return page
}

func listContributionProjectsFor(h *Handler, w *contributeWorld, collective, rawQuery string) (int, []byte) {
	target := fmt.Sprintf("/api/v1/groups/%s/contributable/projects", collective)
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	req := shareRequestAs(w.member, w.memberName, http.MethodGet, target, nil, map[string]string{"id": collective})
	rec := httptest.NewRecorder()
	h.ListContributionProjects(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// updateContributionFixture sets the provider, title and raw project path a case
// names on a transcript the fixture just wrote, as the system actor.
func (w *contributeWorld) updateContributionFixture(t *testing.T, ctx context.Context, tr cpTranscript) {
	t.Helper()
	provider := tr.Provider
	if provider == "" {
		provider = "claude-code"
	}
	title := tr.Title
	if title == "" {
		title = "t-" + tr.Key
	}
	var path pgtype.Text
	if tr.Path != "" {
		path = pgtype.Text{String: tr.Path, Valid: true}
	}
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin fixture update: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatalf("declare the system actor: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE transcripts SET model_provider = $2, title = $3, project_path = $4 WHERE id = $1
	`, w.transcripts[tr.Key], provider, title, path); err != nil {
		t.Fatalf("update transcript %s: %v", tr.Key, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit fixture update %s: %v", tr.Key, err)
	}
}
