package handler

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/segmented_read_negotiation.yaml
var segmentedReadNegotiationYAML []byte

type segmentedReadNegotiationCase struct {
	Name      string `yaml:"name"`
	Query     string `yaml:"query"`
	Status    int    `yaml:"status"`
	Supported bool   `yaml:"supported"`
}

func TestSegmentedReadNegotiation(t *testing.T) {
	cases, err := decodeFixtureRows[segmentedReadNegotiationCase](segmentedReadNegotiationYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"omitted": true, "refused": true, "malformed": true, "empty": true, "duplicate-same": true, "duplicate-mixed": true}
	for _, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or duplicate case %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			w := httptest.NewRecorder()
			_, ok := readGeneration(w, httptest.NewRequest(http.MethodGet, "/"+tc.Query, nil))
			if ok != tc.Supported || w.Code != tc.Status {
				t.Fatalf("supported %t status %d, want %t %d", ok, w.Code, tc.Supported, tc.Status)
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing cases: %v", required)
	}
	for _, generation := range schema.SegmentedReadGenerations() {
		t.Run(fmt.Sprintf("contract-generation-%d", generation), func(t *testing.T) {
			w := httptest.NewRecorder()
			actual, ok := readGeneration(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/?v=%d", generation), nil))
			wantVersion, declared := schema.SegmentedReadProtocolVersion(generation)
			actualVersion, valid := schema.SegmentedReadProtocolVersion(actual)
			if !declared || !ok || !valid || actualVersion != wantVersion {
				t.Fatalf("selector %d resolves to %d (%q), want %q", generation, actual, actualVersion, wantVersion)
			}
		})
	}
}

func TestSegmentedReadVersionAdvertisement(t *testing.T) {
	w := httptest.NewRecorder()
	newTestHandler(&mockQuerier{}, nil).GetSchemaVersion(w, httptest.NewRequest(http.MethodGet, "/", nil))
	var response schema.SchemaVersionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || response.SegmentedReadVersion != schema.SegmentedReadAPIVersion || response.MinSegmentedReadVersion != schema.SegmentedReadMinVersion {
		t.Fatalf("advertisement status %d versions %q and %q", w.Code, response.SegmentedReadVersion, response.MinSegmentedReadVersion)
	}
}

//go:embed testdata/segmented_read_pages.yaml
var segmentedReadPagesYAML []byte

//go:embed testdata/segmented_read_marks.yaml
var segmentedReadMarksYAML []byte

//go:embed testdata/segmented_read_hashes.yaml
var segmentedReadHashesYAML []byte

type segmentedReadHashCase struct {
	Name       string                          `yaml:"name"`
	Validation schema.TranscriptReadValidation `yaml:"validation"`
	Withheld   bool                            `yaml:"withheld"`
	Hash       string                          `yaml:"hash"`
	Want       string                          `yaml:"want"`
}

func TestSegmentedVisibleContentHash(t *testing.T) {
	cases, err := decodeFixtureRows[segmentedReadHashCase](segmentedReadHashesYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"strict-visible": true, "legacy-visible": true, "migrated-hidden": true, "strict-withheld": true, "pending-hash": true}
	for _, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or repeated fixture %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			s := segmentedRead{withheld: tc.Withheld, opened: &openedTranscript{Index: &transcriptIndex{Validation: tc.Validation, ContentHash: storage.ContentHash(tc.Hash)}}}
			if got := s.visibleContentHash(); got != tc.Want {
				t.Fatalf("got %q want %q", got, tc.Want)
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing fixtures: %v", required)
	}
}

type segmentedReadMarkCase struct {
	Name     string `yaml:"name"`
	Document string `yaml:"document"`
	Stored   []int  `yaml:"stored"`
	Marks    []struct {
		Position int `yaml:"position"`
		Turn     int `yaml:"turn"`
	} `yaml:"marks"`
}

func TestSegmentedMarkRemap(t *testing.T) {
	cases, err := decodeFixtureRows[segmentedReadMarkCase](segmentedReadMarksYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"remap-and-drop-removed": true, "sort-and-deduplicate": true}
	for _, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or duplicate case %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			x, err := newTranscriptIndex(transcriptIndexInput{Bytes: []byte(tc.Document)})
			if err != nil {
				t.Fatal(err)
			}
			p, err := x.Partition(mainPartitionKey(), noTranscriptBuildReserve)
			if err != nil {
				t.Fatal(err)
			}
			want := make([]schema.TranscriptFlaggedTurn, 0, len(tc.Marks))
			for _, m := range tc.Marks {
				turn := m.Turn
				want = append(want, schema.TranscriptFlaggedTurn{Position: m.Position, Turn: &turn})
			}
			if got := remapFlaggedTurns(p, tc.Stored); !reflect.DeepEqual(got, want) {
				t.Fatalf("marks %v, want %v", got, want)
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing fixtures: %v", required)
	}
}

type segmentedReadPageCase struct {
	Name   string `yaml:"name"`
	Total  int    `yaml:"total"`
	Query  string `yaml:"query"`
	Status int    `yaml:"status"`
	Start  int    `yaml:"start"`
	End    int    `yaml:"end"`
	Paged  bool   `yaml:"paged"`
}

func TestSegmentedDirectoryPage(t *testing.T) {
	cases, err := decodeFixtureRows[segmentedReadPageCase](segmentedReadPagesYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"under-cap-whole": true, "at-cap-whole": true, "above-cap-first": true, "above-cap-last": true, "beyond-last": true, "under-cap-second": true, "zero-page": true, "malformed-page": true}
	for _, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or duplicate case %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			w := httptest.NewRecorder()
			start, end, page, size, ok := directoryPage(w, httptest.NewRequest("GET", "/"+tc.Query, nil), tc.Total)
			if w.Code != tc.Status {
				t.Fatalf("status=%d, want %d", w.Code, tc.Status)
			}
			if tc.Status != 200 {
				if ok {
					t.Fatal("refused page was served")
				}
				return
			}
			if !ok || start != tc.Start || end != tc.End || (page != nil) != tc.Paged || (size != nil) != tc.Paged {
				t.Fatalf("got range %d..%d page %v size %v", start, end, page, size)
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing fixtures: %v", required)
	}
}
