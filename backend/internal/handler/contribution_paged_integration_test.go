//go:build integration

package handler

// The paged contribution read, driven through the REAL mounted routes against a
// REAL PostgreSQL. The page is built by one recursive statement over the owner's
// library, the project names it can match are resolved by the shared resolver,
// and the contribution state of each member comes from the append-only attempt
// ledger, so none of it can be checked by a Go function computing its own
// expectation. The cases live in testdata/contribution-paged.yaml.

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
)

//go:embed testdata/contribution-paged.yaml
var contributionPagedYAML []byte

type pagedTranscriptSpec struct {
	Key           string   `yaml:"key"`
	Project       string   `yaml:"project"`
	Parent        string   `yaml:"parent"`
	ParentMissing string   `yaml:"parent_missing"`
	Owner         string   `yaml:"owner"`
	Title         string   `yaml:"title"`
	Provider      string   `yaml:"provider"`
	Visibility    string   `yaml:"visibility"`
	SessionAge    *int     `yaml:"session_age"`
	PublishedAge  *int     `yaml:"published_age"`
	History       []string `yaml:"history"`
}

type pagedRequestSpec struct {
	Query                  string              `yaml:"query"`
	ExpectStatus           int                 `yaml:"expect_status"`
	ExpectErrorContains    []string            `yaml:"expect_error_contains"`
	ExpectRoots            []string            `yaml:"expect_roots"`
	ExpectHelpers          map[string][]string `yaml:"expect_helpers"`
	ExpectHasMore          bool                `yaml:"expect_has_more"`
	ExpectMatched          map[string]bool     `yaml:"expect_matched"`
	ExpectLatestStatus     map[string]string   `yaml:"expect_latest_status"`
	ExpectAlreadyShared    []string            `yaml:"expect_already_shared"`
	ExpectProject          map[string]string   `yaml:"expect_project"`
	ExpectProjectName      map[string]string   `yaml:"expect_project_name"`
	ExpectParentTranscript map[string]string   `yaml:"expect_parent_transcript"`
	ExpectSessionStart     map[string]string   `yaml:"expect_session_start"`
	ExpectVisibility       map[string]string   `yaml:"expect_visibility"`
	BothPrefixes           bool                `yaml:"both_prefixes"`
}

type pagedWalkSpec struct {
	Limit       int      `yaml:"limit"`
	Query       string   `yaml:"query"`
	ExpectRoots []string `yaml:"expect_roots"`
	ExpectPages int      `yaml:"expect_pages"`
}

type pagedCase struct {
	Name        string                `yaml:"name"`
	Why         string                `yaml:"why"`
	Member      *bool                 `yaml:"member"`
	Projects    map[string]string     `yaml:"projects"`
	Transcripts []pagedTranscriptSpec `yaml:"transcripts"`
	Requests    []pagedRequestSpec    `yaml:"requests"`
	Walk        *pagedWalkSpec        `yaml:"walk"`
}

