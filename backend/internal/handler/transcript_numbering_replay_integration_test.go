//go:build integration

package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/peasant-labs/schema"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/transcript_numbering_replay.yaml
var numberingReplayYAML []byte

const schemaNumberingFixturePath = "testdata/transcript_numbering.yaml"

type numberingCorpusOutcome string

const (
	numberingCooked  numberingCorpusOutcome = "cooked"
	numberingRefused numberingCorpusOutcome = "refused"
)

type numberingReplayManifest struct {
	Name              string   `yaml:"name"`
	ControlTurns      string   `yaml:"control_turns"`
	ControlNumbers    []string `yaml:"control_numbers"`
	Replayed          []string `yaml:"replayed"`
	RefusedAtBoundary []string `yaml:"refused_at_boundary"`
}

type schemaNumberingCase struct {
	Name  string `yaml:"name"`
	Input struct {
		Harness string `yaml:"harness"`
		Turns   string `yaml:"turnsJSON"`
	} `yaml:"input"`
	Expect struct {
		Outcome numberingCorpusOutcome `yaml:"outcome"`
	} `yaml:"expect"`
	Expected []struct {
		Position       int    `yaml:"position"`
		Keep           bool   `yaml:"keep"`
		ServedPosition int    `yaml:"servedPosition"`
		DisplayNumber  string `yaml:"displayNumber"`
	} `yaml:"expected"`
}

