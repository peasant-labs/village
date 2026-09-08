//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/helper_group_listing.yaml
var helperGroupListingYAML []byte

type helperListingRow struct {
	Name    string                `yaml:"name"`
	Origin  string                `yaml:"origin"`
	Title   string                `yaml:"title"`
	Purpose schema.SessionPurpose `yaml:"purpose"`
	Owner   string                `yaml:"owner"`
	Time    int                   `yaml:"time"`
	Project string                `yaml:"project"`
	Private bool                  `yaml:"private"`
	Input   *int64                `yaml:"input"`
	Turns   *int32                `yaml:"turns"`
}

type helperListingItem struct {
	Transcript string   `yaml:"transcript"`
	Members    []string `yaml:"members"`
}

type helperListingCase struct {
	Owners       map[string]string   `yaml:"owners"`
	Nested       map[string][]string `yaml:"nested"`
	Name         string              `yaml:"name"`
	Seed         []string            `yaml:"seed"`
	Query        string              `yaml:"query"`
	Ordinary     int                 `yaml:"ordinary"`
	Helpers      int                 `yaml:"helpers"`
	Total        int                 `yaml:"total"`
	Items        []helperListingItem `yaml:"items"`
	Next         []string            `yaml:"next"`
	Mutation     string              `yaml:"mutation"`
	MemberStatus int                 `yaml:"member_status"`
	AfterMembers []string            `yaml:"after_members"`
	Legacy       bool                `yaml:"legacy"`
}

