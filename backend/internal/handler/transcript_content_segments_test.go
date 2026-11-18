package handler

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/peasant-labs/schema"
)

//go:embed testdata/content_source_segments.yaml
var contentSourceSegmentsYAML []byte

type contentSourceSegmentsCase struct {
	Name    string `yaml:"name"`
	Main    int    `yaml:"main"`
	History int    `yaml:"history"`
	Bytes   int    `yaml:"bytes"`
}

func TestContentSourceSegmentCut(t *testing.T) {
	cases, err := decodeFixtureRows[contentSourceSegmentsCase](contentSourceSegmentsYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"empty-partition":            true,
		"main-one-segment":           true,
		"main-crosses-budget":        true,
		"history-cuts-independently": true,
	}
	for _, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected or duplicate case %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			makeTurns := func(n int) ([]schema.TurnDetail, []byte) {
				t.Helper()
				turns := make([]schema.TurnDetail, n)
				for i := range turns {
					turns[i] = schema.TurnDetail{Index: i * 3, Role: schema.RoleUser, Content: strings.Repeat("x", tc.Bytes)}
				}
				raw, err := json.Marshal(turns)
				if err != nil {
					t.Fatal(err)
				}
				return turns, raw
			}
			mainTurns, main := makeTurns(tc.Main)
			historyTurns, history := makeTurns(tc.History)
			document := []byte(fmt.Sprintf(`{"sessionDetail":{"harness":"pi","turns":%s,"earlierHistory":[{"turns":%s}]}}`, main, history))
			index, err := newTranscriptIndex(transcriptIndexInput{Bytes: document})
			if err != nil {
				t.Fatal(err)
			}
			check := func(key partitionKey, input []schema.TurnDetail) {
				t.Helper()
				part, err := index.Partition(key)
				if err != nil {
					t.Fatal(err)
				}
				want := schema.CutTranscriptSegments(input, schema.TranscriptSegmentBudgetBytes)
				if part.TotalSegments != len(want) || len(part.Positions) != len(want) {
					t.Fatalf("got %d refs and %d positions, want %d", part.TotalSegments, len(part.Positions), len(want))
				}
				first := 0
				for i, cut := range want {
					size, last := 0, first
					for size < cut.ByteLength {
						size += schema.TranscriptTurnByteLength(input[last])
						last++
					}
					got := part.Refs[i]
					if got.Index != cut.Index || got.ByteLength != cut.ByteLength || got.SourceFirstPosition != first || got.SourceLastPosition != last-1 || !reflect.DeepEqual(got.ConversationTurns, cut.ConversationTurns) {
						t.Fatalf("ref %d = %+v, cut %+v, positions %d..%d", i, got, cut, first, last-1)
					}
					if got.FirstTurn == nil || got.LastTurn == nil || *got.FirstTurn != cut.FirstTurn || *got.LastTurn != cut.LastTurn {
						t.Fatalf("recorded bounds differ: %+v / %+v", got, cut)
					}
					if part.Positions[i] != (positionRef{Ordinal: i, SourceFirstPosition: first, SourceLastPosition: last - 1}) {
						t.Fatalf("position map %d: %+v", i, part.Positions[i])
					}
					first = last
				}
			}
			check(mainPartitionKey(), mainTurns)
			check(historyPartitionKey(0), historyTurns)
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing required cases: %v", required)
	}
}
