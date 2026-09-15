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

type relationshipNavigationCase struct {
	Name              string                         `yaml:"name"`
	State             schema.RelationshipTargetState `yaml:"state"`
	Kind              schema.SessionRelationshipKind `yaml:"kind"`
	Evidence          schema.EvidenceKind            `yaml:"evidence"`
	Target            string                         `yaml:"target"`
	Lookup            string                         `yaml:"lookup"`
	Collision         bool                           `yaml:"collision_owner_holds_local_id"`
	TargetVisibility  string                         `yaml:"target_visibility"`
	Viewer            string                         `yaml:"viewer"`
	TargetContentHash string                         `yaml:"target_content_hash"`
	Anchor            string                         `yaml:"anchor"`
	Expected          string                         `yaml:"expected"`
}

type relationshipNavigationCorpus struct {
	Required []string                     `yaml:"required_names"`
	Cases    []relationshipNavigationCase `yaml:"cases"`
}

func loadRelationshipNavigationFixtures(t *testing.T) relationshipNavigationCorpus {
	t.Helper()
	var corpus relationshipNavigationCorpus
	d := yaml.NewDecoder(bytes.NewReader(relationshipNavigationFixtures))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Required) == 0 {
		t.Fatal("relationship-navigation fixture has no required-name manifest")
	}
	names := map[string]bool{}
	for _, c := range corpus.Cases {
		if c.Name == "" || names[c.Name] {
			t.Fatalf("duplicate or empty relationship-navigation case %q", c.Name)
		}
		names[c.Name] = true
	}
	for _, required := range corpus.Required {
		if !names[required] {
			t.Fatalf("required-name manifest names a missing case %q", required)
		}
	}
	if len(names) != len(corpus.Required) {
		t.Fatalf("required-name manifest covers %d of %d cases; every case must be named", len(corpus.Required), len(names))
	}
	return corpus
}

func TestRelationshipNavigation(t *testing.T) {
	corpus := loadRelationshipNavigationFixtures(t)
	childOwner := toPgUUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440001"))
	otherOwner := toPgUUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440004"))
	targetID := toPgUUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440002"))
	otherTargetID := toPgUUID(uuid.MustParse("550e8400-e29b-41d4-a716-446655440005"))
	localID := schema.SessionID("550e8400-e29b-41d4-a716-446655440003")
	for _, c := range corpus.Cases {
		t.Run(c.Name, func(t *testing.T) {
			evidence := c.Evidence
			if evidence == "" {
				evidence = schema.EvidenceNativeTyped
			}
			relation := schema.SessionRelationship{Kind: c.Kind, TargetState: c.State, Evidence: evidence}
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
			child := sqlc.Transcript{OwnerID: childOwner, SessionRelationships: raw}
			q := &mockQuerier{
				getTranscriptIDByOwnerAndLocalID: func(_ context.Context, p sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
					if p.LocalID != string(localID) {
						t.Fatalf("lookup used local id %q, want the stored target %q", p.LocalID, localID)
					}
					switch p.OwnerID {
					case childOwner:
						if c.Lookup == "found" {
							return targetID, nil
						}
						return pgtype.UUID{}, pgx.ErrNoRows
					case otherOwner:
						if c.Collision {
							return otherTargetID, nil
						}
					}
					t.Fatalf("lookup escaped the child owner scope: %v", p.OwnerID)
					return pgtype.UUID{}, pgx.ErrNoRows
				},
				getTranscriptByID: func(_ context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
					if id == otherTargetID {
						t.Fatal("a cross-owner same-local-id transcript was substituted for the child's target")
					}
					if id != targetID {
						t.Fatalf("read a target that is not the child's owner-local target: %v", id)
					}
					return sqlc.Transcript{
						ID:          id,
						OwnerID:     childOwner,
						LocalID:     string(localID),
						Visibility:  c.TargetVisibility,
						ContentHash: pgtype.Text{String: c.TargetContentHash, Valid: c.TargetContentHash != ""},
						Title:       pgText("target title that must never leak"),
					}, nil
				},
			}
			var user *AuthUser
			if c.Viewer != "anonymous" {
				user = &AuthUser{ID: uuidFromPg(childOwner)}
			}
			h := newTestHandler(q, nil)
			got, err := h.relationshipNavigation(context.Background(), user, child)
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
}
