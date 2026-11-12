//go:build integration

package handler

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/peasant-labs/schema"
)

//go:embed testdata/conversation-turn-count-reads.yaml
var conversationTurnCountReadsYAML []byte

// requiredConversationTurnCountReads is the name manifest for
// testdata/conversation-turn-count-reads.yaml. Exact membership, never a count.
var requiredConversationTurnCountReads = []string{
	"transcript-detail",
	"discovery-most-turns-flat",
	"discovery-grouped",
	"collective-flat",
	"collective-grouped",
	"my-shares-flat",
	"my-shares-grouped",
	"personal-totals",
}

type conversationTurnCountRow struct {
	TurnCount             int64  `yaml:"turn_count"`
	ConversationTurnCount *int64 `yaml:"conversation_turn_count"`
	DisplayedTurnCount    int64  `yaml:"displayed_turn_count"`
}

// turnCountReadShape names the response a read serves. Each member decodes the
// body into that route's contract type, so every field the test reads is a
// compile-checked struct field and a renamed wire field stops the decode from
// filling it.
type turnCountReadShape string

const (
	turnCountShapeTranscriptDetail  turnCountReadShape = "transcript-detail"
	turnCountShapeTranscriptList    turnCountReadShape = "transcript-list"
	turnCountShapeSessionList       turnCountReadShape = "session-list"
	turnCountShapeCollectiveDetail  turnCountReadShape = "collective-detail"
	turnCountShapeCollectiveGrouped turnCountReadShape = "collective-grouped"
	turnCountShapeMyShares          turnCountReadShape = "my-shares"
	turnCountShapeUserStats         turnCountReadShape = "user-stats"
)

// turnCountReadShapes maps every shape to its decoder. A fixture shape outside
// this set is refused before any request is made.
var turnCountReadShapes = map[turnCountReadShape]func(body []byte, into *servedTurnCounts) error{
	turnCountShapeTranscriptDetail: func(body []byte, into *servedTurnCounts) error {
		var response schema.VillageTranscriptMetadataResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return err
		}
		into.addTranscript(response.Transcript)
		return nil
	},
	turnCountShapeTranscriptList: func(body []byte, into *servedTurnCounts) error {
		var response schema.VillageTranscriptListResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return err
		}
		for _, row := range response.Transcripts {
			into.addTranscript(row.Transcript)
		}
		for _, row := range response.NextItems {
			into.addTranscript(row.Transcript)
		}
		return nil
	},
	turnCountShapeSessionList: func(body []byte, into *servedTurnCounts) error {
		var response schema.VillageSessionListPayload
		if err := json.Unmarshal(body, &response); err != nil {
			return err
		}
		into.addSessionList(response)
		return nil
	},
	turnCountShapeCollectiveDetail: func(body []byte, into *servedTurnCounts) error {
		var response schema.VillageCollectiveDetailResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return err
		}
		for _, row := range response.Transcripts {
			into.addCollective(row)
		}
		into.totals = append(into.totals, response.Stats.TotalTurns)
		return nil
	},
	turnCountShapeCollectiveGrouped: func(body []byte, into *servedTurnCounts) error {
		var response schema.VillageGroupedCollectiveDetailResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return err
		}
		into.addSessionList(response.TranscriptList)
		into.totals = append(into.totals, response.Stats.TotalTurns)
		return nil
	},
	turnCountShapeMyShares: func(body []byte, into *servedTurnCounts) error {
		var response []schema.VillageUserCollectiveShare
		if err := json.Unmarshal(body, &response); err != nil {
			return err
		}
		for _, row := range response {
			into.addMyShare(row)
		}
		return nil
	},
	turnCountShapeUserStats: func(body []byte, into *servedTurnCounts) error {
		var response schema.VillageUserStats
		if err := json.Unmarshal(body, &response); err != nil {
			return err
		}
		into.totals = append(into.totals, response.TotalTurns)
		return nil
	},
}

type conversationTurnCountReadsFixture struct {
	World    prWorld                             `yaml:"world"`
	Rows     map[string]conversationTurnCountRow `yaml:"rows"`
	Surfaces []struct {
		Name              string             `yaml:"name"`
		Viewer            string             `yaml:"viewer"`
		Path              string             `yaml:"path"`
		Shape             turnCountReadShape `yaml:"shape"`
		RowsPerTranscript int                `yaml:"rows_per_transcript"`
		Order             []string           `yaml:"order"`
		TotalTurns        *int64             `yaml:"total_turns"`
	} `yaml:"surfaces"`
}