// requiredContributionPagedCases names the cases that must exist. Each is here
// because losing it hides a specific failure: the ordering and its fallback
// clock, the bound counted in roots, the cursor across ties and filters, the
// definition of a root, the family rule for helpers in other projects, every
// search shape, the per-collective contribution state, the refusals, and the
// membership gate.
var requiredContributionPagedCases = []string{
	"roots_are_ordered_by_session_time_newest_first",
	"limit_counts_roots_and_helpers_ride_with_their_root",
	"exact_fit_page_has_no_more",
	"keyset_walk_visits_every_root_once",
	"walk_of_single_root_pages_visits_every_root_once",
	"helper_in_another_project_rides_with_its_root",
	"project_hash_filter_keeps_only_roots_of_that_project",
	"a_helper_whose_parent_is_not_published_is_a_root",
	"a_parent_owned_by_someone_else_is_not_a_parent",
	"nested_helpers_are_one_family_ordered_by_depth",
	"parent_cycles_are_unreachable_and_the_read_terminates",
	"other_owners_transcripts_never_appear",
	"every_visibility_is_listed_with_its_own_visibility",
	"session_start_is_carried_only_when_known",
	"latest_status_reports_all_five_statuses_and_already_shared_only_for_live",
	"a_resubmission_after_a_decision_reads_as_live",
	"an_attempt_in_another_collective_is_not_this_collectives_status",
	"a_live_helper_is_marked_live_beside_an_unshared_root",
	"q_matches_a_root_title_case_insensitively",
	"q_matching_only_a_helper_returns_the_whole_family",
	"q_matches_an_exact_transcript_id_only",
	"q_matches_project_display_names",
	"q_with_no_match_returns_an_empty_page",
	"q_is_taken_literally_not_as_a_pattern",
	"a_blank_q_is_no_search",
	"provider_keeps_families_holding_a_session_of_that_provider",
	"q_and_provider_combine_with_and",
	"q_and_provider_may_be_satisfied_by_different_members_of_one_family",
	"a_cursor_walk_under_a_filter_visits_each_family_once",
	"both_route_prefixes_serve_the_same_page",
	"non_member_403",
	"an_unknown_view_is_refused_naming_the_accepted_values",
	"limit_must_be_a_whole_number_of_roots_from_one_to_five_hundred",
	"a_malformed_request_is_refused_before_anything_is_read",
	"a_cursor_that_is_not_ours_is_refused",
}

