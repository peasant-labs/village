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
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/peasant-labs/schema"
)

//go:embed testdata/transcript_numbering_lifecycle.yaml
var numberingLifecycleYAML []byte

type numberingLifecycleCase struct {
	Name     string   `yaml:"name"`
	Scenario string   `yaml:"scenario"`
	Before   []string `yaml:"before"`
	After    []string `yaml:"after"`
}

func TestNumberingLifecycle(t *testing.T) {
	cases, err := decodeFixtureRows[numberingLifecycleCase](numberingLifecycleYAML)
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
	required := map[string]bool{"number-stable-across-evict-refetch": true, "number-stable-across-revision-change": true}
	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	objects := publishEvidenceObjects(t)
	for ordinal, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or duplicate case %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			w := newOrderingWorld(t, pool, objects, 50000+ordinal, []byte(fixture.Content))
			initial := strings.ReplaceAll(fixture.HistoryContent, "__SESSION_ID__", w.localID)
			if response := w.request([]byte(initial), "history")(w.ctx); response.Code != http.StatusOK {
				t.Fatalf("republish: %d %s", response.Code, response.Body.String())
			}
			now := time.Now()
			cache := newTranscriptContentCache(transcriptContentCacheConfig{Now: func() time.Time { return now }, IdleLifetime: time.Second})
			w.handler.contentCache = cache
			counter := &decryptionCountingStore{TranscriptBlobStore: w.handler.blobs}
			w.handler.blobs = counter
			mux := chi.NewRouter()
			mux.Get("/transcripts/{id}/segments/{index}", w.handler.GetTranscriptSegment)
			base := "/transcripts/" + uuidFromPg(w.id).String() + "/segments/0?v=2"
			read := func(suffix string, status int) []string {
				t.Helper()
				r := httptest.NewRequest(http.MethodGet, base+suffix, nil)
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, w.owner))
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, r)
				if response.Code != status {
					t.Fatalf("read %s status %d: %s", suffix, response.Code, response.Body.String())
				}
				if status != http.StatusOK {
					return nil
				}
				var segment schema.PositionedSegment
				if err := json.Unmarshal(response.Body.Bytes(), &segment); err != nil {
					t.Fatal(err)
				}
				result := make([]string, 0, len(segment.Rows))
				for i, row := range segment.Rows {
					if row.Position != i {
						t.Fatalf("position %d at row %d", row.Position, i)
					}
					result = append(result, row.DisplayNumber)
				}
				return result
			}
			revision := contentGeneration(w.row().ContentRevision).String()
			if got := read("", http.StatusOK); !reflect.DeepEqual(got, tc.Before) {
				t.Fatalf("before %v, want %v", got, tc.Before)
			}
			switch tc.Scenario {
			case "evict":
				now = now.Add(2 * time.Second)
				if cache.Sweep() != 1 {
					t.Fatal("idle sweep did not evict the warmed entry")
				}
			case "replace":
				changed := strings.Replace(initial, `"turnCount":2`, `"turnCount":3`, 1)
				changed = strings.Replace(changed, `],"earlierHistory":`, `,{"index":2,"role":"user","content":"new prompt","timestamp":"2026-01-01T00:00:03Z","depth":0}],"earlierHistory":`, 1)
				if response := w.request([]byte(changed), "replacement")(w.ctx); response.Code != http.StatusOK {
					t.Fatalf("replacement: %d %s", response.Code, response.Body.String())
				}
				if revision == contentGeneration(w.row().ContentRevision).String() {
					t.Fatal("replacement did not change revision")
				}
				read("&revision="+revision, http.StatusConflict)
			default:
				t.Fatalf("unknown scenario %q", tc.Scenario)
			}
			if got := read("", http.StatusOK); !reflect.DeepEqual(got, tc.After) {
				t.Fatalf("after %v, want %v", got, tc.After)
			}
			if counter.count() != 2 {
				t.Fatalf("object reads %d, want one per loaded generation", counter.count())
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing cases %v", required)
	}
}
