//go:build integration

package handler

// The three contribution reads must be one answer. The project picker counts,
// the paged view lists and the snapshot view enumerates the same families, and
// each of them answers "which families does this search select" by itself. They
// are written in different forms (static SQL, a keyset page, a streamed
// hand-built statement), so nothing but a test that asks all three the same
// question can notice that one of them drifted.
//
// One library is built from testdata/contribution-consistency.yaml. Each request
// in that file is sent through the MOUNTED routes to all three reads, and the
// answers are held to three equalities and to a hand-written statement of which
// families the rule selects, so the three reads cannot agree on a wrong answer.

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
)

//go:embed testdata/contribution-consistency.yaml
var contributionConsistencyYAML []byte

type consistencyLibrary struct {
	Projects    map[string]string     `yaml:"projects"`
	Overrides   map[string]string     `yaml:"overrides"`
	Transcripts []pagedTranscriptSpec `yaml:"transcripts"`
}

type consistencyRequest struct {
	Name           string   `yaml:"name"`
	Why            string   `yaml:"why"`
	Q              string   `yaml:"q"`
	Provider       string   `yaml:"provider"`
	ExpectFamilies []string `yaml:"expect_families"`
	ExpectMatched  []string `yaml:"expect_matched"`
}

type consistencyParameter struct {
	Name     string `yaml:"name"`
	Why      string `yaml:"why"`
	Raw      string `yaml:"raw"`
	Picker   int    `yaml:"picker"`
	Paged    int    `yaml:"paged"`
	Snapshot int    `yaml:"snapshot"`
}

type consistencyGate struct {
	Name         string `yaml:"name"`
	Why          string `yaml:"why"`
	Viewer       string `yaml:"viewer"`
	Collective   string `yaml:"collective"`
	Raw          string `yaml:"raw"`
	ExpectStatus int    `yaml:"expect_status"`
}

type consistencyFixture struct {
	Library    consistencyLibrary     `yaml:"library"`
	Requests   []consistencyRequest   `yaml:"requests"`
	Parameters []consistencyParameter `yaml:"parameters"`
	Gates      []consistencyGate      `yaml:"gates"`
}

// requiredConsistencyParameters and requiredConsistencyGates name the parameter
// and gate cases that must exist.
var requiredConsistencyParameters = []string{
	"an_unknown_parameter_is_ignored",
	"a_repeated_q_is_refused",
	"a_repeated_provider_is_refused",
	"a_repeated_limit_is_refused_where_it_applies",
	"an_empty_cursor_is_refused_where_it_applies",
	"q_that_is_not_utf8_is_refused",
	"q_with_a_nul_is_refused",
	"provider_that_is_not_utf8_is_refused",
	"a_repeated_project_hash_is_refused_where_it_applies",
	"a_repeated_view_is_refused_where_it_applies",
}

var requiredConsistencyGates = []string{
	"a_member_is_served",
	"a_stranger_is_refused",
	"a_stranger_is_refused_before_a_bad_parameter_is_read",
	"a_collective_that_is_not_there_is_refused_like_one_you_are_not_in",
	"an_address_that_is_not_an_identifier_is_refused",
}

// requiredConsistencyRequests names the requests that must exist. Each is here
// because losing it hides one way the reads have disagreed: the provider rule,
// the project name of a root against the project name of a helper, the displayed
// name against the published one, the three literal characters of a search, the
// local session id, and a search of nothing.
var requiredConsistencyRequests = []string{
	"no_search_lists_every_family",
	"a_blank_q_is_no_search",
	"provider_keeps_families_holding_one_session_of_it",
	"provider_is_trimmed",
	"q_matches_a_title_in_a_root_or_a_helper",
	"q_is_trimmed_and_case_insensitive",
	"q_and_provider_combine_by_family",
	"the_root_project_name_selects_its_families",
	"a_helpers_own_project_name_does_not_match",
	"the_displayed_name_is_matched_not_the_raw_one",
	"percent_in_q_is_a_literal_percent",
	"underscore_in_q_is_a_literal_underscore",
	"backslash_in_q_is_a_literal_backslash",
	"q_equal_to_a_local_session_id_matches_that_session",
	"part_of_a_local_session_id_matches_nothing",
	"q_equal_to_a_transcript_id_matches_in_any_case",
	"q_with_no_match_selects_nothing",
}

