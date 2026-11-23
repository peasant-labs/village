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

//go:embed testdata/transcript_numbering_parity.yaml
var numberingParityYAML []byte

type numberingParityCase struct {
	Name           string   `yaml:"name"`
	Harness        string   `yaml:"harness"`
	Turns          string   `yaml:"turns"`
	MainNumbers    []string `yaml:"main_numbers"`
	HistoryNumbers []string `yaml:"history_numbers"`
}

func TestNumberingPartitionParity(t *testing.T) {
	cases, err := decodeFixtureRows[numberingParityCase](numberingParityYAML)
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
	required := map[string]bool{"number-pi-partition-parity": true}
	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	objects := publishEvidenceObjects(t)
	for ordinal, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or duplicate case %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			w := newOrderingWorld(t, pool, objects, 52000+ordinal, []byte(fixture.Content))
			content := strings.ReplaceAll(fixture.HistoryContent, "__SESSION_ID__", w.localID)
			var envelope map[string]json.RawMessage
			var detail map[string]json.RawMessage
			if err := json.Unmarshal([]byte(content), &envelope); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(envelope["sessionDetail"], &detail); err != nil {
				t.Fatal(err)
			}
			detail["turns"] = json.RawMessage(tc.Turns)
			detail["turnCount"] = json.RawMessage("4")
			detail["harness"], err = json.Marshal(tc.Harness)
			if err != nil {
				t.Fatal(err)
			}
			envelope["sessionDetail"], err = json.Marshal(detail)
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			request := publishEvidenceRequest(w.localID, body, "")
			request.Model.Harness = schema.Harness(tc.Harness)
			response := publishEvidencePost(t, w.handler, w.owner, request, body)
			if response.Code != http.StatusOK {
				t.Fatalf("republish %d: %s", response.Code, response.Body.String())
			}
			w.handler.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
			mux := chi.NewRouter()
			mux.Get("/transcripts/{id}/segments/{index}", w.handler.GetTranscriptSegment)
			base := "/transcripts/" + uuidFromPg(w.id).String() + "/segments/0?v=2"
			read := func(suffix string, want []string) {
				t.Helper()
				r := httptest.NewRequest(http.MethodGet, base+suffix, nil)
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, w.owner))
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, r)
				if response.Code != http.StatusOK {
					t.Fatalf("segment %d: %s", response.Code, response.Body.String())
				}
				var segment schema.PositionedSegment
				if err := json.Unmarshal(response.Body.Bytes(), &segment); err != nil {
					t.Fatal(err)
				}
				var numbers []string
				for i, row := range segment.Rows {
					if row.Position != i {
						t.Fatalf("position %d at row %d", row.Position, i)
					}
					numbers = append(numbers, row.DisplayNumber)
				}
				if !reflect.DeepEqual(numbers, want) {
					t.Fatalf("numbers %v, want %v", numbers, want)
				}
			}
			read("", tc.MainNumbers)
			read("&partition=history&section=0", tc.HistoryNumbers)
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing cases %v", required)
	}
}
