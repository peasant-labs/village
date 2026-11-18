//go:build integration

package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/transcript_numbering_refusal.yaml
var numberingRefusalYAML []byte

type numberingRefusalCase struct {
	Name     string `yaml:"name"`
	Scenario string `yaml:"scenario"`
}

func TestNumberingRefusal(t *testing.T) {
	cases, err := decodeFixtureRows[numberingRefusalCase](numberingRefusalYAML)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := decodeFixtureDocument[struct {
		Content        string                       `yaml:"content"`
		HistoryContent string                       `yaml:"history_content"`
		Cases          []contentSourceEncryptedCase `yaml:"cases"`
	}](contentSourceEncryptedYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"number-absent-never-fabricated": true, "number-refusal-uncookable-no-partial-body": true}
	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	objects := publishEvidenceObjects(t)
	for ordinal, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or duplicate case %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			w := newOrderingWorld(t, pool, objects, 51000+ordinal, []byte(fixture.Content))
			content := strings.ReplaceAll(fixture.HistoryContent, "__SESSION_ID__", w.localID)
			content = strings.Replace(content, `"turnCount":2`, `"turnCount":3`, 1)
			content = strings.Replace(content, `],"earlierHistory":`, `,{"index":2,"role":"system","content":"short","timestamp":"2026-01-01T00:00:03Z","depth":0}],"earlierHistory":`, 1)
			if response := w.request([]byte(content), "history")(w.ctx); response.Code != http.StatusOK {
				t.Fatalf("republish: %d %s", response.Code, response.Body.String())
			}
			w.handler.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
			if tc.Scenario == "cook-error" {
				opened, err := w.handler.openCachedTranscript(w.ctx, w.row(), func(sqlc.Transcript) bool { return true })
				if err != nil {
					t.Fatal(err)
				}
				opened.Index.cook = func([]schema.TranscriptCookTurn, schema.Harness) ([]schema.CookedTurn, error) {
					return nil, errors.New("synthetic cooking refusal")
				}
			} else if tc.Scenario != "dropped" {
				t.Fatalf("unknown scenario %q", tc.Scenario)
			}
			mux := chi.NewRouter()
			mux.Get("/transcripts/{id}/segments/{index}", w.handler.GetTranscriptSegment)
			path := "/transcripts/" + uuidFromPg(w.id).String() + "/segments/0?v=2"
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r = r.WithContext(context.WithValue(r.Context(), UserContextKey, w.owner))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, r)
			if tc.Scenario == "cook-error" {
				if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), `"rows"`) || strings.Contains(response.Body.String(), "main prompt") {
					t.Fatalf("refusal status %d body %s", response.Code, response.Body.String())
				}
				return
			}
			if response.Code != http.StatusOK {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			var segment schema.PositionedSegment
			if err := json.Unmarshal(response.Body.Bytes(), &segment); err != nil {
				t.Fatal(err)
			}
			if len(segment.Rows) != 2 || strings.Contains(response.Body.String(), `"short"`) {
				t.Fatalf("dropped row was served: %s", response.Body.String())
			}
			for i, row := range segment.Rows {
				if row.Position != i || row.DisplayNumber == "" {
					t.Fatalf("row %d: %+v", i, row)
				}
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing cases %v", required)
	}
}
