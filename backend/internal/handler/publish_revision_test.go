package handler

import (
	_ "embed"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/publish_revision.yaml
var publishRevisionYAML []byte

type revisionFixtureSlot string
type revisionFixtureError string

const (
	revisionFixtureContent   revisionFixtureSlot  = "content"
	revisionFixtureSegmented revisionFixtureSlot  = "segmented"
	revisionFixturePending   revisionFixtureSlot  = "pending"
	revisionFixtureMalformed revisionFixtureSlot  = "malformed"
	revisionFixtureDivergent revisionFixtureError = "divergent"
	revisionFixtureOverflow  revisionFixtureError = "overflow"
)

var requiredPublishRevisionCases = []string{
	"republish-identical-keeps",
	"republish-changed-advances",
	"republish-exposed-identical-keeps",
	"republish-exposed-different-advances",
	"republish-pending-requires-authentication",
	"republish-malformed-requires-authentication",
	"republish-divergent-r-greater-than-one-refused",
	"overflow-refused",
	"max-revision-identical-kept",
}

func TestPublishRevisionDecisions(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name         string               `yaml:"name"`
			Slot         revisionFixtureSlot  `yaml:"slot"`
			Revision     int64                `yaml:"revision"`
			OldHash      string               `yaml:"old_hash"`
			OldSize      int64                `yaml:"old_size"`
			NewHash      string               `yaml:"new_hash"`
			NewSize      int64                `yaml:"new_size"`
			WantRevision int64                `yaml:"want_revision"`
			WantPending  bool                 `yaml:"want_pending"`
			WantError    revisionFixtureError `yaml:"want_error"`
		} `yaml:"cases"`
	}
	if err := yaml.Unmarshal(publishRevisionYAML, &fixture); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate or empty publish revision case %q", c.Name)
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			row := sqlc.Transcript{ContentRevision: c.Revision}
			switch c.Slot {
			case revisionFixtureContent, revisionFixtureMalformed:
				row.ContentHash = pgtype.Text{String: c.OldHash, Valid: true}
				row.BlobSizeBytes = pgtype.Int8{Int64: c.OldSize, Valid: true}
			case revisionFixtureSegmented:
				row.SegmentedIdentityHash = pgtype.Text{String: c.OldHash, Valid: true}
				row.SegmentedIdentitySize = pgtype.Int8{Int64: c.OldSize, Valid: true}
			case revisionFixturePending:
			default:
				t.Fatalf("unknown identity slot %q", c.Slot)
			}
			old, known, err := establishedRevisionIdentity(row)
			if c.WantError == revisionFixtureDivergent {
				if !errors.Is(err, errDivergentRevision) {
					t.Fatalf("identity classification = %v, want divergent refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.WantPending {
				if known {
					t.Fatal("unverified predecessor was classified as an established identity")
				}
				return
			}
			if !known {
				t.Fatal("valid predecessor identity was not classified")
			}
			next, err := storage.NewContentIdentity(c.NewHash, c.NewSize)
			if err != nil {
				t.Fatal(err)
			}
			revision, err := nextContentRevision(c.Revision, old, next)
			if c.WantError == revisionFixtureOverflow {
				if !errors.Is(err, errRevisionOverflow) {
					t.Fatalf("revision decision = %v, want overflow refusal", err)
				}
				return
			}
			if err != nil || revision != c.WantRevision {
				t.Fatalf("revision = %d, error = %v, want %d", revision, err, c.WantRevision)
			}
		})
	}
	for _, name := range requiredPublishRevisionCases {
		if !seen[name] {
			t.Fatalf("publish revision fixture omits %q", name)
		}
	}
}
