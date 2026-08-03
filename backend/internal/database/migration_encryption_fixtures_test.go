package database

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"testing"

	"gopkg.in/yaml.v3"
)

//go:embed testdata/migration_runner/cases.yaml testdata/migration_031/cases.yaml testdata/transcript_writer_fence/cases.yaml
var encryptionMigrationFixtures embed.FS

type migrationFixture struct {
	Name      string `yaml:"name"`
	Operation string `yaml:"operation"`
}

func loadMigrationFixtures(path string, want int) ([]migrationFixture, error) {
	b, err := encryptionMigrationFixtures.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read strict migration fixture %q: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var cases []migrationFixture
	if err := dec.Decode(&cases); err != nil {
		return nil, fmt.Errorf("decode strict migration fixture %q: %w", path, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("strict migration fixture %q must contain exactly one YAML document", path)
	}
	if len(cases) != want {
		return nil, fmt.Errorf("strict migration fixture %q has %d rows, want exactly %d", path, len(cases), want)
	}
	seen := make(map[string]struct{}, len(cases))
	for i, c := range cases {
		if c.Name == "" || c.Operation == "" {
			return nil, fmt.Errorf("strict migration fixture %q row %d requires non-empty name and operation", path, i+1)
		}
		if _, ok := seen[c.Operation]; ok {
			return nil, fmt.Errorf("strict migration fixture %q repeats operation %q", path, c.Operation)
		}
		seen[c.Operation] = struct{}{}
	}
	return cases, nil
}

func TestEncryptionMigrationFixturesAreStrict(t *testing.T) {
	assertMigrationFixture(t, "testdata/migration_runner/cases.yaml", 5)
	assertMigrationFixture(t, "testdata/migration_031/cases.yaml", 6)
	assertMigrationFixture(t, "testdata/transcript_writer_fence/cases.yaml", 10)
}

func assertMigrationFixture(t *testing.T, path string, want int) {
	t.Helper()
	if _, err := loadMigrationFixtures(path, want); err != nil {
		t.Errorf("%s: %v", path, err)
	}
}
