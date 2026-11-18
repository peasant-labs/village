//go:build integration

package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/transcript_numbering_served.yaml
var numberingServedYAML []byte

type numberingServedCase struct {
	Name      string   `yaml:"name"`
	Partition string   `yaml:"partition"`
	Numbers   []string `yaml:"numbers"`
}

func TestNumberingServedEncrypted(t *testing.T) {
	cases, err := decodeFixtureRows[numberingServedCase](numberingServedYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"number-history-section-restarts":     true,
		"number-main-and-history-independent": true,
		"number-entry-pass-computes-once":     true,
		"number-no-body-read-for-numbering":   true,
	}
	fixture, err := decodeFixtureDocument[struct {
		Content        string                       `yaml:"content"`
		HistoryContent string                       `yaml:"history_content"`
		Cases          []contentSourceEncryptedCase `yaml:"cases"`
	}](contentSourceEncryptedYAML)
	if err != nil {
		t.Fatal(err)
	}
	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	world := newOrderingWorld(t, pool, publishEvidenceObjects(t), 49002, []byte(fixture.Content))
	history := strings.ReplaceAll(fixture.HistoryContent, "__SESSION_ID__", world.localID)
	if response := world.request([]byte(history), "initial")(world.ctx); response.Code != http.StatusOK {
		t.Fatalf("republish status %d: %s", response.Code, response.Body.String())
	}
	world.handler.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
	counting := &decryptionCountingStore{TranscriptBlobStore: world.handler.blobs}
	world.handler.blobs = counting
	opened, err := world.handler.openCachedTranscript(world.ctx, world.row(), func(_ sqlc.Transcript) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	cooks := 0
	opened.Index.cook = func(turns []schema.TranscriptCookTurn, harness schema.Harness) ([]schema.CookedTurn, error) {
		cooks++
		return schema.CookTranscriptRows(turns, harness)
	}
	mux := chi.NewRouter()
	mux.Get("/transcripts/{id}/segments/{index}", world.handler.GetTranscriptSegment)
	base := "/transcripts/" + uuidFromPg(world.id).String() + "/segments/0?v=2"
	read := func(query string) []string {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, base+query, nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, world.owner))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("segment status %d: %s", w.Code, w.Body.String())
		}
		var segment schema.PositionedSegment
		if err := json.Unmarshal(w.Body.Bytes(), &segment); err != nil {
			t.Fatal(err)
		}
		numbers := make([]string, 0, len(segment.Rows))
		for i, row := range segment.Rows {
			if row.Position != i || row.DisplayNumber == "" {
				t.Fatalf("invalid served row %d: %+v", i, row)
			}
			numbers = append(numbers, row.DisplayNumber)
		}
		return numbers
	}
	for _, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or duplicate case %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Partition == "both" {
				if got := read(""); !reflect.DeepEqual(got, []string{"1", "1a"}) {
					t.Fatalf("main numbers %v", got)
				}
			}
			query := ""
			if tc.Partition == "earlier" || tc.Partition == "both" {
				query = "&partition=history&section=0"
			}
			got := read(query)
			if !reflect.DeepEqual(got, tc.Numbers) {
				t.Fatalf("numbers %v, want %v", got, tc.Numbers)
			}
			if tc.Partition == "repeat" {
				beforeCooks, beforeReads := cooks, counting.count()
				if again := read(query); !reflect.DeepEqual(again, tc.Numbers) {
					t.Fatalf("repeat numbers %v, want %v", again, tc.Numbers)
				}
				if cooks != beforeCooks || counting.count() != beforeReads {
					t.Fatalf("warm read cooked %d times and read store %d times", cooks-beforeCooks, counting.count()-beforeReads)
				}
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing cases %v", required)
	}
	if cooks != 2 || counting.count() != 1 {
		t.Fatalf("got %d cooks and %d object reads, want two partitions and one read", cooks, counting.count())
	}
}
