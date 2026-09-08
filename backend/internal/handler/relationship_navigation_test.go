package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/relationship-navigation.yaml
var relationshipNavigationFixtures []byte

func TestRelationshipNavigation(t *testing.T) {
	var corpus struct {
		Required []string `yaml:"required_names"`
		Cases    []struct {
			Name     string                         `yaml:"name"`
			State    schema.RelationshipTargetState `yaml:"state"`
			Kind     schema.SessionRelationshipKind `yaml:"kind"`
			Target   string                         `yaml:"target"`
			Anchor   string                         `yaml:"anchor"`
			Expected string                         `yaml:"expected"`
		} `yaml:"cases"`
	}
	d := yaml.NewDecoder(bytes.NewReader(relationshipNavigationFixtures))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, required := range []string{"missing-parent", "cross-owner-collision", "private-parent", "public-starter", "exact-claim-without-public-authority", "unknown-parent", "conflicting-parent", "explicit-none"} {
		found := false
		for _, name := range corpus.Required {
			if name == required {
				found = true
			}
		}
		if !found {
			t.Fatalf("required-name manifest lost %s", required)
		}
	}
	for _, c := range corpus.Cases {
		if seen[c.Name] || c.Name == "" {
			t.Fatal("duplicate or empty fixture name")
		}
		seen[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			owner := toPgUUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440001"))
			targetID := toPgUUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440002"))
			localID := schema.SessionID("550e8400-e29b-41d4-a716-446655440003")
			relation := schema.SessionRelationship{Kind: c.Kind, TargetState: c.State, Evidence: schema.EvidenceNativeTyped}
			if c.Target != "" {
				relation.TargetLocalID = &localID
			}
			if c.Anchor != "" {
				if err := json.Unmarshal([]byte(c.Anchor), &relation.Anchor); err != nil {
					t.Fatal(err)
				}
			}
			if err := relation.Validate(); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal([]schema.SessionRelationship{relation})
			if err != nil {
				t.Fatal(err)
			}
			child := sqlc.Transcript{OwnerID: owner, SessionRelationships: raw}
			q := &mockQuerier{
				getTranscriptIDByOwnerAndLocalID: func(_ context.Context, p sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
					if p.OwnerID != owner || p.LocalID != string(localID) {
						t.Fatal("lookup escaped child owner-local identity")
					}
					if c.Target == "missing" || c.Target == "other-owner" {
						return pgtype.UUID{}, pgx.ErrNoRows
					}
					return targetID, nil
				},
				getTranscriptByID: func(_ context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
					if id != targetID {
						t.Fatal("wrong public target")
					}
					return sqlc.Transcript{ID: id, OwnerID: owner, LocalID: string(localID), Visibility: c.Target, Title: pgText("private target title")}, nil
				},
			}
			h := newTestHandler(q, nil)
			got, err := h.relationshipNavigation(context.Background(), nil, child)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var actual, want any
			if err := json.Unmarshal(encoded, &actual); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(c.Expected), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("navigation = %s, want %s", encoded, c.Expected)
			}
			if !bytes.Equal(child.SessionRelationships, raw) {
				t.Fatal("navigation mutated durable relationship evidence")
			}
		})
	}
	for _, name := range corpus.Required {
		if !seen[name] {
			t.Errorf("missing required fixture %s", name)
		}
	}
}
