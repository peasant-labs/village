package database

import (
	"bytes"
	_ "embed"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/session_graph_projection.yaml
var graphProjectionFixture []byte

type graphProjectionCase struct {
	Name          string  `yaml:"name"`
	Count         *string `yaml:"count"`
	Purpose       *string `yaml:"purpose"`
	Root          *string `yaml:"root"`
	Relationships string  `yaml:"relationships"`
	Accepted      bool    `yaml:"accepted"`
}

func loadGraphProjectionFixtures(t *testing.T) (fixture struct {
	RequiredUp   []string              `yaml:"required_up"`
	RequiredDown []string              `yaml:"required_down"`
	ForbiddenUp  []string              `yaml:"forbidden_up"`
	Cases        []graphProjectionCase `yaml:"cases"`
}) {
	t.Helper()
	d := yaml.NewDecoder(bytes.NewReader(graphProjectionFixture))
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
		if c.Name == "" || names[c.Name] || c.Relationships == "" {
			t.Fatalf("invalid or duplicate graph fixture %q", c.Name)
		}
		names[c.Name] = true
	}
	for _, name := range strings.Fields("historical-absence measured-zero measured-positive safe-maximum unknown-purpose negative-count unsafe-count fraction-count invalid-purpose empty-root nonarray-relationships nonobject-relationship") {
		if !names[name] {
			t.Fatalf("missing required graph fixture %q", name)
		}
	}
	return fixture
}

func TestMigration039SessionGraphStructure(t *testing.T) {
	f := loadGraphProjectionFixtures(t)
	up, err := migrationsFS.ReadFile("migrations/039_session_graph_provenance.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/039_session_graph_provenance.down.sql")
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
	if !isRegisteredMigration(39) {
		t.Fatal("graph migration is not registered")
	}
}