func loadHelperListingFixtures(t *testing.T) (map[string]helperListingRow, []helperListingCase) {
	t.Helper()
	var fixture struct {
		Rows  []helperListingRow  `yaml:"rows"`
		Cases []helperListingCase `yaml:"cases"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(helperGroupListingYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("unexpected trailing YAML: %v", err)
	}
	rows := map[string]helperListingRow{}
	for _, row := range fixture.Rows {
		if _, duplicate := rows[row.Name]; duplicate || row.Name == "" {
			t.Fatalf("duplicate/empty fixture row %q", row.Name)
		}
		if !row.Purpose.IsValid() {
			t.Fatalf("invalid fixture purpose %q", row.Purpose)
		}
		rows[row.Name] = row
	}
	names := map[string]bool{}
	for _, c := range fixture.Cases {
		if names[c.Name] || c.Name == "" {
			t.Fatalf("duplicate/empty fixture case %q", c.Name)
		}
		names[c.Name] = true
		seeded := map[string]bool{}
		for _, name := range c.Seed {
			if rows[name].Name == "" || seeded[name] {
				t.Fatalf("%s: invalid seed %s", c.Name, name)
			}
			seeded[name] = true
		}
		for _, item := range c.Items {
			if item.Transcript != "" && !seeded[item.Transcript] {
				t.Fatalf("%s: unseeded ordinary row", c.Name)
			}
			for _, member := range item.Members {
				if !seeded[member] || rows[member].Purpose != schema.SessionPurposeHelperReview {
					t.Fatalf("%s: invalid helper expectation %s", c.Name, member)
				}
			}
		}
	}
	for _, name := range []string{"owner-retained-pages", "helper-only-search", "owner-excluded", "unresolved-independent", "identical-independent-helpers", "guardian-like-title-negative", "scope-project-profile-collective", "scope-search-replay", "scope-expired", "scope-restart", "scope-missing", "foreign-scope", "foreign-viewer", "extra-member-filter", "authorization-changed", "denied-child-no-widening", "ordinary-origin-filter", "legacy-flat-shape"} {
		if !names[name] {
			t.Fatalf("required real-route fixture %s missing", name)
		}
	}
	for _, name := range []string{"cyclic-owner-search-stability", "trunk-append-vs-replacement", "replacement-saved-identity"} {
		if !names[name] {
			t.Fatalf("required real-route fixture %s missing", name)
		}
	}
	return rows, fixture.Cases
}

func TestGroupedBrowseRegisteredRoutesRealSQL(t *testing.T) {
	rows, cases := loadHelperListingFixtures(t)
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	for caseIndex, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			username := fmt.Sprintf("helper-scope-owner-%d", caseIndex)
			owner := pullInsertUser(t, ctx, pool, int64(980851+caseIndex), username)
			defer cleanupOwners(t, ctx, pool, owner)
			newHandler := func() (*Handler, http.Handler) {
				h := &Handler{cfg: minimalConfig(), pool: pool, queries: sqlc.New(pool), projectNames: projectname.Resolver{Label: schema.RemoteLabel}}
				router := chi.NewRouter()
				router.Route("/api/v1", h.RegisterTranscriptBrowseRoutes)
				return h, router
			}
			h, routes := newHandler()
			idNames := map[string]string{}
			ids := map[string]pgtype.UUID{}
			seedRow := func(name string) {
				row := rows[name]
				if owner, ok := c.Owners[name]; ok {
					row.Owner = owner
				}
				visibility := dbVisibilityPublic
				if row.Private {
					visibility = dbVisibilityPrivate
				}
				stored := govStoreWithOrigin(t, ctx, h, owner, "ses_"+name, sessionorigin.Origin(row.Origin), visibility)
				ids[name] = stored.ID
				idNames[uuid.UUID(stored.ID.Bytes).String()] = name
				relations := []schema.SessionRelationship{}
				if row.Owner != "" {
					local := schema.SessionID("ses_" + row.Owner)
					relation := schema.SessionRelationship{Kind: schema.SessionRelationshipStartedBy, TargetState: schema.RelationshipTargetKnown, TargetLocalID: &local, Evidence: schema.EvidenceNativeTyped}
					if err := relation.Validate(); err != nil {
						t.Fatal(err)
					}
					relations = append(relations, relation)
				}
				encoded, err := json.Marshal(relations)
				if err != nil {
					t.Fatal(err)
				}
				parent := ""
				if row.Owner != "" {
					parent = "ses_" + row.Owner
				}
				_, err = pool.Exec(ctx, `UPDATE transcripts SET title=$2, session_start=$3, project_hash=$4, session_purpose=NULLIF($5,''), session_relationships=$6, parent_session_id=NULLIF($7,''), input_submission_count=$8, turn_count=$9 WHERE id=$1`, stored.ID, row.Title, time.Unix(int64(row.Time), 0), strings.Repeat(row.Project, 64), string(row.Purpose), encoded, parent, row.Input, row.Turns)
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range c.Seed {
				seedRow(name)
			}
			get := func(path, token string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				if token != "" {
					r.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				routes.ServeHTTP(w, r)
				return w
			}
			query, err := url.ParseQuery(c.Query)
			if err != nil {
				t.Fatal(err)
			}
			query.Set("owner", username)
			if !c.Legacy {
				query.Set("view", "grouped")
			}
			path := "/api/v1/transcripts?" + query.Encode()
			response := get(path, "")
			if response.Code != http.StatusOK {
				t.Fatalf("list status %d: %s", response.Code, response.Body)
			}
			if c.Legacy {
				var legacy map[string]json.RawMessage
				if err := json.Unmarshal(response.Body.Bytes(), &legacy); err != nil {
					t.Fatal(err)
				}
				if legacy["items"] != nil || legacy["transcripts"] == nil || legacy["agent_total"] == nil {
					t.Fatalf("legacy shape changed: %s", response.Body)
				}
				var total int
				if err := json.Unmarshal(legacy["total"], &total); err != nil || total != c.Total {
					t.Fatalf("legacy total %d: %v", total, err)
				}
				return
			}
			var payload schema.VillageSessionListPayload
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if err := payload.Validate(); err != nil {
				t.Fatal(err)
			}
			if payload.TotalItems != c.Total || payload.OrdinarySessionTotal != c.Ordinary || payload.HelperThreadTotal != c.Helpers || len(payload.Items) != len(c.Items) {
				t.Fatalf("wrong exact list/counts: %+v", payload)
			}
			var repeat schema.VillageSessionListPayload
			if err := json.Unmarshal(get(path, "").Body.Bytes(), &repeat); err != nil {
				t.Fatal(err)
			}
			// The same saved helper has the same group key even when a search
			// excludes its ordinary owner or another member of a logical cycle.
			baselineQuery := url.Values{"owner": {username}, "view": {"grouped"}, "limit": {"100"}}
			var baseline schema.VillageSessionListPayload
			baselineResponse := get("/api/v1/transcripts?"+baselineQuery.Encode(), "")
			if baselineResponse.Code != 200 {
				t.Fatalf("baseline status %d: %s", baselineResponse.Code, baselineResponse.Body)
			}
			if err := json.Unmarshal(baselineResponse.Body.Bytes(), &baseline); err != nil {
				t.Fatal(err)
			}
			baselineGroups := map[string]string{}
			var walkBaseline func([]schema.VillageSessionListItem)
			visitedGroups := map[string]bool{}
			walkBaseline = func(items []schema.VillageSessionListItem) {
				for _, item := range items {
					for _, group := range item.HelperGroups {
						if visitedGroups[group.GroupID] {
							t.Fatal("recursive or duplicated helper group")
						}
						visitedGroups[group.GroupID] = true
						w := get("/api/v1/transcript-groups/"+group.GroupID+"/members?scope="+group.MemberScope, "")
						if w.Code != 200 {
							t.Fatalf("baseline members: %s", w.Body)
						}
						var members schema.VillageHelperMembersPayload
						if err := json.Unmarshal(w.Body.Bytes(), &members); err != nil {
							t.Fatal(err)
						}
						for _, member := range members.Members {
							if member.Kind != schema.SessionListItemTranscript || member.Transcript == nil || member.Context != nil {
								t.Fatal("member must be a transcript item")
							}
							baselineGroups[idNames[string(member.Transcript.Session.ID)]] = group.GroupID
						}
						walkBaseline(members.Members)
					}
				}
			}
			walkBaseline(baseline.Items)
			for i, item := range payload.Items {
				want := c.Items[i]
				if want.Transcript != "" {
					if item.Transcript == nil || idNames[string(item.Transcript.Session.ID)] != want.Transcript {
						t.Fatalf("item %d lost ordinary %s", i, want.Transcript)
					}
					if !reflect.DeepEqual(item.Transcript.Session.InputSubmissionCount, rows[want.Transcript].Input) || !reflect.DeepEqual(item.Transcript.Session.TurnCount, rows[want.Transcript].Turns) {
						t.Fatal("input and turn measures changed")
					}
				} else if item.Context == nil || item.Transcript != nil {
					t.Fatal("helper-only result fabricated an ordinary owner")
				}
				if len(want.Members) == 0 {
					if len(item.HelperGroups) != 0 {
						t.Fatal("unexpected helpers")
					}
					continue
				}
				if len(item.HelperGroups) != 1 {
					t.Fatalf("expected one helper group: %+v", item)
				}
				group := item.HelperGroups[0]
				for _, name := range want.Members {
					if baselineGroups[name] != group.GroupID {
						t.Fatalf("%s changed group identity under filters", name)
					}
				}
				if group.HelperThreadCount != len(want.Members) || group.GroupID != repeat.Items[i].HelperGroups[0].GroupID {
					t.Fatal("unstable group identity/count")
				}
				memberPath := "/api/v1/transcript-groups/" + group.GroupID + "/members?scope=" + group.MemberScope
				memberResponse := get(memberPath, "")
				assertMembers := func(w *httptest.ResponseRecorder, expected []string) {
					t.Helper()
					if w.Code != 200 {
						t.Fatalf("members status %d: %s", w.Code, w.Body)
					}
					var members schema.VillageHelperMembersPayload
					if err := json.Unmarshal(w.Body.Bytes(), &members); err != nil {
						t.Fatal(err)
					}
					got := []string{}
					for _, member := range members.Members {
						if member.Kind != schema.SessionListItemTranscript || member.Transcript == nil || member.Context != nil {
							t.Fatal("member must be a transcript item")
						}
						got = append(got, idNames[string(member.Transcript.Session.ID)])
					}
					if !reflect.DeepEqual(got, expected) || members.Total != len(expected) {
						t.Fatalf("scoped member IDs=%v total=%d want=%v", got, members.Total, expected)
					}
				}
				assertMembers(memberResponse, want.Members)
				if len(want.Members) > 1 {
					pagedResponse := get(memberPath+"&page=2&limit=1", "")
					if pagedResponse.Code != 200 {
						t.Fatalf("member paging: %s", pagedResponse.Body)
					}
					var paged schema.VillageHelperMembersPayload
					if err := json.Unmarshal(pagedResponse.Body.Bytes(), &paged); err != nil {
						t.Fatal(err)
					}
					if paged.Total != len(want.Members) || len(paged.Members) != 1 || idNames[string(paged.Members[0].Transcript.Session.ID)] != want.Members[1] {
						t.Fatalf("wrong independently paged members: %+v", paged)
					}
				}
				if c.Mutation == "" {
					continue
				}
				token := ""
				switch c.Mutation {
				case "append-review":
					if _, err := pool.Exec(ctx, `UPDATE transcripts SET turn_count=coalesce(turn_count,0)+10 WHERE id=$1`, ids["G1"]); err != nil {
						t.Fatal(err)
					}
				case "replacement":
					seedRow("G4")
				case "expire":
					h.groupedScopes.mu.Lock()
					entry := h.groupedScopes.entries[group.MemberScope]
					entry.expires = time.Now().Add(-time.Second)
					h.groupedScopes.entries[group.MemberScope] = entry
					h.groupedScopes.mu.Unlock()
				case "restart":
					h, routes = newHandler()
				case "missing":
					memberPath = "/api/v1/transcript-groups/" + group.GroupID + "/members"
				case "foreign-group":
					memberPath = strings.Replace(memberPath, group.GroupID, group.GroupID+"foreign", 1)
				case "foreign-viewer":
					token, err = auth.CreateToken(h.cfg.JWTSecret, uuid.UUID(owner.Bytes), username)
					if err != nil {
						t.Fatal(err)
					}
				case "extra-filter":
					memberPath += "&q=needle"
				case "hide-second":
					if err := h.inTxAs(ctx, owner, func(q Querier) error {
						private := dbVisibilityPrivate
						_, e := applyMetadataPatch(ctx, q, ids["G2"], metadataPatch{Visibility: &private})
						return e
					}); err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatalf("unhandled fixture mutation %s", c.Mutation)
				}
				changed := get(memberPath, token)
				if c.MemberStatus != 0 {
					if changed.Code != c.MemberStatus {
						t.Fatalf("changed scope status %d: %s", changed.Code, changed.Body)
					}
					if c.MemberStatus == 409 && (!strings.Contains(changed.Body.String(), "group_scope_expired") || !strings.Contains(changed.Body.String(), "refresh")) {
						t.Fatalf("nonactionable scope expiry: %s", changed.Body)
					}
				} else {
					assertMembers(changed, c.AfterMembers)
				}
			}
			if len(c.Next) != 0 {
				query.Set("page", "2")
				var next schema.VillageSessionListPayload
				if err := json.Unmarshal(get("/api/v1/transcripts?"+query.Encode(), "").Body.Bytes(), &next); err != nil {
					t.Fatal(err)
				}
				got := []string{}
				for _, item := range next.Items {
					got = append(got, idNames[string(item.Transcript.Session.ID)])
				}
				if !reflect.DeepEqual(got, c.Next) {
					t.Fatalf("second page %v want %v", got, c.Next)
				}
			}
		})
	}
}