func loadContributionPagedCases(t *testing.T) []pagedCase {
	t.Helper()
	cases, err := decodeFixtureRows[pagedCase](contributionPagedYAML)
	if err != nil {
		t.Fatalf("load the paged contribution corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" {
			t.Fatalf("a paged contribution case has no name; every case is addressed by name")
		}
		if present[c.Name] {
			t.Fatalf("the paged contribution corpus repeats case %q", c.Name)
		}
		present[c.Name] = true
		if c.Why == "" {
			t.Fatalf("case %q states no reason it exists", c.Name)
		}
		if len(c.Transcripts) == 0 {
			t.Fatalf("case %q builds no transcript, so it asks the route nothing", c.Name)
		}
		if len(c.Requests) == 0 && c.Walk == nil {
			t.Fatalf("case %q makes no request", c.Name)
		}
		for _, r := range c.Requests {
			if r.ExpectStatus == 0 {
				t.Fatalf("case %q has a request with no expect_status", c.Name)
			}
		}
	}
	for _, required := range requiredContributionPagedCases {
		if !present[required] {
			t.Fatalf("the paged contribution corpus no longer contains %q. That case exists because losing it hides a "+
				"real failure; restore it rather than deleting it from this manifest.", required)
		}
	}
	return cases
}

// pagedWorld is one member's library plus the mounted routes that read it.
type pagedWorld struct {
	*contributeWorld
	router     http.Handler
	token      string
	otherGroup pgtype.UUID
	spec       pagedCase
	// base is the one instant every age in the case is measured back from, so two
	// transcripts given the same age really do share one session time.
	base time.Time
}

func (w *pagedWorld) localID(key string) string { return "paged-" + w.suffix + "-" + key }

func (w *pagedWorld) projectHashOf(key string) string {
	sum := sha256.Sum256([]byte("paged:" + w.suffix + ":" + key))
	return hex.EncodeToString(sum[:])
}

func (w *pagedWorld) insert(t *testing.T, ctx context.Context, ordinal int, spec pagedTranscriptSpec) {
	t.Helper()
	owner := w.member
	if spec.Owner == "other" {
		owner = w.other
	}
	visibility := spec.Visibility
	if visibility == "" {
		visibility = "shared"
	}
	provider := spec.Provider
	if provider == "" {
		provider = "claude-code"
	}
	title := spec.Title
	if title == "" {
		title = "title-" + spec.Key
	}
	var parent pgtype.Text
	switch {
	case spec.Parent != "":
		parent = pgtype.Text{String: w.localID(spec.Parent), Valid: true}
	case spec.ParentMissing != "":
		parent = pgtype.Text{String: spec.ParentMissing + "-" + w.suffix, Valid: true}
	}
	var sessionAge pgtype.Int4
	if spec.SessionAge != nil {
		sessionAge = pgtype.Int4{Int32: int32(*spec.SessionAge), Valid: true}
	}
	publishedAge := 100000 + ordinal
	if spec.PublishedAge != nil {
		publishedAge = *spec.PublishedAge
	}
	projectName := pgtype.Text{}
	if name, ok := w.spec.Projects[spec.Project]; ok {
		projectName = pgtype.Text{String: name, Valid: true}
	}
	localID := w.localID(spec.Key)
	id := toPgUUID(uuid.New())

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transcript fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatalf("declare the system actor for the transcript fixture: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key,
		                         blob_size_bytes, schema_version, content_hash, wrapped_data_key, encryption_algorithm,
		                         key_version, project_hash, project_name, parent_session_id, git_branch, session_origin,
		                         published_at, session_start)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'0.1.0',$10,$11,'aes-256-gcm-random-nonce-v1',1,$12,$13,$14,'main','user',
		        $17::timestamptz - ($15 * interval '1 minute'),
		        CASE WHEN $16::int IS NULL THEN NULL ELSE $17::timestamptz - ($16::int * interval '1 minute') END)
	`, id, owner, localID, title, visibility, provider, "m-"+spec.Key, "blob/"+localID, int64(len(localID)),
		hex.EncodeToString(sha256.New().Sum([]byte(localID))[:16]), []byte("fixture-wrapped-data-key"),
		w.projectHashOf(spec.Project), projectName, parent, publishedAge, sessionAge, w.base); err != nil {
		t.Fatalf("insert transcript %s: %v", spec.Key, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit transcript fixture %s: %v", spec.Key, err)
	}
	w.transcripts[spec.Key] = id
	w.localIDs[spec.Key] = localID
}

// appendHistory writes a transcript's attempt ledger directly, one event per
// status, numbered from one. A status prefixed "other:" lands in a second
// collective.
func (w *pagedWorld) appendHistory(t *testing.T, ctx context.Context, spec pagedTranscriptSpec) {
	t.Helper()
	next := map[bool]int{}
	for _, entry := range spec.History {
		elsewhere := strings.HasPrefix(entry, "other:")
		status := strings.TrimPrefix(entry, "other:")
		group := w.group
		if elsewhere {
			if !w.otherGroup.Valid {
				if err := w.pool.QueryRow(ctx, `
					INSERT INTO groups (name, created_by, acceptance_mode) VALUES ($1, $2, 'open') RETURNING id
				`, "paged-other-"+w.suffix, w.moderator).Scan(&w.otherGroup); err != nil {
					t.Fatalf("create the second collective: %v", err)
				}
			}
			group = w.otherGroup
		}
		next[elsewhere]++
		if _, err := w.pool.Exec(ctx, `
			INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status) VALUES ($1, $2, $3, $4)
		`, w.transcripts[spec.Key], group, next[elsewhere], status); err != nil {
			t.Fatalf("record %s event %q for %s: %v", spec.Key, entry, spec.Key, err)
		}
	}
}

// expand turns a token-bearing query string into a request target.
func (w *pagedWorld) expand(t *testing.T, query, cursor string) string {
	t.Helper()
	out := query
	for key, id := range w.transcripts {
		idText := uuid.UUID(id.Bytes).String()
		out = strings.ReplaceAll(out, "{id:"+key+"}", idText)
		out = strings.ReplaceAll(out, "{idprefix:"+key+"}", idText[:8])
	}
	for key := range w.hashes {
		out = strings.ReplaceAll(out, "{project:"+key+"}", w.hashes[key])
	}
	for _, spec := range w.spec.Transcripts {
		out = strings.ReplaceAll(out, "{project:"+spec.Project+"}", w.projectHashOf(spec.Project))
	}
	out = strings.ReplaceAll(out, "{cursor}", cursor)
	if strings.Contains(out, "{") {
		t.Fatalf("case %s: the query %q has a token no transcript, project or cursor supplies", w.caseName, out)
	}
	return out
}

func (w *pagedWorld) get(prefix, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1"+prefix+"/"+w.groupID()+"/contributable?"+query, nil)
	req.Header.Set("Authorization", "Bearer "+w.token)
	rec := httptest.NewRecorder()
	w.router.ServeHTTP(rec, req)
	return rec
}

// keyOf maps a transcript id back to the key the case gave it.
func (w *pagedWorld) keyOf(id string) string {
	for key, pg := range w.transcripts {
		if uuid.UUID(pg.Bytes).String() == id {
			return key
		}
	}
	return "<unknown:" + id + ">"
}

// expectedOrder expands tie groups ("a|b") into the ascending-id order the page
// must show for roots that share one session time.
func (w *pagedWorld) expectedOrder(entries []string) []string {
	var out []string
	for _, entry := range entries {
		if !strings.Contains(entry, "|") {
			out = append(out, entry)
			continue
		}
		keys := strings.Split(entry, "|")
		sort.Slice(keys, func(i, j int) bool {
			return uuid.UUID(w.transcripts[keys[i]].Bytes).String() < uuid.UUID(w.transcripts[keys[j]].Bytes).String()
		})
		out = append(out, keys...)
	}
	return out
}

type pagedRawMember struct {
	Transcript map[string]any `json:"transcript"`
	Matched    *bool          `json:"matched"`
}

type pagedRawPage struct {
	GroupID string `json:"group_id"`
	Items   []struct {
		Root    pagedRawMember   `json:"root"`
		Helpers []pagedRawMember `json:"helpers"`
	} `json:"items"`
	NextCursor *string `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

