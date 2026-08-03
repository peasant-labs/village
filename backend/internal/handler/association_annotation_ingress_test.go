package handler

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/association_annotation_ingress/push_cases.yaml
var associationAnnotationPushCasesYAML []byte

type associationAnnotationPushCase struct {
	Name                string `yaml:"name"`
	ContentHash         string `yaml:"contentHash"`
	TargetKind          string `yaml:"targetKind"`
	TargetAssociationID string `yaml:"targetAssociationId"`
	SessionID           string `yaml:"sessionId"`
	TypeID              string `yaml:"typeId"`
	Value               string `yaml:"value"`
	ExpectedStatus      int    `yaml:"expectedStatus"`
}

type associationAnnotationPushFixture struct {
	ExpectedCaseCount int                             `yaml:"expectedCaseCount"`
	RequiredCaseNames []string                        `yaml:"requiredCaseNames"`
	Cases             []associationAnnotationPushCase `yaml:"cases"`
}

func loadAssociationAnnotationPushFixture(t *testing.T) associationAnnotationPushFixture {
	t.Helper()
	fixture, err := decodeAssociationAnnotationPushFixture(associationAnnotationPushCasesYAML)
	if err != nil {
		t.Fatalf("decode association annotation push fixture: %v", err)
	}
	if len(fixture.Cases) != fixture.ExpectedCaseCount {
		t.Fatalf("association annotation push fixture has %d cases, want declared %d", len(fixture.Cases), fixture.ExpectedCaseCount)
	}
	byName := make(map[string]associationAnnotationPushCase, len(fixture.Cases))
	for _, c := range fixture.Cases {
		if c.Name == "" {
			t.Fatal("association annotation push fixture contains an unnamed case")
		}
		if _, duplicate := byName[c.Name]; duplicate {
			t.Fatalf("association annotation push fixture repeats case %q", c.Name)
		}
		byName[c.Name] = c
	}
	for _, name := range fixture.RequiredCaseNames {
		if _, exists := byName[name]; !exists {
			t.Fatalf("association annotation push fixture omits required case %q", name)
		}
	}
	return fixture
}

func decodeAssociationAnnotationPushFixture(data []byte) (associationAnnotationPushFixture, error) {
	var fixture associationAnnotationPushFixture
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		return associationAnnotationPushFixture{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return associationAnnotationPushFixture{}, errors.New("fixture contains a trailing YAML document")
		}
		return associationAnnotationPushFixture{}, err
	}
	return fixture, nil
}

func TestAssociationAnnotationPushFixtureRejectsTrailingDocument(t *testing.T) {
	_, err := decodeAssociationAnnotationPushFixture(append(append([]byte{}, associationAnnotationPushCasesYAML...), []byte("\n---\nunexpected: document\n")...))
	if err == nil || !strings.Contains(err.Error(), "trailing YAML document") {
		t.Fatalf("trailing fixture document error = %v, want explicit rejection", err)
	}
}

func associationAnnotationPushItem(c associationAnnotationPushCase) schema.AnnotationPushItem {
	item := schema.AnnotationPushItem{
		ContentHash: c.ContentHash,
		TargetKind:  schema.TargetKind(c.TargetKind),
		TypeID:      c.TypeID,
		Value:       c.Value,
	}
	if c.TargetAssociationID != "" {
		associationID := schema.AssociationID(c.TargetAssociationID)
		item.TargetAssociationID = &associationID
	}
	if c.SessionID != "" {
		sessionID := c.SessionID
		item.SessionID = &sessionID
	}
	return item
}

