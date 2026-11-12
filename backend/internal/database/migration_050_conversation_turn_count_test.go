package database

import (
	"bytes"
	_ "embed"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/conversation_turn_count.yaml
var conversationTurnCountFixture []byte

type conversationTurnCountCase struct {
	Name      string  `yaml:"name"`
	Count     *string `yaml:"count"`
	TurnCount *int32  `yaml:"turn_count"`
	Accepted  bool    `yaml:"accepted"`
	Displayed *int64  `yaml:"displayed"`
}

type conversationTurnCountFixtures struct {
	RequiredUp   []string                    `yaml:"required_up"`
	RequiredDown []string                    `yaml:"required_down"`
	ForbiddenUp  []string                    `yaml:"forbidden_up"`
	Cases        []conversationTurnCountCase `yaml:"cases"`
}

func loadConversationTurnCountFixtures(t *testing.T) conversationTurnCountFixtures {
	t.Helper()
	var fixture conversationTurnCountFixtures
	d := yaml.NewDecoder(bytes.NewReader(conversationTurnCountFixture))
	d.KnownFields(true)
	if err := d.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		t.Fatalf("fixture trailing document: %v", err)
	}
	names := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || names[c.Name] {
			t.Fatalf("invalid or duplicate conversation turn count fixture %q", c.Name)
		}
		if !c.Accepted && c.Displayed != nil {
			t.Fatalf("fixture %q is refused and cannot state a displayed value", c.Name)
		}
		names[c.Name] = true
	}
	for _, name := range strings.Fields("historical-absence measured-zero measured-positive safe-maximum negative-count unsafe-count fraction-count displayed-falls-back-to-turn-count displayed-prefers-conversation-count displayed-clamps-negative-turn-count") {
		if !names[name] {
			t.Fatalf("missing required conversation turn count fixture %q", name)
		}
	}
	return fixture
}

func TestMigration050ConversationTurnCountStructure(t *testing.T) {
	f := loadConversationTurnCountFixtures(t)
	up, err := migrationsFS.ReadFile("migrations/050_conversation_turn_count.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/050_conversation_turn_count.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range f.RequiredUp {
		if !strings.Contains(string(up), text) {
			t.Fatalf("up SQL missing %q", text)
		}
	}
	for _, text := range f.RequiredDown {
		if !strings.Contains(string(down), text) {
			t.Fatalf("down SQL missing %q", text)
		}
	}
	for _, text := range f.ForbiddenUp {
		if strings.Contains(string(up), text) {
			t.Fatalf("up SQL must not contain %q", text)
		}
	}
	if !isRegisteredMigration(50) {
		t.Fatal("conversation turn count migration is not registered")
	}
}