// decodePage reads a 200 body twice: strictly into the contract's own types, so
// an invented field or a malformed value fails, and loosely, so the presence and
// absence of optional fields can be asserted.
func (w *pagedWorld) decodePage(t *testing.T, body []byte) (pagedRawPage, schema.VillageContributionPageResponse) {
	t.Helper()
	var contract schema.VillageContributionPageResponse
	strict := json.NewDecoder(strings.NewReader(string(body)))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&contract); err != nil {
		t.Fatalf("the page does not decode into the contract's VillageContributionPageResponse: %v\nbody: %s", err, body)
	}
	var raw pagedRawPage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode the page: %v\nbody: %s", err, body)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(body, &shape); err != nil {
		t.Fatal(err)
	}
	if string(shape["items"]) == "null" || shape["items"] == nil {
		t.Fatalf("items must be an array, never null or absent: %s", body)
	}
	if raw.GroupID != w.groupID() {
		t.Errorf("the page names collective %q, want %q", raw.GroupID, w.groupID())
	}
	if raw.HasMore != (raw.NextCursor != nil) {
		t.Errorf("has_more=%v but next_cursor present=%v; the cursor exists exactly when another page does", raw.HasMore, raw.NextCursor != nil)
	}
	if _, present := shape["total"]; present {
		t.Errorf("the page carries a count; counts are the project picker's, not the page's")
	}
	return raw, contract
}