// TestUploadAnnotations_AssociationTargetContract drives the mounted annotation
// endpoint with the contract cases. Invalid exclusive-target shapes must be
// rejected by the published schema before any association lookup or write runs.
func TestUploadAnnotations_AssociationTargetContract(t *testing.T) {
	fixture := loadAssociationAnnotationPushFixture(t)
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			item := associationAnnotationPushItem(c)
			var stored []bulkAnnotationRecord
			q := &mockQuerier{
				listTranscriptAssociationIDsByOwner: func(_ context.Context, arg sqlc.ListTranscriptAssociationIDsByOwnerParams) ([]string, error) {
					if c.ExpectedStatus != http.StatusOK {
						t.Fatalf("invalid request reached association lookup for %q", arg.AssociationIds)
					}
					return arg.AssociationIds, nil
				},
				bulkUpsertAnnotations: func(_ context.Context, arg sqlc.BulkUpsertAnnotationsParams) ([]sqlc.BulkUpsertAnnotationsRow, error) {
					if err := json.Unmarshal(arg.Items, &stored); err != nil {
						t.Fatalf("decode persisted annotation records: %v", err)
					}
					return []sqlc.BulkUpsertAnnotationsRow{{ContentHash: c.ContentHash, Created: true}}, nil
				},
			}
			h := newTestHandler(q, nil)
			w := postAnnotationPush(t, h, schema.AnnotationPushRequest{Annotations: []schema.AnnotationPushItem{item}})
			if w.Code != c.ExpectedStatus {
				t.Fatalf("status: got %d, want %d (body: %s)", w.Code, c.ExpectedStatus, w.Body.String())
			}
			if c.ExpectedStatus != http.StatusOK {
				if got := decodeError(t, w.Body.Bytes()); !strings.Contains(got, "annotation request failed schema validation") {
					t.Errorf("invalid association target error %q does not identify schema validation", got)
				}
				return
			}
			if len(stored) != 1 || stored[0].AssociationID == nil || *stored[0].AssociationID != c.TargetAssociationID {
				t.Fatalf("stored association target: got %+v, want exactly %q", stored, c.TargetAssociationID)
			}
		})
	}
}

// TestResolveAssociationTargets_RejectsUnknownOwnerTarget is a storage-boundary
// guard. The HTTP schema validates shape; this check ensures a valid opaque ID
// cannot cross an owner boundary or reference an unpublished relationship.
func TestResolveAssociationTargets_RejectsUnknownOwnerTarget(t *testing.T) {
	fixture := loadAssociationAnnotationPushFixture(t)
	var valid associationAnnotationPushCase
	for _, c := range fixture.Cases {
		if c.ExpectedStatus == http.StatusOK {
			valid = c
			break
		}
	}
	if valid.Name == "" {
		t.Fatal("fixture has no valid association annotation case")
	}
	item := associationAnnotationPushItem(valid)
	owner := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	err := resolveAssociationTargets(context.Background(), &mockQuerier{
		listTranscriptAssociationIDsByOwner: func(context.Context, sqlc.ListTranscriptAssociationIDsByOwnerParams) ([]string, error) {
			return nil, nil
		},
	}, owner, []schema.AnnotationPushItem{item})
	if !errors.Is(err, ErrAssociationBinding) {
		t.Fatalf("unknown owner association error = %v, want ErrAssociationBinding", err)
	}
	if !strings.Contains(err.Error(), "not recorded for the authenticated owner") {
		t.Errorf("unknown owner association error %q does not provide remediation", err)
	}
}

// TestAnnotationRowToSummary_AssociationTarget keeps the response projection
// aligned with stored association annotations. The mounted transcript endpoint
// uses this mapper after the ledger-backed discovery query.
func TestAnnotationRowToSummary_AssociationTarget(t *testing.T) {
	fixture := loadAssociationAnnotationPushFixture(t)
	var valid associationAnnotationPushCase
	for _, c := range fixture.Cases {
		if c.ExpectedStatus == http.StatusOK {
			valid = c
			break
		}
	}
	if valid.Name == "" {
		t.Fatal("fixture has no valid association annotation case")
	}
	summary := annotationRowToSummary(sqlc.Annotation{
		ID:                  pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		TargetKind:          string(schema.TargetAssociation),
		TargetAssociationID: pgText(valid.TargetAssociationID),
		TypeID:              valid.TypeID,
		Value:               valid.Value,
		CreatedAt:           pgtype.Timestamptz{Time: time.UnixMilli(1700000000000), Valid: true},
	})
	if summary.TargetAssociationID == nil || summary.TargetAssociationID.String() != valid.TargetAssociationID {
		t.Fatalf("response association target: got %v, want %q", summary.TargetAssociationID, valid.TargetAssociationID)
	}
	if summary.TargetSessionID != nil || summary.TargetEntryIndex != nil || summary.TargetAnnotID != nil || summary.TargetProjectHash != nil {
		t.Fatalf("association response leaked another target arm: %+v", summary)
	}
}
