package handler

import (
	_ "embed"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/peasant-labs/schema"
)

//go:embed testdata/contribution-page-request.yaml
var contributionPageRequestYAML []byte

type contributionPageIssuedFor struct {
	Query  string `yaml:"query"`
	Viewer string `yaml:"viewer"`
	Group  string `yaml:"group"`
}

type contributionPageExpect struct {
	Limit       int    `yaml:"limit"`
	Q           string `yaml:"q"`
	Provider    string `yaml:"provider"`
	ProjectHash string `yaml:"project_hash"`
	Cursor      bool   `yaml:"cursor"`
}

type contributionPageRequestCase struct {
	Name          string                     `yaml:"name"`
	Why           string                     `yaml:"why"`
	Query         string                     `yaml:"query"`
	Viewer        string                     `yaml:"viewer"`
	Group         string                     `yaml:"group"`
	IssuedFor     *contributionPageIssuedFor `yaml:"issued_for"`
	UseCursor     bool                       `yaml:"use_cursor"`
	RawCursorJSON string                     `yaml:"raw_cursor_json"`
	Expect        *contributionPageExpect    `yaml:"expect"`
	ExpectRefusal []string                   `yaml:"expect_refusal"`
}

var requiredContributionPageRequestCases = []string{
	"an_empty_query_reads_the_first_page_of_the_default_size",
	"limit_zero_is_refused",
	"limit_over_the_maximum_is_refused",
	"an_empty_limit_is_refused",
	"q_is_trimmed_and_blank_q_is_no_search",
	"a_blank_q_is_absent",
	"q_with_a_nul_byte_is_refused",
	"a_project_hash_must_be_a_project_identity",
	"a_cursor_from_the_same_request_is_accepted",
	"a_cursor_for_another_viewer_is_refused",
	"a_cursor_for_another_collective_is_refused",
	"a_cursor_under_a_different_search_is_refused",
	"a_cursor_under_a_different_provider_is_refused",
	"a_cursor_under_a_different_project_is_refused",
	"a_cursor_that_is_not_base64_is_refused",
	"a_cursor_that_is_not_a_position_is_refused",
}

func TestContributionPageRequest(t *testing.T) {
	cases, err := decodeFixtureRows[contributionPageRequestCase](contributionPageRequestYAML)
	if err != nil {
		t.Fatalf("load the paged request corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" {
			t.Fatalf("every case needs a name and a reason; got name %q", c.Name)
		}
		if present[c.Name] {
			t.Fatalf("the corpus repeats case %q", c.Name)
		}
		present[c.Name] = true
	}
	for _, required := range requiredContributionPageRequestCases {
		if !present[required] {
			t.Fatalf("the paged request corpus no longer contains %q; restore it rather than dropping it from the manifest", required)
		}
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			viewer, group := c.Viewer, c.Group
			if viewer == "" {
				viewer = "viewer-1"
			}
			if group == "" {
				group = "group-1"
			}
			query := c.Query
			if c.UseCursor {
				issuedQuery, err := url.ParseQuery(c.IssuedFor.Query)
				if err != nil {
					t.Fatal(err)
				}
				issued, problem := contributionPageParams(issuedQuery, c.IssuedFor.Viewer, c.IssuedFor.Group)
				if problem != "" {
					t.Fatalf("the query a cursor is issued under was refused: %s", problem)
				}
				cursor := encodeContributionCursor(time.UnixMicro(1_700_000_000_123_456), uuid.MustParse("00000000-0000-0000-0000-000000000001"),
					contributionCursorFingerprint(c.IssuedFor.Viewer, c.IssuedFor.Group, issued))
				query += "&cursor=" + cursor
			}
			if c.RawCursorJSON != "" {
				issuedFingerprint := contributionCursorFingerprint(viewer, group, contributionPageRequest{Limit: schema.VillageContributionPageLimitDefault})
				raw := strings.ReplaceAll(c.RawCursorJSON, "{fp}", issuedFingerprint)
				query += "&cursor=" + base64.RawURLEncoding.EncodeToString([]byte(raw))
			}
			values, err := url.ParseQuery(query)
			if err != nil {
				t.Fatal(err)
			}
			got, problem := contributionPageParams(values, viewer, group)

			if c.ExpectRefusal != nil {
				if problem == "" {
					t.Fatalf("the request was accepted, want a refusal naming %v (%s)", c.ExpectRefusal, c.Why)
				}
				for _, word := range c.ExpectRefusal {
					if !strings.Contains(problem, word) {
						t.Errorf("the refusal %q does not mention %q (%s)", problem, word, c.Why)
					}
				}
				return
			}
			if problem != "" {
				t.Fatalf("the request was refused: %s (%s)", problem, c.Why)
			}
			want := c.Expect
			if got.Limit != want.Limit || got.Q != want.Q || got.Provider != want.Provider || got.ProjectHash != want.ProjectHash || (got.Cursor != nil) != want.Cursor {
				t.Errorf("parsed %+v (cursor %v), want %+v (%s)", got, got.Cursor != nil, *want, c.Why)
			}
			if got.Cursor != nil && (got.Cursor.SortMicros != 1_700_000_000_123_456 || got.Cursor.RootID != "00000000-0000-0000-0000-000000000001") {
				t.Errorf("the cursor lost its position: %+v", got.Cursor)
			}
		})
	}
}

// The default and the bounds are the contract's own constants, so the handler
// cannot drift from the declared range.
func TestContributionPageLimitMatchesTheContract(t *testing.T) {
	values, _ := url.ParseQuery("view=paged")
	req, problem := contributionPageParams(values, "v", "g")
	if problem != "" || req.Limit != schema.VillageContributionPageLimitDefault {
		t.Fatalf("default limit = %d (%q), want the contract default %d", req.Limit, problem, schema.VillageContributionPageLimitDefault)
	}
	for _, limit := range []string{"1", "500"} {
		values, _ := url.ParseQuery("limit=" + limit)
		if _, problem := contributionPageParams(values, "v", "g"); problem != "" {
			t.Errorf("limit %s is inside the contract's range but was refused: %s", limit, problem)
		}
	}
}

// The dispatcher answers the views that need no database itself.
func TestDispatchContributableViewMenu(t *testing.T) {
	h := &Handler{}
	for _, view := range []string{"pagd", "PAGED", "flat", "%20"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/collectives/x/contributable?view="+view, nil)
		if !h.dispatchContributableView(rec, req) {
			t.Errorf("view=%s was not answered by the dispatcher", view)
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("view=%s answered %d, want 400 (body %s)", view, rec.Code, rec.Body.String())
		}
		for _, accepted := range []string{"grouped", "paged", "snapshot"} {
			if !strings.Contains(rec.Body.String(), accepted) {
				t.Errorf("view=%s: the refusal does not name %q: %s", view, accepted, rec.Body.String())
			}
		}
	}
	rec := httptest.NewRecorder()
	if h.dispatchContributableView(rec, httptest.NewRequest(http.MethodGet, "/api/v1/collectives/x/contributable", nil)) {
		t.Errorf("an omitted view must fall through to the flat response")
	}
	if rec.Body.Len() != 0 {
		t.Errorf("an omitted view wrote %q before falling through", rec.Body.String())
	}
}