// servedTurnCounts is what a response states about turns: every row of a world
// transcript, in the order the response lists them, and every turn total.
type servedTurnCounts struct {
	// names maps a world transcript's id to its fixture name.
	names  map[schema.TranscriptID]string
	rows   []servedTurnCountRow
	totals []int64
}

type servedTurnCountRow struct {
	transcript   string
	turnCount    *int32
	conversation *int64
	displayed    *int64
}

func (s *servedTurnCounts) add(id schema.TranscriptID, turnCount *int32, conversation, displayed *int64) {
	if name, known := s.names[id]; known {
		s.rows = append(s.rows, servedTurnCountRow{transcript: name, turnCount: turnCount, conversation: conversation, displayed: displayed})
	}
}

func (s *servedTurnCounts) addTranscript(row schema.VillageTranscript) {
	s.add(row.ID, row.TurnCount, row.ConversationTurnCount, row.DisplayedTurnCount)
}

func (s *servedTurnCounts) addCollective(row schema.VillageCollectiveTranscript) {
	s.add(row.ID, row.TurnCount, row.ConversationTurnCount, row.DisplayedTurnCount)
}

func (s *servedTurnCounts) addMyShare(row schema.VillageUserCollectiveShare) {
	s.add(row.ID, row.TurnCount, row.ConversationTurnCount, row.DisplayedTurnCount)
}

// addSessionList adds each grouped row's session and the route arm beside it
// that restates the counts.
func (s *servedTurnCounts) addSessionList(list schema.VillageSessionListPayload) {
	for _, item := range list.Items {
		if item.Transcript == nil {
			continue
		}
		s.addTranscript(item.Transcript.Session)
		if item.Transcript.Collective != nil {
			s.addCollective(*item.Transcript.Collective)
		}
		if item.Transcript.MyShare != nil {
			s.addMyShare(*item.Transcript.MyShare)
		}
	}
}

// conversationTurnCountKey is the wire name of the conversation count on a
// served row, read from the contract type.
var conversationTurnCountKey = wireKeyOf(schema.VillageTranscript{}, schema.VillageTranscript{ConversationTurnCount: new(int64)})

// countObjectsWithKey counts the JSON objects in a document that carry key,
// whatever its value. A typed decode reads an absent key and a null alike, so
// this is what tells "served as null" from "not served".
func countObjectsWithKey(document []byte, key string) (int, error) {
	var value any
	if err := json.Unmarshal(document, &value); err != nil {
		return 0, err
	}
	var count func(any) int
	count = func(value any) int {
		total := 0
		switch typed := value.(type) {
		case []any:
			for _, element := range typed {
				total += count(element)
			}
		case map[string]any:
			if _, carries := typed[key]; carries {
				total++
			}
			for _, element := range typed {
				total += count(element)
			}
		}
		return total
	}
	return count(value), nil
}

