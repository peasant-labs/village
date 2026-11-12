package handler

import (
	_ "embed"
	"testing"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/sessionorigin"
)

//go:embed testdata/schema_mapping/model_names.yaml
var modelNameFixtureYAML []byte

//go:embed testdata/schema_mapping/titles.yaml
var titleFixtureYAML []byte

type modelNameFixture struct {
	Name     string `yaml:"name"`
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	Expected string `yaml:"expected"`
}

type titleFixture struct {
	Name                  string         `yaml:"name"`
	GeneratedTitle        string         `yaml:"generated_title"`
	GeneratedTitlePresent bool           `yaml:"generated_title_present"`
	Harness               schema.Harness `yaml:"harness"`
	Model                 string         `yaml:"model"`
	ProjectName           string         `yaml:"project_name"`
	GitRemote             string         `yaml:"git_remote"`
	Expected              string         `yaml:"expected"`
	ExpectedValid         bool           `yaml:"expected_valid"`
}

func TestFormatModelShort(t *testing.T) {
	tests := loadFixtureRows[modelNameFixture](t, modelNameFixtureYAML, 6)
	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			got := formatModelShort(tt.Provider, tt.Model)
			if got != tt.Expected {
				t.Errorf("formatModelShort(%q, %q) = %q, want %q", tt.Provider, tt.Model, got, tt.Expected)
			}
		})
	}
}

func TestDeriveTitle(t *testing.T) {
	tests := loadFixtureRows[titleFixture](t, titleFixtureYAML, 6)
	for _, tt := range tests {
		t.Run(tt.Name, func(t *testing.T) {
			request := schema.PublishRequest{
				Model:   schema.ModelInfo{Harness: tt.Harness, Model: schema.ModelID(tt.Model)},
				Project: schema.ProjectContext{Name: tt.ProjectName},
			}
			if tt.GitRemote != "" {
				request.Git.Remote = &tt.GitRemote
			}
			if tt.GeneratedTitlePresent {
				request.Quality = &schema.QualityMetrics{TitleGenerated: &tt.GeneratedTitle}
			}

			got := deriveTitle(request)
			if got.Valid != tt.ExpectedValid || got.String != tt.Expected {
				t.Errorf("deriveTitle() = {%q, %v}, want {%q, %v}", got.String, got.Valid, tt.Expected, tt.ExpectedValid)
			}
		})
	}
}

//go:embed testdata/schema_mapping/conversation_turn_counts.yaml
var conversationTurnCountMappingYAML []byte

// requiredConversationTurnCountMappings is the name manifest for
// testdata/schema_mapping/conversation_turn_counts.yaml. Exact membership,
// never a count.
var requiredConversationTurnCountMappings = []string{
	"absent-count-is-sql-null",
	"measured-zero-is-a-value",
	"measured-positive",
	"safe-maximum",
}

type conversationTurnCountMappingFixture struct {
	Name      string `yaml:"name"`
	TurnCount int    `yaml:"turn_count"`
	Count     *int64 `yaml:"count"`
	WantValid bool   `yaml:"want_valid"`
	WantValue int64  `yaml:"want_value"`
}

// The publish mapper carries the conversation turn count as published: absent
// stays SQL NULL, a measured zero stays zero, and turn_count is untouched. The
// read mapper returns the same presence and value.
func TestSchemaToTranscriptParamsConversationTurnCount(t *testing.T) {
	rows, err := decodeFixtureRows[conversationTurnCountMappingFixture](conversationTurnCountMappingYAML)
	if err != nil {
		t.Fatalf("load testdata/schema_mapping/conversation_turn_counts.yaml: %v", err)
	}
	present := map[string]struct{}{}
	for _, row := range rows {
		if _, repeated := present[row.Name]; repeated {
			t.Fatalf("testdata/schema_mapping/conversation_turn_counts.yaml repeats %q", row.Name)
		}
		present[row.Name] = struct{}{}
	}
	assertExactTitleFixtureNames(t, "conversation_turn_counts", present, requiredConversationTurnCountMappings)
	for _, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			request := schema.PublishRequest{Stats: schema.SessionStats{TurnCount: row.TurnCount, ConversationTurnCount: row.Count}}
			params := schemaToTranscriptParams(request, "transcripts/fixture.bin", 1, "2", sessionorigin.Unknown)
			if params.ConversationTurnCount.Valid != row.WantValid || params.ConversationTurnCount.Int64 != row.WantValue {
				t.Fatalf("conversation_turn_count parameter = %+v, want valid=%v value=%d", params.ConversationTurnCount, row.WantValid, row.WantValue)
			}
			if !params.TurnCount.Valid || int(params.TurnCount.Int32) != row.TurnCount {
				t.Fatalf("turn_count parameter = %+v, want %d", params.TurnCount, row.TurnCount)
			}
			read := transcriptToSchema(sqlc.Transcript{TurnCount: params.TurnCount, ConversationTurnCount: params.ConversationTurnCount})
			if got := read.Stats.ConversationTurnCount; (got != nil) != row.WantValid || got != nil && *got != row.WantValue {
				t.Fatalf("read mapper conversationTurnCount = %v, want valid=%v value=%d", got, row.WantValid, row.WantValue)
			}
		})
	}
}
