package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/association_annotation_ingress/batch_cases.yaml
var associationBatchCasesYAML []byte

type associationBatchMemberFixture struct {
	AssociationID      string `yaml:"associationId"`
	ObservedCommitHash string `yaml:"observedCommitHash"`
}

type associationBatchCaseFixture struct {
	Name         string                          `yaml:"name"`
	Associations []associationBatchMemberFixture `yaml:"associations"`
}

type associationBatchFixture struct {
	ExpectedCaseCount int                           `yaml:"expectedCaseCount"`
	RequiredCaseNames []string                      `yaml:"requiredCaseNames"`
	Cases             []associationBatchCaseFixture `yaml:"cases"`
}

func loadAssociationBatchFixture(t *testing.T) associationBatchFixture {
	t.Helper()
	fixture, err := decodeAssociationBatchFixture(associationBatchCasesYAML)
	if err != nil {
		t.Fatalf("decode association batch fixture: %v", err)
	}
	if len(fixture.Cases) != fixture.ExpectedCaseCount {
		t.Fatalf("association batch fixture has %d cases, want declared %d", len(fixture.Cases), fixture.ExpectedCaseCount)
	}
	seen := make(map[string]struct{}, len(fixture.Cases))
	for _, c := range fixture.Cases {
		if c.Name == "" || len(c.Associations) < 2 {
			t.Fatalf("association batch fixture case %+v must name at least two associations", c)
		}
		if _, duplicate := seen[c.Name]; duplicate {
			t.Fatalf("association batch fixture repeats case %q", c.Name)
		}
		seen[c.Name] = struct{}{}
	}
	for _, required := range fixture.RequiredCaseNames {
		if _, exists := seen[required]; !exists {
			t.Fatalf("association batch fixture omits required case %q", required)
		}
	}
	return fixture
}

func decodeAssociationBatchFixture(data []byte) (associationBatchFixture, error) {
	var fixture associationBatchFixture
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		return associationBatchFixture{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return associationBatchFixture{}, errors.New("fixture contains a trailing YAML document")
		}
		return associationBatchFixture{}, err
	}
	return fixture, nil
}

func TestAssociationBatchFixtureRejectsTrailingDocument(t *testing.T) {
	_, err := decodeAssociationBatchFixture(append(append([]byte{}, associationBatchCasesYAML...), []byte("\n---\nunexpected: document\n")...))
	if err == nil || !strings.Contains(err.Error(), "trailing YAML document") {
		t.Fatalf("trailing fixture document error = %v, want explicit rejection", err)
	}
}

// TestAssociationBatchUsesSetQueries proves the number of database operations
// is constant for a multi-item payload: one owner/ID lookup, one relationship
// lookup, one JSONB insert, and one target authorization lookup. The fixture
// gives the representative multi-item input; the real-Postgres suite proves the
// same generated SQL persists it atomically.
func TestAssociationBatchUsesSetQueries(t *testing.T) {
	fixture := loadAssociationBatchFixture(t)
	var caseFixture associationBatchCaseFixture
	for _, candidate := range fixture.Cases {
		if candidate.Name == "multi-item association batch uses set queries" {
			caseFixture = candidate
			break
		}
	}
	if caseFixture.Name == "" {
		t.Fatal("association batch fixture has no multi-item set-query case")
	}
	associations := make([]schema.PublishedAssociation, 0, len(caseFixture.Associations))
	annotationItems := make([]schema.AnnotationPushItem, 0, len(caseFixture.Associations))
	for _, member := range caseFixture.Associations {
		associationID := schema.AssociationID(member.AssociationID)
		associations = append(associations, schema.PublishedAssociation{ID: associationID, ObservedCommitHash: member.ObservedCommitHash})
		annotationItems = append(annotationItems, schema.AnnotationPushItem{
			ContentHash:         "batch-" + member.AssociationID,
			TargetKind:          schema.TargetAssociation,
			TargetAssociationID: &associationID,
			TypeID:              "quality.session_outcome",
			Value:               "resolved",
		})
	}
	ownerID := pgtype.UUID{Bytes: [16]byte{4}, Valid: true}
	transcriptID := pgtype.UUID{Bytes: [16]byte{5}, Valid: true}
	ownerIDLookups := 0
	relationshipLookups := 0
	insertCalls := 0
	targetLookups := 0
	q := &mockQuerier{
		listTranscriptAssociationsByOwnerAndIDs: func(_ context.Context, arg sqlc.ListTranscriptAssociationsByOwnerAndIDsParams) ([]sqlc.TranscriptAssociation, error) {
			ownerIDLookups++
			if len(arg.AssociationIds) != len(associations) {
				t.Fatalf("owner/ID lookup contains %d IDs, want %d", len(arg.AssociationIds), len(associations))
			}
			return nil, nil
		},
		listTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashes: func(_ context.Context, arg sqlc.ListTranscriptAssociationsByOwnerTranscriptAndObservedCommitHashesParams) ([]sqlc.TranscriptAssociation, error) {
			relationshipLookups++
			if arg.TranscriptID != transcriptID || len(arg.ObservedCommitHashes) != len(associations) {
				t.Fatalf("relationship lookup = %+v, want transcript and %d hashes", arg, len(associations))
			}
			return nil, nil
		},
		insertTranscriptAssociations: func(_ context.Context, arg sqlc.InsertTranscriptAssociationsParams) error {
			insertCalls++
			var records []map[string]string
			if err := json.Unmarshal(arg.Items, &records); err != nil {
				t.Fatalf("decode batch association insert: %v", err)
			}
			if arg.OwnerID != ownerID || arg.TranscriptID != transcriptID || len(records) != len(associations) {
				t.Fatalf("batch association insert = owner=%+v transcript=%+v records=%v", arg.OwnerID, arg.TranscriptID, records)
			}
			return nil
		},
		listTranscriptAssociationIDsByOwner: func(_ context.Context, arg sqlc.ListTranscriptAssociationIDsByOwnerParams) ([]string, error) {
			targetLookups++
			if len(arg.AssociationIds) != len(associations) {
				t.Fatalf("target lookup contains %d IDs, want %d", len(arg.AssociationIds), len(associations))
			}
			return arg.AssociationIds, nil
		},
	}
	newAssociations, err := validatePublishedAssociationBindings(context.Background(), q, ownerID, transcriptID, associations)
	if err != nil {
		t.Fatalf("validate batch associations: %v", err)
	}
	if len(newAssociations) != len(associations) {
		t.Fatalf("new batch associations=%d, want %d", len(newAssociations), len(associations))
	}
	if err := insertPublishedAssociationBindings(context.Background(), q, ownerID, transcriptID, newAssociations); err != nil {
		t.Fatalf("insert batch associations: %v", err)
	}
	if err := resolveAssociationTargets(context.Background(), q, ownerID, annotationItems); err != nil {
		t.Fatalf("resolve batch association targets: %v", err)
	}
	if ownerIDLookups != 1 || relationshipLookups != 1 || insertCalls != 1 || targetLookups != 1 {
		t.Fatalf("batch query counts ownerIDs=%d relationships=%d inserts=%d targetIDs=%d, want 1/1/1/1", ownerIDLookups, relationshipLookups, insertCalls, targetLookups)
	}
}