func TestListContributablePaged(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	const secret = "disposable-paged-contribution-secret"

	for _, c := range loadContributionPagedCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			member := c.Member == nil || *c.Member
			world := &pagedWorld{
				contributeWorld: newContributeWorld(t, ctx, pool, c.Name, "open", member),
				spec:            c,
				base:            time.Now().UTC().Truncate(time.Second),
			}
			defer world.cleanup(t, ctx)
			defer func() {
				if world.otherGroup.Valid {
					if _, err := pool.Exec(ctx, "DELETE FROM groups WHERE id = $1", world.otherGroup); err != nil {
						t.Errorf("cleanup the second collective: %v", err)
					}
				}
			}()

			for ordinal, spec := range c.Transcripts {
				world.insert(t, ctx, ordinal, spec)
			}
			for _, spec := range c.Transcripts {
				world.appendHistory(t, ctx, spec)
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

			cursor := ""
			for i, request := range c.Requests {
				query := world.expand(t, request.Query, cursor)
				rec := world.get("/collectives", query)
				if rec.Code != request.ExpectStatus {
					t.Fatalf("request %d (%s) answered %d, want %d (%s)\nbody: %s", i, query, rec.Code, request.ExpectStatus, c.Why, rec.Body.String())
				}
				if request.ExpectStatus != http.StatusOK {
					var body struct {
						Error string `json:"error"`
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || strings.TrimSpace(body.Error) == "" {
						t.Fatalf("request %d (%s): the refusal carries no message; a person cannot act on a bare status (%s)\nbody: %s", i, query, c.Why, rec.Body.String())
					}
					for _, want := range request.ExpectErrorContains {
						if !strings.Contains(body.Error, want) {
							t.Errorf("request %d (%s): the refusal %q does not mention %q (%s)", i, query, body.Error, want, c.Why)
						}
					}
					continue
				}
				raw, _ := world.decodePage(t, rec.Body.Bytes())
				if raw.NextCursor != nil {
					cursor = *raw.NextCursor
				}
				world.assertPage(t, i, query, request, raw, c.Why)
				if request.BothPrefixes {
					legacy := world.get("/groups", query)
					if legacy.Code != rec.Code || legacy.Body.String() != rec.Body.String() {
						t.Errorf("request %d: /groups answered %d %s, /collectives answered %d %s; the alias must serve the same page byte for byte",
							i, legacy.Code, legacy.Body.String(), rec.Code, rec.Body.String())
					}
				}
			}

			if c.Walk != nil {
				world.assertWalk(t, *c.Walk, c.Why)
			}
		})
	}
}

