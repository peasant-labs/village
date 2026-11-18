package database

import (
	_ "embed"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/migration-051-identity.yaml
var migration051IdentityYAML []byte

type migration051Case struct {
	Name       string `yaml:"name"`
	Assignment string `yaml:"assignment"`
	Accepted   bool   `yaml:"accepted"`
}

var requiredMigration051Cases = []string{
	"migration-null-and-malformed-tolerated",
	"migration-constant-default-metadata-only",
	"revision-one-with-missing-identity",
	"revision-one-with-malformed-hash",
	"revision-one-with-negative-legacy-size",
	"revision-must-be-positive",
	"revision-advanced-needs-identity",
	"revision-advanced-valid-content-identity",
	"revision-advanced-valid-segmented-identity",
	"segmented-identity-must-be-paired",
	"segmented-identity-must-be-canonical",
	"segmented-size-must-be-nonnegative",
	"identity-slots-are-exclusive",
}

func loadMigration051Cases(t *testing.T) []migration051Case {
	t.Helper()
	var fixture struct {
		Cases []migration051Case `yaml:"cases"`
	}
	if err := yaml.Unmarshal(migration051IdentityYAML, &fixture); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("invalid or repeated migration case %q", c.Name)
		}
		seen[c.Name] = true
	}
	for _, name := range requiredMigration051Cases {
		if !seen[name] {
			t.Fatalf("migration case %q is missing", name)
		}
	}
	return fixture.Cases
}

func TestMigration051ContentRevisionStructure(t *testing.T) {
	loadMigration051Cases(t)
	up, err := migrationsFS.ReadFile("migrations/051_transcript_content_revision.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := migrationsFS.ReadFile("migrations/051_transcript_content_revision.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !isRegisteredMigration(51) || !strings.Contains(string(up), "content_revision BIGINT NOT NULL DEFAULT 1") ||
		!strings.Contains(string(down), "forward-only") || !strings.Contains(string(down), "RAISE EXCEPTION") {
		t.Fatal("content revision migration registration, constant default or forward-only refusal is missing")
	}
	if strings.Contains(strings.ToUpper(string(up)), "CREATE TRIGGER") {
		t.Fatal("the additive revision migration must not install the later transition guard")
	}
}

func TestMigration051IdentityIsNotSerialized(t *testing.T) {
	var fixture struct {
		PrivateColumns []string `yaml:"private_columns"`
	}
	if err := yaml.Unmarshal(migration051IdentityYAML, &fixture); err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"content_revision": false, "segmented_identity_hash": false,
		"segmented_identity_size": false,
	}
	for _, name := range fixture.PrivateColumns {
		if _, ok := required[name]; !ok || required[name] {
			t.Fatalf("unknown or repeated private column %q", name)
		}
		required[name] = true
	}
	for name, present := range required {
		if !present {
			t.Fatalf("identity privacy fixture omits %q", name)
		}
	}
	hash := pgtype.Text{String: strings.Repeat("a", 64), Valid: true}
	size := pgtype.Int8{Int64: 42, Valid: true}
	// sqlc emits a full transcript and a separate joined full-row result;
	// both are used by production response paths.
	rows := []any{
		sqlc.Transcript{ContentRevision: 9, SegmentedIdentityHash: hash, SegmentedIdentitySize: size},
		sqlc.ListGroupTranscriptsRow{ContentRevision: 9, SegmentedIdentityHash: hash, SegmentedIdentitySize: size},
	}
	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for _, name := range fixture.PrivateColumns {
			if _, exposed := fields[name]; exposed {
				t.Fatalf("%T serializes private identity field %q", row, name)
			}
		}
	}
}
