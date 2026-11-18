//go:build integration

package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/peasant-labs/schema"
)

//go:embed testdata/transcript_numbering_legacy.yaml
var numberingLegacyYAML []byte

type numberingLegacyCase struct {
	Name     string `yaml:"name"`
	Variants []struct {
		Name  string `yaml:"name"`
		Turns string `yaml:"turns"`
	} `yaml:"variants"`
}

func TestNumberingLegacyParity(t *testing.T) {
	cases, err := decodeFixtureRows[numberingLegacyCase](numberingLegacyYAML)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].Name != "number-legacy-shapes-parity" {
		t.Fatalf("required legacy parity case missing: %+v", cases)
	}
	required := map[string]bool{"duplicate-index": true, "nonmonotonic-index": true, "missing-index": true, "pre-typed-shape": true}
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
	w := newOrderingWorld(t, pool, publishEvidenceObjects(t), 54000, []byte(fixture.Content))
	mux := chi.NewRouter()
	mux.Get("/transcripts/{id}/content", w.handler.GetTranscriptContent)
	mux.Get("/transcripts/{id}/segments", w.handler.GetTranscriptSegments)
	mux.Get("/transcripts/{id}/segments/{index}", w.handler.GetTranscriptSegment)
	base := "/transcripts/" + uuidFromPg(w.id).String()
	read := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, base+path, nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, w.owner))
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		if out.Code != http.StatusOK {
			t.Fatalf("read %s: %d %s", path, out.Code, out.Body.String())
		}
		return out
	}
	for _, tc := range cases[0].Variants {
		if !required[tc.Name] {
			t.Fatalf("unexpected or duplicate variant %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			raw := fmt.Sprintf(`{"id":%q,"harness":"claude","turns":%s}`, w.localID, tc.Turns)
			installHistoricalNumberingContent(t, w, []byte(raw), "claude-code")
			whole := read("/content")
			var envelope struct {
				SessionDetail json.RawMessage `json:"sessionDetail"`
			}
			if err := json.Unmarshal(whole.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			captured, err := schema.NewTranscriptCookTurns(envelope.SessionDetail)
			if err != nil {
				t.Fatal(err)
			}
			verdict, err := schema.CookTranscriptRows(captured, schema.HarnessClaudeCode)
			if err != nil {
				t.Fatal(err)
			}
			var directory schema.PositionedDirectory
			if err := json.Unmarshal(read("/segments?v=2").Body.Bytes(), &directory); err != nil {
				t.Fatal(err)
			}
			var rows []schema.TranscriptSegmentRow
			for _, ref := range directory.Segments {
				var segment schema.PositionedSegment
				if err := json.Unmarshal(read(fmt.Sprintf("/segments/%d?v=2", ref.Index)).Body.Bytes(), &segment); err != nil {
					t.Fatal(err)
				}
				rows = append(rows, segment.Rows...)
			}
			n := 0
			for _, want := range verdict {
				if !want.Keep {
					continue
				}
				if n >= len(rows) || rows[n].Position != want.ServedPosition || rows[n].DisplayNumber != want.DisplayNumber {
					t.Fatalf("row %d mismatch: got %+v, want %+v", n, rows, want)
				}
				n++
			}
			if n != len(rows) || n == 0 {
				t.Fatalf("served %d rows; content expects %d nonempty rows", len(rows), n)
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing variants %v", required)
	}
}
