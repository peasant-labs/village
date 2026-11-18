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
)

//go:embed testdata/transcript_locator.yaml
var locatorCasesYAML []byte

type locatorCase struct {
	Name     string `yaml:"name"`
	Scenario string `yaml:"scenario"`
}

func TestTranscriptLocatorEncrypted(t *testing.T) {
	cases, err := decodeFixtureRows[locatorCase](locatorCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"locator-no-match-404": true, "locator-authorizes-before-generation": true, "locator-ref-not-echoed": true, "locator-body-contract-violation-400": true, "locator-stale-revision-409": true, "locator-stale-representation-409": true, "locator-carried-hash-mismatch-general-link": true, "locator-scan-bounded-to-partition": true, "access-locator-denied-equals-no-match": true}
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
	world := newOrderingWorld(t, pool, publishEvidenceObjects(t), 59001, []byte(fixture.Content))
	content := strings.ReplaceAll(fixture.HistoryContent, "__SESSION_ID__", world.localID)
	content = strings.Replace(content, `"content":"main prompt"`, `"sourceEntryRef":"main-ref","content":"main prompt"`, 1)
	content = strings.Replace(content, `"content":"older prompt"`, `"sourceEntryRef":"history-ref","content":"older prompt"`, 1)
	if response := world.request([]byte(content), "history-only")(world.ctx); response.Code != http.StatusOK {
		t.Fatalf("republish: %d %s", response.Code, response.Body.String())
	}
	counting := &decryptionCountingStore{TranscriptBlobStore: world.handler.blobs}
	world.handler.blobs = counting
	world.handler.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
	mux := chi.NewRouter()
	mux.Post("/transcripts/{id}/segments/locate", world.handler.LocateTranscriptEntry)
	mux.Get("/transcripts/{id}/segments", world.handler.GetTranscriptSegments)
	base := "/transcripts/" + uuidFromPg(world.id).String()
	read := func(path, body string, owner bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		if owner {
			r = r.WithContext(context.WithValue(r.Context(), UserContextKey, world.owner))
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	// The real directory supplies exactly the context a client carries.
	r := httptest.NewRequest(http.MethodGet, base+"/segments?v=2", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, world.owner))
	directory := httptest.NewRecorder()
	mux.ServeHTTP(directory, r)
	if directory.Code != 200 {
		t.Fatalf("directory: %d %s", directory.Code, directory.Body.String())
	}
	var d schema.PositionedDirectory
	if err = json.Unmarshal(directory.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	request := schema.TranscriptEntryLocateRequest{Reference: "main-ref", Revision: d.Context.Revision, Representation: d.Context.Representation, ContentHash: d.Context.ContentHash}
	body := func(req schema.TranscriptEntryLocateRequest) string {
		b, e := json.Marshal(req)
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	// Denial is tested against a private published row, before any object read.
	if response := transcriptVisibilityPatch(t, world.handler, world.owner, world.id, dbVisibilityPrivate); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	missingID := "/transcripts/00000000-0000-4000-8000-000000000001/segments/locate"
	missing := read(missingID, body(request), false)
	for _, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or duplicate case %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			q := request
			target := base + "/segments/locate"
			owner := true
			expected := http.StatusNotFound
			switch tc.Scenario {
			case "no-match":
				q.Reference = "absent-ref"
			case "denied", "denied-equals":
				owner = false
			case "match":
				expected = 200
			case "bad-body":
				expected = 400
			case "revision":
				q.Revision = "999"
				expected = 409
			case "representation":
				q.Representation = "stale"
				expected = 409
			case "wrong-hash":
				q.ContentHash = "stale"
			case "history-only":
				q.Reference = "history-ref"
			default:
				t.Fatalf("unknown scenario %q", tc.Scenario)
			}
			payload := body(q)
			if tc.Scenario == "bad-body" {
				payload = `{}`
			}
			if tc.Scenario == "denied" {
				world.handler.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
			}
			before := counting.count()
			got := read(target, payload, owner)
			if got.Code != expected {
				t.Fatalf("status %d want %d: %s", got.Code, expected, got.Body.String())
			}
			if tc.Scenario == "denied" || tc.Scenario == "denied-equals" {
				if counting.count() != before {
					t.Fatal("denial read object")
				}
				if got.Body.String() != missing.Body.String() || !reflect.DeepEqual(got.Header(), missing.Header()) {
					t.Fatalf("denial differs from missing: %v %q vs %v %q", got.Header(), got.Body.String(), missing.Header(), missing.Body.String())
				}
			}
			if tc.Scenario == "no-match" || tc.Scenario == "wrong-hash" || tc.Scenario == "history-only" {
				if got.Body.String() != missing.Body.String() || !reflect.DeepEqual(got.Header(), missing.Header()) {
					t.Fatal("no-match differs from missing")
				}
			}
			if strings.Contains(got.Body.String(), q.Reference) {
				t.Fatalf("reference appeared in response: %s", got.Body.String())
			}
			if expected == 200 {
				var match schema.TranscriptEntryMatch
				if err := json.Unmarshal(got.Body.Bytes(), &match); err != nil {
					t.Fatal(err)
				}
				if match.Position != 0 || match.SegmentOrdinal != 0 || match.Kind != schema.TranscriptEntryReferenceSource {
					t.Fatalf("match %+v", match)
				}
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing cases: %v", required)
	}
}
