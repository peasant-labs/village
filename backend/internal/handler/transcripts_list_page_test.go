package handler

import (
	_ "embed"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
)

// transcriptListPageCase is one fixture row: either an accepted parse or a
// refusal. `kind` closes the set so a row cannot assert nothing.
type transcriptListPageCase struct {
	Name          string   `yaml:"name"`
	Kind          string   `yaml:"kind"`
	Query         string   `yaml:"query"`
	Page          int      `yaml:"page"`
	PerPage       int      `yaml:"per_page"`
	Offset        int      `yaml:"offset"`
	RefusedValue  string   `yaml:"refused_value"`
	ErrorContains []string `yaml:"error_contains"`
}

//go:embed testdata/transcript_list_page_params.yaml
var transcriptListPageCasesYAML []byte

// TestParseTranscriptListPage pins the page/per_page contract at the handler's
// boundary: an omitted maximum is 25, only schema.AllPageSizes members are
// accepted, and everything else is refused before a read runs.
func TestParseTranscriptListPage(t *testing.T) {
	cases := loadFixtureRows[transcriptListPageCase](t, transcriptListPageCasesYAML, 9)
	sawAccepted, sawRefused := 0, 0
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			q, err := url.ParseQuery(c.Query)
			if err != nil {
				t.Fatalf("fixture query %q does not parse: %v", c.Query, err)
			}
			got, err := parseTranscriptListPage(q)
			switch c.Kind {
			case "accepted":
				sawAccepted++
				if err != nil {
					t.Fatalf("parseTranscriptListPage(%q) error = %v, want the case accepted", c.Query, err)
				}
				if got.page != c.Page {
					t.Errorf("page = %d, want %d", got.page, c.Page)
				}
				if int(got.perPage) != c.PerPage {
					t.Errorf("per_page = %d, want %d", int(got.perPage), c.PerPage)
				}
				if got.offset != c.Offset {
					t.Errorf("offset = %d, want %d", got.offset, c.Offset)
				}
				if !got.perPage.IsValid() {
					t.Errorf("per_page %d is not a schema.AllPageSizes member", int(got.perPage))
				}
			case "refused":
				sawRefused++
				if err == nil {
					t.Fatalf("parseTranscriptListPage(%q) accepted, want a refusal", c.Query)
				}
				if !strings.Contains(c.Query, "per_page="+c.RefusedValue) && !strings.Contains(c.Query, "per_page="+url.QueryEscape(c.RefusedValue)) {
					t.Fatalf("fixture query %q does not carry the refused value %q", c.Query, c.RefusedValue)
				}
				for _, fragment := range c.ErrorContains {
					if !strings.Contains(err.Error(), fragment) {
						t.Errorf("refusal %q missing fragment %q", err.Error(), fragment)
					}
				}
			default:
				t.Fatalf("fixture kind %q is not accepted or refused", c.Kind)
			}
		})
	}
	if sawAccepted == 0 || sawRefused == 0 {
		t.Fatalf("fixture covers accepted=%d refused=%d; both outcomes need a row", sawAccepted, sawRefused)
	}
}

// TestListTranscriptsRefusesOutOfSetPerPage proves the refusal reaches the HTTP
// boundary: an out-of-set per_page is a 400 answered before any read, so a
// hand-built URL cannot silently receive a differently sized page. The handler
// has no pool here, which the read would dereference, so reaching the read
// would panic rather than answer.
func TestListTranscriptsRefusesOutOfSetPerPage(t *testing.T) {
	h := &Handler{}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts?per_page=26", nil)
	w := httptest.NewRecorder()
	h.ListTranscripts(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "per_page 26") {
		t.Errorf("refusal body %q does not name the refused value", body)
	}
}

// TestPageSizeMenuDerivesFromTheContract keeps the refusal text honest: the
// members it lists come from schema.AllPageSizes, never a handler-local copy.
func TestPageSizeMenuDerivesFromTheContract(t *testing.T) {
	want := make([]string, len(schema.AllPageSizes))
	for i, size := range schema.AllPageSizes {
		want[i] = size.String()
	}
	if got := pageSizeMenu(); got != strings.Join(want, ", ") {
		t.Fatalf("pageSizeMenu = %q, want %q from schema.AllPageSizes", got, strings.Join(want, ", "))
	}
}