func loadConsistencyFixture(t *testing.T) consistencyFixture {
	t.Helper()
	fixture, err := decodeFixtureDocument[consistencyFixture](contributionConsistencyYAML)
	if err != nil {
		t.Fatalf("load the contribution consistency fixture: %v", err)
	}
	present := map[string]bool{}
	for _, r := range fixture.Requests {
		if r.Name == "" || r.Why == "" {
			t.Fatalf("a consistency request needs a name and a reason: %+v", r)
		}
		if present[r.Name] {
			t.Fatalf("the consistency fixture repeats request %q", r.Name)
		}
		present[r.Name] = true
	}
	for _, required := range requiredConsistencyRequests {
		if !present[required] {
			t.Fatalf("the consistency fixture no longer contains %q. That request exists because losing it hides a "+
				"real disagreement between the reads; restore it rather than deleting it from this manifest.", required)
		}
	}
	requireNames := func(kind string, have map[string]bool, required []string) {
		for _, name := range required {
			if !have[name] {
				t.Fatalf("the consistency fixture no longer contains the %s case %q. It exists because losing it hides a "+
					"real disagreement between the reads; restore it rather than deleting it from this manifest.", kind, name)
			}
		}
	}
	params := map[string]bool{}
	for _, p := range fixture.Parameters {
		if p.Name == "" || p.Why == "" || p.Picker == 0 || p.Paged == 0 || p.Snapshot == 0 {
			t.Fatalf("a parameter case needs a name, a reason and a status for each read: %+v", p)
		}
		if params[p.Name] {
			t.Fatalf("the consistency fixture repeats parameter case %q", p.Name)
		}
		params[p.Name] = true
	}
	requireNames("parameter", params, requiredConsistencyParameters)
	gates := map[string]bool{}
	for _, g := range fixture.Gates {
		if g.Name == "" || g.Why == "" || g.ExpectStatus == 0 {
			t.Fatalf("a gate case needs a name, a reason and a status: %+v", g)
		}
		if gates[g.Name] {
			t.Fatalf("the consistency fixture repeats gate case %q", g.Name)
		}
		gates[g.Name] = true
	}
	requireNames("gate", gates, requiredConsistencyGates)
	return fixture
}

// consistencyRead is what one request learned from the three reads.
type consistencyRead struct {
	// families maps a root key to the keys of its helpers, as the paged view lists them.
	families map[string][]string
	matched  map[string]bool
	live     map[string]bool
	provider map[string]string
	// projectOf is the OWN project key of every member the paged view listed.
	projectOf map[string]string
	picker    schema.VillageContributionProjectPage
	snapshots map[string]schema.VillageContributionSnapshotResponse
}

func (w *pagedWorld) expandConsistencyQuery(text string) string {
	out := text
	for key, id := range w.transcripts {
		idText := uuid.UUID(id.Bytes).String()
		out = strings.ReplaceAll(out, "{id:"+key+"}", idText)
		out = strings.ReplaceAll(out, "{ID:"+key+"}", strings.ToUpper(idText))
		local := w.localID(key)
		out = strings.ReplaceAll(out, "{local:"+key+"}", local)
		out = strings.ReplaceAll(out, "{localprefix:"+key+"}", local[:len(local)-2])
	}
	return out
}

func (w *pagedWorld) consistencyQuery(view, q, provider string, extra url.Values) string {
	values := url.Values{}
	if view != "" {
		values.Set("view", view)
	}
	if q != "" {
		values.Set("q", q)
	}
	if provider != "" {
		values.Set("provider", provider)
	}
	for key, vs := range extra {
		for _, v := range vs {
			values.Add(key, v)
		}
	}
	return values.Encode()
}

func (w *pagedWorld) readPagedFamilies(t *testing.T, q, provider string, into *consistencyRead) {
	t.Helper()
	query := w.consistencyQuery("paged", q, provider, url.Values{"limit": {"500"}})
	rec := w.get("/collectives", query)
	if rec.Code != http.StatusOK {
		t.Fatalf("the paged view answered %d for %q: %s", rec.Code, query, rec.Body.String())
	}
	raw, _ := w.decodePage(t, rec.Body.Bytes())
	if raw.HasMore {
		t.Fatalf("the library fits one page of 500 roots but the paged view says there is more: %s", rec.Body.String())
	}
	note := func(key string, member pagedRawMember) {
		if member.Matched != nil && *member.Matched {
			into.matched[key] = true
		}
		live, _ := member.Transcript["already_shared"].(bool)
		into.live[key] = live
		into.provider[key], _ = member.Transcript["model_provider"].(string)
		hash, _ := member.Transcript["project_hash"].(string)
		into.projectOf[key] = w.projectKeyOfHash(hash)
	}
	for _, item := range raw.Items {
		rootKey := w.keyOf(item.Root.Transcript["id"].(string))
		note(rootKey, item.Root)
		into.families[rootKey] = []string{}
		for _, helper := range item.Helpers {
			key := w.keyOf(helper.Transcript["id"].(string))
			note(key, helper)
			into.families[rootKey] = append(into.families[rootKey], key)
		}
	}
}

