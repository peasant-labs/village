package handler

// One aggregate per surface, whatever the number of collectives.
//
// Both collectives surfaces answer a whole page from a single statement. The
// realistic regression is a per-collective follow-up query added later for one
// extra field, which stays invisible to any test that builds one collective.
// These guards therefore drive several and count the calls.

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/collectives_batch/batch_cases.yaml
var collectivesBatchCasesYAML []byte

type collectivesBatchCase struct {
	Name        string   `yaml:"name"`
	Collectives []string `yaml:"collectives"`
}

// requiredCollectivesBatchCases names the cases that must exist. It lives here
// rather than inside the fixture so deleting the fixture's rows cannot delete
// the manifest that protects them.
var requiredCollectivesBatchCases = []string{
	"transcript in many collectives is answered by one query",
	"contributions across many collectives are answered by one query",
}

func loadCollectivesBatchCase(t *testing.T, name string) collectivesBatchCase {
	t.Helper()
	cases, err := decodeFixtureRows[collectivesBatchCase](collectivesBatchCasesYAML)
	if err != nil {
		t.Fatalf("load the collectives batch fixture: %v", err)
	}
	present := map[string]collectivesBatchCase{}
	for _, c := range cases {
		if _, repeated := present[c.Name]; repeated {
			t.Fatalf("the collectives batch fixture repeats case %q", c.Name)
		}
		if len(c.Collectives) < 2 {
			t.Fatalf("case %q names %d collective(s); a one-collective case cannot tell one query from a query per "+
				"collective", c.Name, len(c.Collectives))
		}
		present[c.Name] = c
	}
	for _, required := range requiredCollectivesBatchCases {
		if _, exists := present[required]; !exists {
			t.Fatalf("the collectives batch fixture omits required case %q; restore it rather than removing it from the "+
				"manifest", required)
		}
	}
	found, ok := present[name]
	if !ok {
		t.Fatalf("the collectives batch fixture has no case %q", name)
	}
	return found
}

func collectivesBatchRows(names []string) []sqlc.ListTranscriptCollectivesForViewerRow {
	rows := make([]sqlc.ListTranscriptCollectivesForViewerRow, 0, len(names))
	for i, name := range names {
		rows = append(rows, sqlc.ListTranscriptCollectivesForViewerRow{
			ID:   pgtype.UUID{Bytes: [16]byte{byte(i + 1)}, Valid: true},
			Name: name,
		})
	}
	return rows
}

