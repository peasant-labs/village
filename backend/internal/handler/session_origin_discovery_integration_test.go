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
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
)

//go:embed testdata/session_origin_discovery/cases.yaml
var sessionOriginDiscoveryCasesYAML []byte

type originDiscoveryTranscript struct {
	Name       string `yaml:"name"`
	Origin     string `yaml:"origin"`
	Visibility string `yaml:"visibility"`
	Owner      string `yaml:"owner"`
}

type originDiscoveryRequest struct {
	Name             string   `yaml:"name"`
	Caller           string   `yaml:"caller"`
	OwnerFilter      string   `yaml:"owner_filter"`
	Origin           string   `yaml:"origin"`
	ExpectStatus     int      `yaml:"expect_status"`
	ExpectVisible    []string `yaml:"expect_visible"`
	ExpectTotal      int      `yaml:"expect_total"`
	ExpectAgentTotal int      `yaml:"expect_agent_total"`
}

type originDiscoveryFixture struct {
	Transcripts []originDiscoveryTranscript `yaml:"transcripts"`
	Requests    []originDiscoveryRequest    `yaml:"requests"`
}

const (
	wantOriginDiscoveryTranscripts = 6
	wantOriginDiscoveryRequests    = 8
)

func loadOriginDiscoveryFixture(t *testing.T) originDiscoveryFixture {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(sessionOriginDiscoveryCasesYAML))
	decoder.KnownFields(true)
	var fixture originDiscoveryFixture
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("decode session-origin discovery fixture: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("session-origin discovery fixture must contain exactly one YAML document; got %v", trailing)
	}
	if len(fixture.Transcripts) != wantOriginDiscoveryTranscripts || len(fixture.Requests) != wantOriginDiscoveryRequests {
		t.Fatalf("fixture holds %d transcripts and %d requests, want %d and %d", len(fixture.Transcripts), len(fixture.Requests), wantOriginDiscoveryTranscripts, wantOriginDiscoveryRequests)
	}
	seeded := map[string]originDiscoveryTranscript{}
	origins := map[string]bool{}
	for _, row := range fixture.Transcripts {
		if seeded[row.Name].Name != "" {
			t.Fatalf("fixture repeats transcript name %q", row.Name)
		}
		if _, err := sessionorigin.Parse(row.Origin); err != nil {
			t.Fatalf("transcript %q: %v", row.Name, err)
		}
		if row.Owner != "first" && row.Owner != "second" {
			t.Fatalf("transcript %q has owner %q; expected first or second", row.Name, row.Owner)
		}
		if row.Visibility != dbVisibilityPublic && row.Visibility != dbVisibilityPrivate {
			t.Fatalf("transcript %q has visibility %q; expected public or private", row.Name, row.Visibility)
		}
		seeded[row.Name] = row
		origins[row.Origin] = true
	}
	// The point of the fixture is that all three menu values are seeded and can
	// therefore be told apart in the response.
	for _, origin := range sessionorigin.All {
		if !origins[string(origin)] {
			t.Fatalf("fixture seeds no transcript with origin %q, so that class is not covered", origin)
		}
	}
	names := map[string]bool{}
	for _, request := range fixture.Requests {
		if names[request.Name] {
			t.Fatalf("fixture repeats request name %q", request.Name)
		}
		names[request.Name] = true
		if request.Caller != "anonymous" && request.Caller != "owner" {
			t.Fatalf("request %q has caller %q; expected anonymous or owner", request.Name, request.Caller)
		}
		for _, visible := range request.ExpectVisible {
			if seeded[visible].Name == "" {
				t.Fatalf("request %q expects transcript %q, which the fixture never seeds", request.Name, visible)
			}
		}
		if request.ExpectStatus == http.StatusOK && len(request.ExpectVisible) != request.ExpectTotal {
			t.Fatalf("request %q expects %d visible rows but a total of %d; the single-page fixture must agree with itself", request.Name, len(request.ExpectVisible), request.ExpectTotal)
		}
	}
	return fixture
}