func TestNumberingSchemaReplay(t *testing.T) {
	manifest, err := decodeFixtureDocument[numberingReplayManifest](numberingReplayYAML)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "number-fixture-replay-village" {
		t.Fatalf("missing required replay case: %q", manifest.Name)
	}
	classes := map[string]bool{}
	for _, group := range []struct {
		refused bool
		rows    []string
	}{{false, manifest.Replayed}, {true, manifest.RefusedAtBoundary}} {
		for _, name := range group.rows {
			if _, found := classes[name]; name == "" || found {
				t.Fatalf("empty or duplicate corpus case %q", name)
			}
			classes[name] = group.refused
		}
	}
	moduleDir, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/peasant-labs/schema").Output()
	if err != nil {
		t.Fatalf("locate pinned Schema module: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(moduleDir)), schemaNumberingFixturePath))
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Versions map[string]struct {
			Cases []schemaNumberingCase `yaml:"cases"`
		} `yaml:"versions"`
	}
	if err := yaml.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Versions) == 0 {
		t.Fatal("Schema numbering corpus has no versions")
	}
	seen := map[string]bool{}
	for _, group := range corpus.Versions {
		for _, tc := range group.Cases {
			_, classified := classes[tc.Name]
			if tc.Name == "" || seen[tc.Name] || !classified {
				t.Fatalf("Schema case %q is duplicated or missing from replay manifest", tc.Name)
			}
			seen[tc.Name] = true
		}
	}
	for name := range classes {
		if !seen[name] {
			t.Fatalf("manifest case %q is absent from Schema corpus", name)
		}
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
	w := newOrderingWorld(t, pool, publishEvidenceObjects(t), 55000, []byte(fixture.Content))
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
		return out
	}
	t.Run("served-number-control", func(t *testing.T) {
		// The upstream corpus currently has no boundary-accepted numbered
		// turns. This separate, explicit control is not a modified corpus case.
		body := fmt.Sprintf(`{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":%q,"harness":"claude-code","startTime":"2026-01-01T00:00:00Z","endTime":"2026-01-01T00:05:00Z","durationMins":5,"totalTokens":0,"tokensIn":0,"tokensOut":0,"turnCount":2,"toolCallCount":0,"turns":%s}}`, w.localID, manifest.ControlTurns)
		installHistoricalNumberingContent(t, w, []byte(body), "claude-code")
		response := read("/segments/0?v=2")
		if response.Code != http.StatusOK {
			t.Fatalf("number control: %d %s", response.Code, response.Body.String())
		}
		var segment schema.PositionedSegment
		if err := json.Unmarshal(response.Body.Bytes(), &segment); err != nil {
			t.Fatal(err)
		}
		if len(segment.Rows) != len(manifest.ControlNumbers) || len(segment.Rows) == 0 {
			t.Fatalf("number control rows %d, want %d nonempty rows", len(segment.Rows), len(manifest.ControlNumbers))
		}
		for i, row := range segment.Rows {
			if row.Position != i || row.DisplayNumber != manifest.ControlNumbers[i] {
				t.Fatalf("number control row %d: %+v, want number %q", i, row, manifest.ControlNumbers[i])
			}
		}
	})
	for version, group := range corpus.Versions {
		for _, tc := range group.Cases {
			t.Run(version+"/"+tc.Name, func(t *testing.T) {
				// Only envelope-level fields are supplied here. The case's
				// turn array is inserted verbatim, including member spellings.
				body := fmt.Sprintf(`{"contractVersion":"0.1.0","kind":"session_detail","sessionDetail":{"id":%q,"harness":%q,"startTime":"2026-01-01T00:00:00Z","endTime":"2026-01-01T00:05:00Z","durationMins":5,"totalTokens":0,"tokensIn":0,"tokensOut":0,"turnCount":0,"toolCallCount":0,"turns":%s}}`, w.localID, tc.Input.Harness, tc.Input.Turns)
				index, err := newTranscriptIndex(transcriptIndexInput{Bytes: []byte(body)})
				if err != nil {
					t.Fatalf("construct held content index: %v", err)
				}
				part, err := index.Partition(mainPartitionKey(), noTranscriptBuildReserve)
				if tc.Expect.Outcome == numberingRefused {
					if err == nil || part != nil || len(index.Partitions) != 0 {
						t.Fatalf("expected cook refusal without partial partition: partition=%+v error=%v", part, err)
					}
				} else {
					if tc.Expect.Outcome != numberingCooked || err != nil {
						t.Fatalf("expected cooked partition, got outcome %q error %v", tc.Expect.Outcome, err)
					}
					served := 0
					for _, want := range tc.Expected {
						if !want.Keep {
							continue
						}
						if served >= len(part.Rows) || part.Rows[served].Stored != want.Position ||
							part.Rows[served].ServedPosition != want.ServedPosition || part.Rows[served].DisplayNumber != want.DisplayNumber {
							t.Fatalf("index stored %d row %d: got %+v, want position %d number %q", want.Position, served, part.Rows, want.ServedPosition, want.DisplayNumber)
						}
						served++
					}
					if served != len(part.Rows) {
						t.Fatalf("index served %d rows, want %d", len(part.Rows), served)
					}
				}
				installHistoricalNumberingContent(t, w, []byte(body), tc.Input.Harness)
				directory := read("/segments?v=2")
				if classes[tc.Name] {
					if _, err := validateContentBoundary([]byte(body), tc.Input.Harness, contentStoredRead); err == nil {
						t.Fatal("manifest says boundary refusal but stored-read boundary accepted the case")
					}
					whole := read("/content")
					if directory.Code != http.StatusInternalServerError || whole.Code != http.StatusInternalServerError ||
						strings.Contains(directory.Body.String(), `"segments"`) || strings.Contains(whole.Body.String(), `"sessionDetail"`) {
						t.Fatalf("refusal differs or contains partial body: segments=%d %s content=%d %s", directory.Code, directory.Body.String(), whole.Code, whole.Body.String())
					}
					return
				}
				if directory.Code != http.StatusOK {
					t.Fatalf("directory: %d %s", directory.Code, directory.Body.String())
				}
				var refs schema.PositionedDirectory
				if err := json.Unmarshal(directory.Body.Bytes(), &refs); err != nil {
					t.Fatal(err)
				}
				var rows []schema.TranscriptSegmentRow
				for _, ref := range refs.Segments {
					response := read(fmt.Sprintf("/segments/%d?v=2", ref.Index))
					if response.Code != http.StatusOK {
						t.Fatalf("segment %d: %d %s", ref.Index, response.Code, response.Body.String())
					}
					var segment schema.PositionedSegment
					if err := json.Unmarshal(response.Body.Bytes(), &segment); err != nil {
						t.Fatal(err)
					}
					rows = append(rows, segment.Rows...)
				}
				served := 0
				for _, want := range tc.Expected {
					if !want.Keep {
						continue
					}
					if served >= len(rows) || rows[served].Position != want.ServedPosition || rows[served].DisplayNumber != want.DisplayNumber {
						t.Fatalf("stored %d row %d: got %+v, want position %d number %q", want.Position, served, rows, want.ServedPosition, want.DisplayNumber)
					}
					served++
				}
				if served != len(rows) {
					t.Fatalf("served %d rows, want %d", len(rows), served)
				}
			})
		}
	}
}