// TestConversationTurnCountReads_RealPostgres proves that every mounted read
// serves the published conversation count and the database-resolved displayed
// count, and that the "most turns" sort and both totals use the displayed one.
func TestConversationTurnCountReads_RealPostgres(t *testing.T) {
	fixture, err := decodeFixtureDocument[conversationTurnCountReadsFixture](conversationTurnCountReadsYAML)
	if err != nil {
		t.Fatalf("load testdata/conversation-turn-count-reads.yaml: %v", err)
	}
	present := map[string]struct{}{}
	for _, s := range fixture.Surfaces {
		if _, repeated := present[s.Name]; repeated {
			t.Fatalf("testdata/conversation-turn-count-reads.yaml repeats %q", s.Name)
		}
		present[s.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "conversation-turn-count-reads", present, requiredConversationTurnCountReads)
	if len(fixture.Rows) != len(fixture.World.Transcripts) {
		t.Fatalf("the fixture states %d rows for %d transcripts; every transcript needs its expected row", len(fixture.Rows), len(fixture.World.Transcripts))
	}
	for _, tr := range fixture.World.Transcripts {
		want, ok := fixture.Rows[tr.Name]
		if !ok {
			t.Fatalf("transcript %q has no expected row", tr.Name)
		}
		if tr.Turns == nil || int64(*tr.Turns) == want.DisplayedTurnCount && tr.ConversationTurns != nil {
			t.Fatalf("transcript %q must store a turn_count that differs from its conversation count, or the two numbers cannot be told apart", tr.Name)
		}
	}

	pool := govTestPool(t)
	t.Cleanup(pool.Close)
	world := buildPRWorld(t, pool, fixture.World, "conversation-turn-count-reads")
	names := map[schema.TranscriptID]string{}
	for name, id := range world.transcripts {
		names[schema.TranscriptID(uuid.UUID(id.Bytes).String())] = name
	}
	collective := uuid.UUID(world.collectives["team"].Bytes).String()

	for _, s := range fixture.Surfaces {
		t.Run(s.Name, func(t *testing.T) {
			decode, known := turnCountReadShapes[s.Shape]
			if !known {
				t.Fatalf("testdata/conversation-turn-count-reads.yaml surface %q names shape %q, which is not a turnCountReadShape; name the response the route serves", s.Name, s.Shape)
			}
			h := world.handler(t, pool)
			routes := chi.NewRouter()
			routes.Route("/api/v1", func(r chi.Router) {
				h.RegisterTranscriptBrowseRoutes(r)
				h.RegisterCollectiveBrowseRoutes(r)
				r.With(h.AuthOptional).Get("/transcripts/{id}", h.GetTranscript)
				r.With(h.AuthRequired).Get("/users/me/stats", h.GetMyStats)
			})
			base := strings.NewReplacer("{collective}", collective, "{owner}", world.logins["author"]).Replace(s.Path)
			targets := []string{base}
			if strings.Contains(base, "{transcript}") {
				targets = targets[:0]
				for id := range names {
					targets = append(targets, strings.ReplaceAll(base, "{transcript}", string(id)))
				}
			}
			served := servedTurnCounts{names: names}
			carryingConversationKey := 0
			for _, target := range targets {
				rec := world.get(t, h, routes, s.Viewer, target)
				if rec.Code != http.StatusOK {
					t.Fatalf("GET %s = %d (%s), want 200", target, rec.Code, rec.Body.String())
				}
				if err := decode(rec.Body.Bytes(), &served); err != nil {
					t.Fatalf("decode GET %s as %q: %v", target, s.Shape, err)
				}
				carrying, err := countObjectsWithKey(rec.Body.Bytes(), conversationTurnCountKey)
				if err != nil {
					t.Fatalf("decode GET %s: %v", target, err)
				}
				carryingConversationKey += carrying
			}
			// Every served row states the conversation count, as a number or as
			// null; none leaves the key out.
			if carryingConversationKey != len(served.rows) {
				t.Errorf("%d objects carry %s for %d served rows; every row must serve the key, null when unmeasured", carryingConversationKey, conversationTurnCountKey, len(served.rows))
			}

			seen := map[string]int{}
			var order []string
			for _, row := range served.rows {
				want := fixture.Rows[row.transcript]
				if seen[row.transcript] == 0 {
					order = append(order, row.transcript)
				}
				seen[row.transcript]++
				if row.turnCount == nil || int64(*row.turnCount) != want.TurnCount {
					t.Errorf("%s: turn_count = %s, want the stored %d unchanged", row.transcript, formatCount(row.turnCount), want.TurnCount)
				}
				if row.displayed == nil || *row.displayed != want.DisplayedTurnCount {
					t.Errorf("%s: displayed_turn_count = %s, want %d", row.transcript, formatCount(row.displayed), want.DisplayedTurnCount)
				}
				switch {
				case want.ConversationTurnCount == nil && row.conversation != nil:
					t.Errorf("%s: conversation_turn_count = %d, want null for an unmeasured transcript", row.transcript, *row.conversation)
				case want.ConversationTurnCount != nil && (row.conversation == nil || *row.conversation != *want.ConversationTurnCount):
					t.Errorf("%s: conversation_turn_count = %s, want %d", row.transcript, formatCount(row.conversation), *want.ConversationTurnCount)
				}
			}
			for name := range fixture.Rows {
				if seen[name] != s.RowsPerTranscript {
					t.Errorf("%s is served as %d row objects, want %d", name, seen[name], s.RowsPerTranscript)
				}
			}
			if s.Order != nil && (len(order) < len(s.Order) || !reflect.DeepEqual(order[:len(s.Order)], s.Order)) {
				t.Errorf("rows are served in the order %v, want it to start with %v", order, s.Order)
			}
			if s.TotalTurns != nil {
				if len(served.totals) != 1 || served.totals[0] != *s.TotalTurns {
					t.Errorf("total_turns = %v, want exactly one total of %d", served.totals, *s.TotalTurns)
				}
			} else if len(served.totals) != 0 {
				t.Errorf("the response states total_turns %v; state the expected total in the fixture", served.totals)
			}
		})
	}
}

// formatCount prints a served count, or "null" when the row serves none.
func formatCount[T int32 | int64](count *T) string {
	if count == nil {
		return "null"
	}
	return fmt.Sprint(*count)
}