// TestListTranscripts_SessionOriginScope_RealPostgres proves the discovery
// scope on the real handler over real PostgreSQL: agent-driven sessions leave
// the default list, an explicit origin returns exactly that class, unknown rows
// are listed like user rows, the agent tally follows the same filters, and an
// unsupported origin value is refused rather than silently ignored.
func TestListTranscripts_SessionOriginScope_RealPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := loadOriginDiscoveryFixture(t)
	pool := govTestPool(t)
	defer pool.Close()

	first := pullInsertUser(t, ctx, pool, 980741, "origin-scope-first")
	second := pullInsertUser(t, ctx, pool, 980742, "origin-scope-second")
	defer cleanupOwners(t, ctx, pool, first, second)
	h := &Handler{pool: pool, queries: sqlc.New(pool)}

	ownerLogin := map[string]string{"first": "origin-scope-first", "second": "origin-scope-second"}
	idByName := map[string]string{}
	for _, row := range fixture.Transcripts {
		owner := first
		if row.Owner == "second" {
			owner = second
		}
		stored := govStoreWithOrigin(t, ctx, h, owner, "origin-scope-"+row.Name, sessionorigin.Origin(row.Origin), row.Visibility)
		idByName[row.Name] = uuid.UUID(stored.ID.Bytes).String()
	}

	for _, request := range fixture.Requests {
		t.Run(request.Name, func(t *testing.T) {
			url := fmt.Sprintf("/api/v1/transcripts?limit=100&owner=%s", ownerLogin[request.OwnerFilter])
			if request.Origin != "" {
				url += "&origin=" + request.Origin
			}
			r := httptest.NewRequest(http.MethodGet, url, nil)
			if request.Caller == "owner" {
				r = r.WithContext(withUserID(r.Context(), uuid.UUID(first.Bytes)))
			}
			w := httptest.NewRecorder()
			h.ListTranscripts(w, r)
			if w.Code != request.ExpectStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, request.ExpectStatus, w.Body.String())
			}
			if request.ExpectStatus != http.StatusOK {
				if !bytes.Contains(w.Body.Bytes(), []byte(sessionorigin.Menu())) {
					t.Fatalf("refusal body does not name the supported origin values: %s", w.Body.String())
				}
				return
			}

			var resp struct {
				Transcripts []struct {
					Transcript struct {
						ID            string `json:"id"`
						SessionOrigin string `json:"session_origin"`
					} `json:"transcript"`
				} `json:"transcripts"`
				Total      int `json:"total"`
				AgentTotal int `json:"agent_total"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode list response: %v\nbody: %s", err, w.Body.String())
			}

			originByID := map[string]string{}
			var gotIDs []string
			for _, row := range resp.Transcripts {
				gotIDs = append(gotIDs, row.Transcript.ID)
				originByID[row.Transcript.ID] = row.Transcript.SessionOrigin
			}
			var wantIDs []string
			for _, name := range request.ExpectVisible {
				wantIDs = append(wantIDs, idByName[name])
			}
			sort.Strings(gotIDs)
			sort.Strings(wantIDs)
			if fmt.Sprint(gotIDs) != fmt.Sprint(wantIDs) {
				t.Fatalf("listed ids = %v, want %v", gotIDs, wantIDs)
			}
			for _, name := range request.ExpectVisible {
				id := idByName[name]
				var wantOrigin string
				for _, seeded := range fixture.Transcripts {
					if seeded.Name == name {
						wantOrigin = seeded.Origin
					}
				}
				if originByID[id] != wantOrigin {
					t.Fatalf("row %s carries session_origin %q, want %q; the list response must expose the class it grouped on", name, originByID[id], wantOrigin)
				}
			}
			if resp.Total != request.ExpectTotal {
				t.Fatalf("total = %d, want %d", resp.Total, request.ExpectTotal)
			}
			if resp.AgentTotal != request.ExpectAgentTotal {
				t.Fatalf("agent_total = %d, want %d", resp.AgentTotal, request.ExpectAgentTotal)
			}
		})
	}
}

// govStoreWithOrigin creates one transcript through the real publish-create
// path with an explicit session origin and visibility.
func govStoreWithOrigin(t *testing.T, ctx context.Context, h *Handler, owner pgtype.UUID, localID string, origin sessionorigin.Origin, visibility string) sqlc.Transcript {
	t.Helper()
	req := schema.PublishRequest{
		Identity: schema.SessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2},
		Model:    schema.ModelInfo{Harness: "claude-code", Model: "m"},
	}
	params := schemaToTranscriptParams(req, "blob/"+localID, 1, "2", sessionorigin.Unknown)
	params.OwnerID = owner
	params.LocalID = localID
	params.Visibility = visibility
	params.SessionOrigin = origin.String()
	params = completeEncryptedFixtureParams(params)
	var stored sqlc.Transcript
	if err := h.inTxAs(ctx, owner, func(q Querier) error {
		var txErr error
		stored, txErr = q.CreateTranscript(ctx, params)
		return txErr
	}); err != nil {
		t.Fatalf("seed transcript %s with origin %s: %v", localID, origin, err)
	}
	return stored
}

// TestGetTranscript_AgentSessionStillResolves proves the origin scope is
// discovery only. An anonymous caller holding the link to a public
// agent-driven transcript that the default list hides still gets the whole
// record, carrying the class that hid it from the list.
func TestGetTranscript_AgentSessionStillResolves_RealPostgres(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	owner := pullInsertUser(t, ctx, pool, 980743, "origin-deeplink-owner")
	defer cleanupOwners(t, ctx, pool, owner)
	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	stored := govStoreWithOrigin(t, ctx, h, owner, "origin-deeplink-agent", sessionorigin.Agent, dbVisibilityPublic)
	id := uuid.UUID(stored.ID.Bytes).String()

	list := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts?limit=100&owner=origin-deeplink-owner", nil)
	listRecorder := httptest.NewRecorder()
	h.ListTranscripts(listRecorder, list)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listRecorder.Code)
	}
	if bytes.Contains(listRecorder.Body.Bytes(), []byte(id)) {
		t.Fatalf("the default list still carries the agent transcript %s; the deep-link proof needs a row the list hides", id)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id, nil)
	r = withChiURLParam(r, "id", id)
	w := httptest.NewRecorder()
	h.GetTranscript(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("deep-link status = %d, want 200; hiding a session from a list must never block its link (body: %s)", w.Code, w.Body.String())
	}
	var detail struct {
		Transcript struct {
			ID            string `json:"id"`
			SessionOrigin string `json:"session_origin"`
		} `json:"transcript"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail response: %v\nbody: %s", err, w.Body.String())
	}
	if detail.Transcript.ID != id {
		t.Fatalf("detail id = %q, want %q", detail.Transcript.ID, id)
	}
	if sessionorigin.Origin(detail.Transcript.SessionOrigin) != sessionorigin.Agent {
		t.Fatalf("detail session_origin = %q, want %q so a client can label the page", detail.Transcript.SessionOrigin, sessionorigin.Agent)
	}
}
