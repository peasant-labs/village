//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

type discoveryFacetCase struct {
	Name               string   `yaml:"name"`
	Viewer             string   `yaml:"viewer"`
	Query              string   `yaml:"query"`
	ExpectedFacets     []string `yaml:"expectedFacets"`
	ExpectedIDs        []string `yaml:"expectedIds"`
	ExpectedTotal      int64    `yaml:"expectedTotal"`
	ExpectedAgentTotal int64    `yaml:"expectedAgentTotal"`
	ExpectedPage       int      `yaml:"expectedPage"`
	ExpectedLimit      int      `yaml:"expectedLimit"`
}

//go:embed testdata/discovery_pagination/facet_rows.yaml
var discoveryFacetRowsYAML []byte

//go:embed testdata/discovery_pagination/facet_cases.yaml
var discoveryFacetCasesYAML []byte

//go:embed testdata/discovery_pagination/invalid_harness.yaml
var discoveryInvalidHarnessYAML []byte

func TestListTranscripts_HarnessFacets_RealPostgres(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	owner := pullInsertUser(t, ctx, pool, 980803, "facet-owner")
	member := pullInsertUser(t, ctx, pool, 980804, "facet-member")
	defer cleanupOwners(t, ctx, pool, owner, member)
	emptyHandler := &Handler{pool: pool, queries: sqlc.New(pool)}
	emptyRequest := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts?q=none", nil)
	emptyWriter := httptest.NewRecorder()
	emptyHandler.ListTranscripts(emptyWriter, emptyRequest)
	if emptyWriter.Code != http.StatusOK {
		t.Fatalf("empty corpus status %d: %s", emptyWriter.Code, emptyWriter.Body.String())
	}
	var emptyResponse struct {
		HarnessFacets []any `json:"harness_facets"`
	}
	if err := json.Unmarshal(emptyWriter.Body.Bytes(), &emptyResponse); err != nil || emptyResponse.HarnessFacets == nil || len(emptyResponse.HarnessFacets) != 0 {
		t.Fatalf("empty corpus facets must be explicit []: facets=%v decode=%v body=%s", emptyResponse.HarnessFacets, err, emptyWriter.Body.String())
	}
	rows, err := decodeFixtureRows[discoveryRow](discoveryFacetRowsYAML)
	if err != nil {
		t.Fatalf("decode facet rows: %v", err)
	}
	requiredRows := map[string]bool{"public-claude": false, "public-codex-later": false, "private-opencode": false, "shared-cursor": false, "public-agent": false}
	ids := map[string]uuid.UUID{}
	for _, row := range rows {
		if _, ok := requiredRows[row.Name]; !ok {
			t.Fatalf("unexpected facet row %q", row.Name)
		}
		if requiredRows[row.Name] {
			t.Fatalf("duplicate facet row %q", row.Name)
		}
		requiredRows[row.Name] = true
		rowOwner := owner
		if row.Name == "public-codex-later" {
			rowOwner = member
		}
		discoveryInsertRow(t, ctx, pool, rowOwner, row)
		ids[row.Name] = uuid.MustParse(row.ID)
	}
	for name, found := range requiredRows {
		if !found {
			t.Fatalf("required facet row %q missing", name)
		}
	}

	groupA := pullInsertGroup(t, ctx, pool, owner, "facet-a")
	groupB := pullInsertGroup(t, ctx, pool, owner, "facet-b")
	pullAddMember(t, ctx, pool, groupA, member, "member")
	pullAddMember(t, ctx, pool, groupB, member, "member")
	shared := toPgUUID(ids["shared-cursor"])
	pullShare(t, ctx, pool, shared, groupA, "approved")
	pullShare(t, ctx, pool, shared, groupB, "approved")
	for _, tag := range []string{"facet-one", "facet-two"} {
		var tagID uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO tags (name) VALUES ($1) ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, tag).Scan(&tagID); err != nil {
			t.Fatalf("insert tag %q: %v", tag, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO transcript_tags (transcript_id, tag_id) VALUES ($1, $2)`, shared, tagID); err != nil {
			t.Fatalf("attach tag %q: %v", tag, err)
		}
	}

	h := &Handler{pool: pool, queries: sqlc.New(pool)}
	cases, err := decodeFixtureRows[discoveryFacetCase](discoveryFacetCasesYAML)
	if err != nil {
		t.Fatalf("decode facet cases: %v", err)
	}
	requiredCases := map[string]bool{
		"anonymous_page_one": false, "anonymous_later_page": false, "zero_filtered_rows": false,
		"turns_sort_independent": false, "duration_sort_independent": false,
		"query_narrowing_independent": false, "provider_narrowing_independent": false,
		"owner_narrowing_independent": false, "project_narrowing_independent": false,
		"repo_narrowing_independent": false, "org_narrowing_independent": false,
		"tag_narrowing_independent": false, "explicit_origin_independent": false,
		"owner_sees_private": false, "member_sees_shared_once": false,
	}
	for _, c := range cases {
		if _, ok := requiredCases[c.Name]; !ok {
			t.Fatalf("unexpected facet case %q", c.Name)
		}
		if requiredCases[c.Name] {
			t.Fatalf("duplicate facet case %q", c.Name)
		}
		requiredCases[c.Name] = true
		r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts?"+c.Query, nil)
		switch c.Viewer {
		case "owner":
			r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "facet-owner"}))
		case "member":
			r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: uuid.UUID(member.Bytes), Username: "facet-member"}))
		case "anonymous":
		default:
			t.Fatalf("case %q: unknown viewer %q", c.Name, c.Viewer)
		}
		w := httptest.NewRecorder()
		h.ListTranscripts(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("case %q: status %d body %s", c.Name, w.Code, w.Body.String())
		}
		var response struct {
			HarnessFacets []struct {
				Harness string `json:"harness"`
				Count   int64  `json:"count"`
			} `json:"harness_facets"`
			Transcripts []any `json:"transcripts"`
			Total       int64 `json:"total"`
			AgentTotal  int64 `json:"agent_total"`
			Page        int   `json:"page"`
			Limit       int   `json:"limit"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("case %q decode: %v", c.Name, err)
		}
		got := make([]string, 0, len(response.HarnessFacets))
		for _, facet := range response.HarnessFacets {
			got = append(got, facet.Harness+":"+fmt.Sprint(facet.Count))
		}
		if !equalIDs(got, c.ExpectedFacets) {
			t.Errorf("case %q facets = %v, want %v", c.Name, got, c.ExpectedFacets)
		}
		var wireIDs []string
		for _, raw := range response.Transcripts {
			encoded, _ := json.Marshal(raw)
			var wrapper struct {
				Transcript struct {
					ID string `json:"id"`
				} `json:"transcript"`
			}
			if err := json.Unmarshal(encoded, &wrapper); err != nil {
				t.Fatalf("case %q decode transcript: %v", c.Name, err)
			}
			wireIDs = append(wireIDs, wrapper.Transcript.ID)
		}
		if !equalIDs(wireIDs, c.ExpectedIDs) || response.Total != c.ExpectedTotal || response.AgentTotal != c.ExpectedAgentTotal || response.Page != c.ExpectedPage || response.Limit != c.ExpectedLimit {
			t.Errorf("case %q rows/metadata = %v total=%d agent=%d page=%d limit=%d, want %v total=%d agent=%d page=%d limit=%d", c.Name, wireIDs, response.Total, response.AgentTotal, response.Page, response.Limit, c.ExpectedIDs, c.ExpectedTotal, c.ExpectedAgentTotal, c.ExpectedPage, c.ExpectedLimit)
		}
		if c.Name == "zero_filtered_rows" && len(response.Transcripts) != 0 {
			t.Errorf("zero-filter case returned %d rows", len(response.Transcripts))
		}
	}
	for name, found := range requiredCases {
		if !found {
			t.Fatalf("required facet case %q missing", name)
		}
	}
}

func TestListTranscripts_InvalidStoredHarnessDoesNotLogValue_RealPostgres(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	owner := pullInsertUser(t, ctx, pool, 980805, "invalid-harness-owner")
	defer cleanupOwners(t, ctx, pool, owner)
	rows, err := decodeFixtureRows[discoveryRow](discoveryInvalidHarnessYAML)
	if err != nil {
		t.Fatalf("decode invalid harness fixtures: %v", err)
	}
	var invalid discoveryRow
	seen := false
	for _, row := range rows {
		if row.Name != "sensitive-invalid-harness" {
			t.Fatalf("unexpected invalid harness fixture %q", row.Name)
		}
		if seen {
			t.Fatalf("duplicate invalid harness fixture %q", row.Name)
		}
		seen, invalid = true, row
	}
	if !seen {
		t.Fatalf("required invalid harness fixture missing")
	}
	discoveryInsertRow(t, ctx, pool, owner, invalid)
	var logs bytes.Buffer
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prior)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts", nil)
	(&Handler{pool: pool, queries: sqlc.New(pool)}).ListTranscripts(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(logs.String(), invalid.Harness) {
		t.Fatalf("operator log disclosed invalid stored harness sentinel: %s", logs.String())
	}
}