func (w *pagedWorld) projectKeyOfHash(hash string) string {
	for _, spec := range w.spec.Transcripts {
		if w.projectHashOf(spec.Project) == hash {
			return spec.Project
		}
	}
	return "<unknown:" + hash + ">"
}

func (w *pagedWorld) readPicker(t *testing.T, q, provider string) schema.VillageContributionProjectPage {
	t.Helper()
	query := w.consistencyQuery("", q, provider, nil)
	target := "/api/v1/collectives/" + w.groupID() + "/contributable/projects"
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+w.token)
	rec := httptest.NewRecorder()
	w.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the project picker answered %d for %q: %s", rec.Code, query, rec.Body.String())
	}
	dec := json.NewDecoder(strings.NewReader(rec.Body.String()))
	dec.DisallowUnknownFields()
	var page schema.VillageContributionProjectPage
	if err := dec.Decode(&page); err != nil {
		t.Fatalf("the picker does not match the contract: %v: %s", err, rec.Body.String())
	}
	if page.HasMore {
		t.Fatalf("the library fits one picker page but it says there is more: %s", rec.Body.String())
	}
	return page
}

func (w *pagedWorld) readSnapshot(t *testing.T, projectKey, q, provider string) schema.VillageContributionSnapshotResponse {
	t.Helper()
	query := w.consistencyQuery("snapshot", q, provider, url.Values{"project_hash": {w.projectHashOf(projectKey)}})
	rec := w.get("/collectives", query)
	if rec.Code != http.StatusOK {
		t.Fatalf("the snapshot of %s answered %d for %q: %s", projectKey, rec.Code, query, rec.Body.String())
	}
	dec := json.NewDecoder(strings.NewReader(rec.Body.String()))
	dec.DisallowUnknownFields()
	var snapshot schema.VillageContributionSnapshotResponse
	if err := dec.Decode(&snapshot); err != nil {
		t.Fatalf("the snapshot does not match the contract: %v: %s", err, rec.Body.String())
	}
	return snapshot
}

func consistencyKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func consistencySorted(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

// consistencyWorld is the library of the fixture, owned by one member of one
// collective, and the mounted routes that read it. strangerToken is the token of
// a user who owns sessions of their own and is in no collective of the library.
type consistencyWorld struct {
	*pagedWorld
	strangerToken string
}

func newConsistencyWorld(t *testing.T, fixture consistencyFixture) *consistencyWorld {
	t.Helper()
	ctx := context.Background()
	pool := govTestPool(t)
	t.Cleanup(pool.Close)
	const secret = "disposable-consistency-secret"

	world := &pagedWorld{
		contributeWorld: newContributeWorld(t, ctx, pool, "consistency", "open", true),
		spec:            pagedCase{Name: "consistency", Projects: fixture.Library.Projects, Transcripts: fixture.Library.Transcripts},
		base:            time.Now().UTC().Truncate(time.Second),
	}
	t.Cleanup(func() {
		world.cleanup(t, ctx)
		if world.otherGroup.Valid {
			_, _ = pool.Exec(ctx, "DELETE FROM groups WHERE id = $1", world.otherGroup)
		}
	})

	for ordinal, spec := range fixture.Library.Transcripts {
		world.insert(t, ctx, ordinal, spec)
	}
	for _, spec := range fixture.Library.Transcripts {
		world.appendHistory(t, ctx, spec)
	}
	for key, name := range fixture.Library.Overrides {
		if _, err := pool.Exec(ctx, `
			INSERT INTO owner_overrides (owner_id, target_kind, target_key, field, value)
			VALUES ($1, 'project', $2, 'display_name', $3)
		`, world.member, world.projectHashOf(key), name); err != nil {
			t.Fatalf("name project %s: %v", key, err)
		}
	}

	h := New(&config.Config{JWTSecret: secret}, pool, nil)
	router := chi.NewRouter()
	router.Route("/api/v1", h.RegisterCollectiveBrowseRoutes)
	world.router = router
	token, err := auth.CreateToken(secret, uuid.UUID(world.member.Bytes), world.memberName)
	if err != nil {
		t.Fatal(err)
	}
	world.token = token
	stranger, err := auth.CreateToken(secret, uuid.UUID(world.other.Bytes), "contrib-other-"+world.suffix)
	if err != nil {
		t.Fatal(err)
	}
	return &consistencyWorld{pagedWorld: world, strangerToken: stranger}
}

func (w *consistencyWorld) serve(token, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	w.router.ServeHTTP(rec, req)
	return rec
}

func TestContributionReadsAgree(t *testing.T) {
	fixture := loadConsistencyFixture(t)
	world := newConsistencyWorld(t, fixture).pagedWorld

	projects := consistencyKeys(fixture.Library.Projects)

	for _, request := range fixture.Requests {
		t.Run(request.Name, func(t *testing.T) {
			q := world.expandConsistencyQuery(request.Q)
			provider := request.Provider
			why := request.Why

			read := consistencyRead{
				families:  map[string][]string{},
				matched:   map[string]bool{},
				live:      map[string]bool{},
				provider:  map[string]string{},
				projectOf: map[string]string{},
				snapshots: map[string]schema.VillageContributionSnapshotResponse{},
			}
			world.readPagedFamilies(t, q, provider, &read)
			read.picker = world.readPicker(t, q, provider)
			for _, project := range projects {
				read.snapshots[project] = world.readSnapshot(t, project, q, provider)
			}

			// The rule, written out by hand: these are the families selected.
			if got, want := consistencyKeys(read.families), consistencySorted(request.ExpectFamilies); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("the paged view lists the families rooted at %v, the rule selects %v (%s)", got, want, why)
			}
			if strings.TrimSpace(q) != "" {
				if got, want := consistencyKeys(read.matched), consistencySorted(request.ExpectMatched); strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("the paged view marks %v matched, the rule matches %v (%s)", got, want, why)
				}
			} else if len(read.matched) != 0 {
				t.Errorf("no search was made but the paged view marks %v matched (%s)", consistencyKeys(read.matched), why)
			}

			// Equality 1: the picker's count of a project is that project's snapshot,
			// and the snapshot is the paged view's families rooted in the project.
			pickerCount := map[string]int{}
			pickerSelectable := map[string]int{}
			for _, summary := range read.picker.Projects {
				key := world.projectKeyOfHash(string(summary.ProjectHash))
				pickerCount[key] = summary.MatchingCount
				pickerSelectable[key] = summary.SelectableCount
			}
			for _, project := range projects {
				snapshot := read.snapshots[project]
				wantMembers := map[string]bool{}
				var wantRoots []string
				selectable := 0
				for root, helpers := range read.families {
					if read.projectOf[root] != project {
						continue
					}
					wantRoots = append(wantRoots, root)
					for _, key := range append([]string{root}, helpers...) {
						wantMembers[key] = true
						if !read.live[key] {
							selectable++
						}
					}
				}
				gotMembers := map[string]bool{}
				var gotRoots []string
				for _, record := range snapshot.Records {
					key := world.keyOf(string(record.ID))
					gotMembers[key] = true
					if record.ParentTranscriptID == nil {
						gotRoots = append(gotRoots, key)
					}
				}
				if got, want := consistencyKeys(gotMembers), consistencyKeys(wantMembers); strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("project %s: the snapshot holds %v, the paged view's families rooted there hold %v (%s)", project, got, want, why)
				}
				if got, want := consistencySorted(gotRoots), consistencySorted(wantRoots); strings.Join(got, ",") != strings.Join(want, ",") {
					t.Errorf("project %s: the roots of the snapshot are %v, the paged view lists roots %v (%s)", project, got, want, why)
				}
				if snapshot.Count != len(snapshot.Records) {
					t.Errorf("project %s: the snapshot counts %d but holds %d records (%s)", project, snapshot.Count, len(snapshot.Records), why)
				}
				if pickerCount[project] != len(snapshot.Records) {
					t.Errorf("project %s: the picker counts %d matching sessions, the snapshot holds %d (%s)", project, pickerCount[project], len(snapshot.Records), why)
				}
				if pickerSelectable[project] != selectable {
					t.Errorf("project %s: the picker counts %d selectable sessions, the paged view shows %d not yet live (%s)", project, pickerSelectable[project], selectable, why)
				}
			}
			for key := range pickerCount {
				if _, known := fixture.Library.Projects[key]; !known {
					t.Errorf("the picker lists a project %q the library does not have (%s)", key, why)
				}
			}

			// Equality 3: the picker's totals are the sessions of every family listed.
			members, notLive := 0, 0
			for root, helpers := range read.families {
				for _, key := range append([]string{root}, helpers...) {
					members++
					if !read.live[key] {
						notLive++
					}
				}
			}
			if read.picker.MatchingTotal != members {
				t.Errorf("the picker's matching_total is %d, the paged view lists %d sessions (%s)", read.picker.MatchingTotal, members, why)
			}
			if read.picker.SelectableTotal != notLive {
				t.Errorf("the picker's selectable_total is %d, the paged view lists %d sessions not yet live (%s)", read.picker.SelectableTotal, notLive, why)
			}

			// The provider facet ignores the provider filter and applies the search,
			// so it is the providers of the families the search alone selects.
			facetRead := read
			if provider != "" {
				facetRead = consistencyRead{
					families: map[string][]string{}, matched: map[string]bool{}, live: map[string]bool{},
					provider: map[string]string{}, projectOf: map[string]string{},
				}
				world.readPagedFamilies(t, q, "", &facetRead)
			}
			wantFacet := map[string]int{}
			for root, helpers := range facetRead.families {
				for _, key := range append([]string{root}, helpers...) {
					wantFacet[facetRead.provider[key]]++
				}
			}
			gotFacet := map[string]int{}
			for _, facet := range read.picker.Providers {
				gotFacet[facet.Provider] = facet.Count
			}
			if len(gotFacet) != len(wantFacet) {
				t.Errorf("the picker's provider facet is %v, the sessions of the families the search selects are %v (%s)", gotFacet, wantFacet, why)
			} else {
				for provider, count := range wantFacet {
					if gotFacet[provider] != count {
						t.Errorf("the picker's provider facet is %v, the sessions of the families the search selects are %v (%s)", gotFacet, wantFacet, why)
						break
					}
				}
			}
		})
	}
}