// TestTranscriptCollectivesUsesOneAggregate proves the per-transcript surface
// reaches the database once for the memberships however many come back, and
// that it hands the query the viewer's identity and whether that viewer owns the
// transcript - the two inputs both of its gates depend on.
func TestTranscriptCollectivesUsesOneAggregate(t *testing.T) {
	fixture := loadCollectivesBatchCase(t, "transcript in many collectives is answered by one query")

	ownerID := pgtype.UUID{Bytes: [16]byte{9}, Valid: true}
	viewer := &AuthUser{ID: uuid.UUID([16]byte{7})}
	transcriptID := pgtype.UUID{Bytes: [16]byte{3}, Valid: true}
	membershipQueries := 0

	q := &mockQuerier{
		getTranscriptByID: func(_ context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
			return sqlc.Transcript{ID: id, OwnerID: ownerID, Visibility: "public"}, nil
		},
		listTranscriptCollectivesForViewer: func(_ context.Context, arg sqlc.ListTranscriptCollectivesForViewerParams) ([]sqlc.ListTranscriptCollectivesForViewerRow, error) {
			membershipQueries++
			if arg.TranscriptID != transcriptID {
				t.Fatalf("the membership query asked about %+v, want the requested transcript %+v", arg.TranscriptID, transcriptID)
			}
			if arg.UserID != viewer.PgID() {
				t.Fatalf("the membership query carried viewer %+v, want the signed-in caller %+v; without it the "+
					"member branch of the visibility predicate can never match", arg.UserID, viewer.PgID())
			}
			if arg.ViewerIsOwner {
				t.Fatal("the membership query claimed the viewer owns the transcript; this viewer does not, and the " +
					"contributor opt-in gate would then never apply to them")
			}
			return collectivesBatchRows(fixture.Collectives), nil
		},
	}

	h := &Handler{queries: q}
	rec := httptest.NewRecorder()
	h.ListTranscriptCollectives(rec, collectivesBatchRequest(viewer, map[string]string{
		"id": uuid.UUID(transcriptID.Bytes).String(),
	}))

	if rec.Code != http.StatusOK {
		t.Fatalf("the per-transcript surface answered %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := collectivesBatchBodyLen(t, rec.Body.Bytes()); got != len(fixture.Collectives) {
		t.Fatalf("the body carries %d collective(s), want %d", got, len(fixture.Collectives))
	}
	if membershipQueries != 1 {
		t.Fatalf("the per-transcript surface ran %d membership queries for %d collectives, want exactly 1: the "+
			"memberships come from one aggregate, never one query per collective",
			membershipQueries, len(fixture.Collectives))
	}
}

// TestOwnerContributionsUsesOneAggregate proves the contributions surface reads
// the three counters from a single statement, and that it asks only about the
// authenticated caller.
func TestOwnerContributionsUsesOneAggregate(t *testing.T) {
	fixture := loadCollectivesBatchCase(t, "contributions across many collectives are answered by one query")

	caller := &AuthUser{ID: uuid.UUID([16]byte{5})}
	contributionQueries := 0

	q := &mockQuerier{
		listOwnerCollectiveContributions: func(_ context.Context, ownerID pgtype.UUID) ([]sqlc.ListOwnerCollectiveContributionsRow, error) {
			contributionQueries++
			if ownerID != caller.PgID() {
				t.Fatalf("the contributions query asked about %+v, want the authenticated caller %+v; this surface has "+
					"no other subject", ownerID, caller.PgID())
			}
			rows := make([]sqlc.ListOwnerCollectiveContributionsRow, 0, len(fixture.Collectives))
			for i, name := range fixture.Collectives {
				rows = append(rows, sqlc.ListOwnerCollectiveContributionsRow{
					ID:   pgtype.UUID{Bytes: [16]byte{byte(i + 1)}, Valid: true},
					Name: name,
				})
			}
			return rows, nil
		},
	}

	h := &Handler{queries: q}
	rec := httptest.NewRecorder()
	h.ListMyCollectiveContributions(rec, collectivesBatchRequest(caller, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("the contributions surface answered %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := collectivesBatchBodyLen(t, rec.Body.Bytes()); got != len(fixture.Collectives) {
		t.Fatalf("the body carries %d collective(s), want %d", got, len(fixture.Collectives))
	}
	if contributionQueries != 1 {
		t.Fatalf("the contributions surface ran %d queries for %d collectives, want exactly 1: the three counters come "+
			"from one aggregate", contributionQueries, len(fixture.Collectives))
	}
}

// TestOwnerContributionsRefusesAnonymousCaller pins that the owner-only surface
// has no anonymous answer. The route is AuthRequired, so this is the handler's
// own half of that guarantee.
func TestOwnerContributionsRefusesAnonymousCaller(t *testing.T) {
	h := &Handler{queries: &mockQuerier{}}
	rec := httptest.NewRecorder()
	h.ListMyCollectiveContributions(rec, collectivesBatchRequest(nil, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("an anonymous caller received %d, want 401; contributions are readable only by the person who made "+
			"them (body: %s)", rec.Code, rec.Body.String())
	}
}

func collectivesBatchRequest(actor *AuthUser, params map[string]string) *http.Request {
	route := chi.NewRouteContext()
	for key, value := range params {
		route.URLParams.Add(key, value)
	}
	ctx := context.Background()
	if actor != nil {
		ctx = context.WithValue(ctx, UserContextKey, actor)
	}
	ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
	return httptest.NewRequest(http.MethodGet, "/api/v1/collectives", nil).WithContext(ctx)
}

func collectivesBatchBodyLen(t *testing.T, body []byte) int {
	t.Helper()
	var decoded struct {
		Collectives []json.RawMessage `json:"collectives"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode the collectives body: %v (body: %s)", err, string(body))
	}
	return len(decoded.Collectives)
}