func (w *pagedWorld) assertPage(t *testing.T, index int, query string, request pagedRequestSpec, page pagedRawPage, why string) {
	t.Helper()
	label := func(format string, args ...any) string {
		return "request " + string(rune('0'+index)) + " (" + query + "): " + format
	}
	var gotRoots []string
	members := map[string]pagedRawMember{}
	gotHelpers := map[string][]string{}
	for _, item := range page.Items {
		rootKey := w.keyOf(item.Root.Transcript["id"].(string))
		gotRoots = append(gotRoots, rootKey)
		members[rootKey] = item.Root
		if item.Helpers == nil {
			t.Errorf(label("helpers of %s is null; an empty family is an empty array"), rootKey)
		}
		for _, helper := range item.Helpers {
			key := w.keyOf(helper.Transcript["id"].(string))
			gotHelpers[rootKey] = append(gotHelpers[rootKey], key)
			members[key] = helper
		}
	}
	wantRoots := w.expectedOrder(request.ExpectRoots)
	if strings.Join(gotRoots, ",") != strings.Join(wantRoots, ",") {
		t.Fatalf(label("roots are %v, want %v (%s)"), gotRoots, wantRoots, why)
	}
	for _, rootKey := range wantRoots {
		if strings.Join(gotHelpers[rootKey], ",") != strings.Join(request.ExpectHelpers[rootKey], ",") {
			t.Errorf(label("helpers of %s are %v, want %v (%s)"), rootKey, gotHelpers[rootKey], request.ExpectHelpers[rootKey], why)
		}
	}
	if page.HasMore != request.ExpectHasMore {
		t.Errorf(label("has_more is %v, want %v (%s)"), page.HasMore, request.ExpectHasMore, why)
	}

	if request.ExpectMatched == nil {
		for key, member := range members {
			if member.Matched != nil {
				t.Errorf(label("member %s carries matched=%v although no q was sent"), key, *member.Matched)
			}
		}
	} else {
		for key, member := range members {
			want, listed := request.ExpectMatched[key]
			if !listed {
				t.Errorf(label("member %s is on the page but the case does not say whether it matched"), key)
				continue
			}
			if member.Matched == nil {
				t.Errorf(label("member %s carries no matched although q was sent (%s)"), key, why)
			} else if *member.Matched != want {
				t.Errorf(label("member %s matched=%v, want %v (%s)"), key, *member.Matched, want, why)
			}
		}
	}

	if request.ExpectAlreadyShared != nil {
		want := map[string]bool{}
		for _, key := range request.ExpectAlreadyShared {
			want[key] = true
		}
		for key, member := range members {
			if got, _ := member.Transcript["already_shared"].(bool); got != want[key] {
				t.Errorf(label("member %s already_shared=%v, want %v (%s)"), key, got, want[key], why)
			}
		}
	}
	for key, want := range request.ExpectLatestStatus {
		member, ok := members[key]
		if !ok {
			t.Errorf(label("member %s is not on the page"), key)
			continue
		}
		got, present := member.Transcript["latest_status"]
		if want == "none" {
			if present {
				t.Errorf(label("member %s carries latest_status=%v, want none (%s)"), key, got, why)
			}
			continue
		}
		if got != want {
			t.Errorf(label("member %s latest_status=%v, want %q (%s)"), key, got, want, why)
		}
	}
	for key, projectKey := range request.ExpectProject {
		if got := members[key].Transcript["project_hash"]; got != w.projectHashOf(projectKey) {
			t.Errorf(label("member %s is in project %v, want project %s (%s)"), key, got, projectKey, why)
		}
	}
	for key, want := range request.ExpectProjectName {
		if got := members[key].Transcript["project_display_name"]; got != want {
			t.Errorf(label("member %s renders project name %v, want %q (%s)"), key, got, want, why)
		}
	}
	for key, want := range request.ExpectParentTranscript {
		got, present := members[key].Transcript["parent_transcript_id"]
		if want == "none" {
			if present {
				t.Errorf(label("member %s carries parent_transcript_id=%v, want none (%s)"), key, got, why)
			}
			continue
		}
		if wantID := uuid.UUID(w.transcripts[want].Bytes).String(); got != wantID {
			t.Errorf(label("member %s parent_transcript_id=%v, want %s's id (%s)"), key, got, want, why)
		}
	}
	for key, want := range request.ExpectSessionStart {
		_, present := members[key].Transcript["session_start"]
		if present != (want == "present") {
			t.Errorf(label("member %s session_start present=%v, want %s (%s)"), key, present, want, why)
		}
	}
	for key, want := range request.ExpectVisibility {
		if got := members[key].Transcript["visibility"]; got != want {
			t.Errorf(label("member %s visibility=%v, want %q (%s)"), key, got, want, why)
		}
	}
}

// assertWalk follows next_cursor from the first page to the last and checks the
// roots seen, in order, across every page.
func (w *pagedWorld) assertWalk(t *testing.T, walk pagedWalkSpec, why string) {
	t.Helper()
	var seen []string
	cursor := ""
	pages := 0
	for {
		query := "view=paged&limit=" + string(rune('0'+walk.Limit))
		if walk.Query != "" {
			query += "&" + walk.Query
		}
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		rec := w.get("/collectives", query)
		if rec.Code != http.StatusOK {
			t.Fatalf("walk page %d (%s) answered %d: %s", pages, query, rec.Code, rec.Body.String())
		}
		page, _ := w.decodePage(t, rec.Body.Bytes())
		pages++
		if len(page.Items) > walk.Limit {
			t.Fatalf("walk page %d holds %d roots, over the limit %d", pages, len(page.Items), walk.Limit)
		}
		for _, item := range page.Items {
			seen = append(seen, w.keyOf(item.Root.Transcript["id"].(string)))
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
		if pages > 50 {
			t.Fatalf("the walk did not end after 50 pages; the cursor is not advancing")
		}
	}
	want := w.expectedOrder(walk.ExpectRoots)
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("the walk visited %v, want %v (%s)", seen, want, why)
	}
	if pages != walk.ExpectPages {
		t.Errorf("the walk took %d pages, want %d (%s)", pages, walk.ExpectPages, why)
	}
}