// TestContributionReadsAgreeOnParameters sends one set of parameters to all three
// reads. A parameter nobody reads is ignored, and a parameter a read does use is
// refused the same way by every read that uses it.
func TestContributionReadsAgreeOnParameters(t *testing.T) {
	fixture := loadConsistencyFixture(t)
	world := newConsistencyWorld(t, fixture)
	base := "/api/v1/collectives/" + world.groupID() + "/contributable"
	project := world.projectHashOf("alpha")

	for _, c := range fixture.Parameters {
		t.Run(c.Name, func(t *testing.T) {
			raw := strings.ReplaceAll(c.Raw, "{project}", project)
			reads := []struct {
				name   string
				target string
				want   int
			}{
				{"picker", base + "/projects?" + raw, c.Picker},
				{"paged", base + "?view=paged&" + raw, c.Paged},
				{"snapshot", base + "?view=snapshot&project_hash=" + project + "&" + raw, c.Snapshot},
			}
			for _, read := range reads {
				rec := world.serve(world.token, read.target)
				if rec.Code != read.want {
					t.Errorf("%s answered %d for %q, want %d (%s)\nbody: %s", read.name, rec.Code, read.target, read.want, c.Why, rec.Body.String())
					continue
				}
				if read.want == http.StatusBadRequest {
					var body struct {
						Error string `json:"error"`
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || strings.TrimSpace(body.Error) == "" {
						t.Errorf("%s refused %q with no message a person can act on: %s", read.name, read.target, rec.Body.String())
					}
				}
			}
		})
	}
}

// TestContributionReadsAgreeOnTheGate holds the three reads to the flat
// contributable route: the same viewer, collective and refusal, whatever else is
// wrong with the request.
func TestContributionReadsAgreeOnTheGate(t *testing.T) {
	fixture := loadConsistencyFixture(t)
	world := newConsistencyWorld(t, fixture)
	project := world.projectHashOf("alpha")

	for _, c := range fixture.Gates {
		t.Run(c.Name, func(t *testing.T) {
			token := world.token
			if c.Viewer == "stranger" {
				token = world.strangerToken
			}
			collective := world.groupID()
			switch c.Collective {
			case "unknown":
				collective = uuid.NewString()
			case "malformed":
				collective = "not-an-identifier"
			}
			base := "/api/v1/collectives/" + collective + "/contributable"
			extra := ""
			if c.Raw != "" {
				extra = "&" + c.Raw
			}
			flatTarget := base
			if c.Raw != "" {
				flatTarget += "?" + c.Raw
			}
			reads := []struct{ name, target string }{
				{"flat", flatTarget},
				{"picker", base + "/projects" + strings.Replace(extra, "&", "?", 1)},
				{"paged", base + "?view=paged" + extra},
				{"snapshot", base + "?view=snapshot&project_hash=" + project + extra},
			}
			for _, read := range reads {
				rec := world.serve(token, read.target)
				if rec.Code != c.ExpectStatus {
					t.Errorf("%s answered %d for %q, want %d (%s)\nbody: %s", read.name, rec.Code, read.target, c.ExpectStatus, c.Why, rec.Body.String())
				}
			}
		})
	}
}
