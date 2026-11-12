//go:build integration

package handler

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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

type conversationTurnCountReadsFixture struct {
	World    prWorld                             `yaml:"world"`
	Rows     map[string]conversationTurnCountRow `yaml:"rows"`
	Surfaces []struct {
		Name              string   `yaml:"name"`
		Viewer            string   `yaml:"viewer"`
		Path              string   `yaml:"path"`
		RowsPerTranscript int      `yaml:"rows_per_transcript"`
		Order             []string `yaml:"order"`
		TotalTurns        *int64   `yaml:"total_turns"`
	} `yaml:"surfaces"`
}

// servedTurnCounts is what a response states about turns: every row object of
// a world transcript, in document order, and every total_turns value.
type servedTurnCounts struct {
	rows   []servedTurnCountRow
	totals []float64
}

type servedTurnCountRow struct {
	transcript string
	object     map[string]any
}

// collectServedTurnCounts walks a decoded response. A row object is any object
// whose id is a world transcript and that carries turn_count, so each of the
// row types is found wherever a route nests it.
func collectServedTurnCounts(value any, names map[string]string, into *servedTurnCounts) {
	switch typed := value.(type) {
	case []any:
		for _, element := range typed {
			collectServedTurnCounts(element, names, into)
		}
	case map[string]any:
		if id, ok := typed["id"].(string); ok {
			if name, known := names[id]; known {
				if _, isRow := typed["turn_count"]; isRow {
					into.rows = append(into.rows, servedTurnCountRow{transcript: name, object: typed})
				}
			}
		}
		if total, ok := typed["total_turns"].(float64); ok {
			into.totals = append(into.totals, total)
		}
		// encoding/json decodes object keys into a map; arrays keep their order,
		// which is the only order a list response states.
		for _, element := range typed {
			collectServedTurnCounts(element, names, into)
		}
	}
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
	names := map[string]string{}
	for name, id := range world.transcripts {
		names[uuid.UUID(id.Bytes).String()] = name
	}
	collective := uuid.UUID(world.collectives["team"].Bytes).String()

	for _, s := range fixture.Surfaces {
		t.Run(s.Name, func(t *testing.T) {
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
					targets = append(targets, strings.ReplaceAll(base, "{transcript}", id))
				}
			}
			var served servedTurnCounts
			for _, target := range targets {
				rec := world.get(t, h, routes, s.Viewer, target)
				if rec.Code != http.StatusOK {
					t.Fatalf("GET %s = %d (%s), want 200", target, rec.Code, rec.Body.String())
				}
				var decoded any
				if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
					t.Fatalf("decode GET %s: %v", target, err)
				}
				collectServedTurnCounts(decoded, names, &served)
			}

			seen := map[string]int{}
			var order []string
			for _, row := range served.rows {
				want := fixture.Rows[row.transcript]
				if seen[row.transcript] == 0 {
					order = append(order, row.transcript)
				}
				seen[row.transcript]++
				if got, _ := row.object["turn_count"].(float64); int64(got) != want.TurnCount {
					t.Errorf("%s: turn_count = %v, want the stored %d unchanged", row.transcript, row.object["turn_count"], want.TurnCount)
				}
				if got, ok := row.object["displayed_turn_count"].(float64); !ok || int64(got) != want.DisplayedTurnCount {
					t.Errorf("%s: displayed_turn_count = %v, want %d", row.transcript, row.object["displayed_turn_count"], want.DisplayedTurnCount)
				}
				got, served := row.object["conversation_turn_count"]
				switch {
				case want.ConversationTurnCount == nil && (!served || got != nil):
					t.Errorf("%s: conversation_turn_count = %v (key present: %v), want null for an unmeasured transcript", row.transcript, got, served)
				case want.ConversationTurnCount != nil:
					if number, ok := got.(float64); !ok || int64(number) != *want.ConversationTurnCount {
						t.Errorf("%s: conversation_turn_count = %v, want %d", row.transcript, got, *want.ConversationTurnCount)
					}
				}
			}
			for name := range fixture.Rows {
				if seen[name] != s.RowsPerTranscript {
					t.Errorf("%s is served as %d row objects, want %d", name, seen[name], s.RowsPerTranscript)
				}
			}
			if s.Order != nil && !reflect.DeepEqual(order, s.Order) {
				t.Errorf("rows are served in the order %v, want %v", order, s.Order)
			}
			if s.TotalTurns != nil {
				if len(served.totals) != 1 || int64(served.totals[0]) != *s.TotalTurns {
					t.Errorf("total_turns = %v, want exactly one total of %d", served.totals, *s.TotalTurns)
				}
			} else if len(served.totals) != 0 {
				t.Errorf("the response states total_turns %v; state the expected total in the fixture", served.totals)
			}
		})
	}
}
