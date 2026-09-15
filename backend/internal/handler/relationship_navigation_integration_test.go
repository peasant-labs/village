//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/storage"
	"gopkg.in/yaml.v3"
)

//go:embed testdata/relationship-navigation-integration.yaml
var relationshipNavigationIntegrationYAML []byte

// Actions. A case is an ordered list of these; the stateful publish/read work
// lives in Go, while the fixture holds every input and expectation.
const (
	relationshipNavigationActionPublish               = "publish"
	relationshipNavigationActionInsert                = "insert"
	relationshipNavigationActionRead                  = "read"
	relationshipNavigationActionCaptureContent        = "capture_content"
	relationshipNavigationActionExpectContentSame     = "expect_content_unchanged"
	relationshipNavigationActionExpectRevisionChanged = "expect_revision_changed"
)

const (
	relationshipNavigationOwnerSelf  = "self"
	relationshipNavigationOwnerOther = "other"

	relationshipNavigationVisibilityPublic  = "public"
	relationshipNavigationVisibilityPrivate = "private"

	relationshipNavigationViewerOwner  = "owner"
	relationshipNavigationViewerViewer = "viewer"

	relationshipNavigationIdentityNone = "none"

	relationshipNavigationRevisionLiteral       = "literal"
	relationshipNavigationRevisionCurrentPublic = "current_public_revision"
	relationshipNavigationRevisionUnset         = ""
	relationshipNavigationLocalIDNamespace      = "relationship-navigation"
)

type relationshipNavigationIntegrationFixture struct {
	Required []string                                `yaml:"required_names"`
	Markers  map[string]string                       `yaml:"markers"`
	Cases    []relationshipNavigationIntegrationCase `yaml:"cases"`
}

type relationshipNavigationIntegrationCase struct {
	Name          string                                  `yaml:"name"`
	Why           string                                  `yaml:"why"`
	AbsentTargets []string                                `yaml:"absent_targets"`
	Steps         []relationshipNavigationIntegrationStep `yaml:"steps"`
}

type relationshipNavigationIntegrationStep struct {
	Do            string                                          `yaml:"do"`
	Transcript    string                                          `yaml:"transcript"`
	LocalID       string                                          `yaml:"local_id"`
	Owner         string                                          `yaml:"owner"`
	Visibility    string                                          `yaml:"visibility"`
	Turn          string                                          `yaml:"turn"`
	Title         string                                          `yaml:"title"`
	Writes        *int                                            `yaml:"writes"`
	Relationships []relationshipNavigationIntegrationRelationship `yaml:"relationships"`
	Viewer        string                                          `yaml:"viewer"`
	Absent        bool                                            `yaml:"absent"`
	Navigation    []relationshipNavigationIntegrationExpectation  `yaml:"navigation"`
	Leaks         relationshipNavigationIntegrationLeaks          `yaml:"leaks"`
}

type relationshipNavigationIntegrationRelationship struct {
	Kind     schema.SessionRelationshipKind           `yaml:"kind"`
	State    schema.RelationshipTargetState           `yaml:"state"`
	Evidence schema.EvidenceKind                      `yaml:"evidence"`
	Target   string                                   `yaml:"target"`
	Anchor   *relationshipNavigationIntegrationAnchor `yaml:"anchor"`
}

type relationshipNavigationIntegrationAnchor struct {
	Kind        schema.PublicSourceAnchorKind `yaml:"kind"`
	Entry       string                        `yaml:"entry"`
	Revision    string                        `yaml:"revision"`
	RevisionRef string                        `yaml:"revision_ref"`
}

type relationshipNavigationIntegrationExpectation struct {
	Kind           schema.SessionRelationshipKind      `yaml:"kind"`
	Status         schema.RelationshipNavigationStatus `yaml:"status"`
	Identity       string                              `yaml:"identity"`
	AnchorKind     schema.PublicSourceAnchorKind       `yaml:"anchor_kind"`
	AnchorEntry    string                              `yaml:"anchor_entry"`
	AnchorRevision string                              `yaml:"anchor_revision"`
}

type relationshipNavigationIntegrationLeaks struct {
	Transcripts []string `yaml:"transcripts"`
	Titles      []string `yaml:"titles"`
}

// relationshipNavigationCreateDecl records one step that creates the owner-local
// row a relationship can resolve to.
type relationshipNavigationCreateDecl struct {
	name   string
	alias  string
	owner  string
	action string
	index  int
}

func loadRelationshipNavigationIntegrationFixture(t *testing.T) relationshipNavigationIntegrationFixture {
	t.Helper()
	var fixture relationshipNavigationIntegrationFixture
	decoder := yaml.NewDecoder(bytes.NewReader(relationshipNavigationIntegrationYAML))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("relationship-navigation integration fixture: decode: %v", err)
	}
	if len(fixture.Required) == 0 {
		t.Fatal("relationship-navigation integration fixture has no required-name manifest")
	}
	if len(fixture.Markers) == 0 {
		t.Fatal("relationship-navigation integration fixture has no markers")
	}
	for name, marker := range fixture.Markers {
		if marker == "" {
			t.Fatalf("marker %q is empty", name)
		}
		for otherName, otherMarker := range fixture.Markers {
			if otherName != name && strings.Contains(otherMarker, marker) {
				t.Fatalf("marker %q is a substring of marker %q; a leak assertion could pass by matching the wrong marker", name, otherName)
			}
		}
	}
	names := map[string]bool{}
	for _, testCase := range fixture.Cases {
		if testCase.Name == "" || names[testCase.Name] {
			t.Fatalf("duplicate or empty relationship-navigation integration case %q", testCase.Name)
		}
		names[testCase.Name] = true
		validateRelationshipNavigationIntegrationCase(t, fixture, testCase)
	}
	for _, required := range fixture.Required {
		if !names[required] {
			t.Fatalf("required-name manifest names a missing case %q", required)
		}
	}
	if len(names) != len(fixture.Required) {
		t.Fatalf("required-name manifest covers %d of %d cases; every case must be named", len(fixture.Required), len(names))
	}
	return fixture
}

func validateRelationshipNavigationIntegrationCase(t *testing.T, fixture relationshipNavigationIntegrationFixture, testCase relationshipNavigationIntegrationCase) {
	t.Helper()
	if testCase.Why == "" {
		t.Fatalf("relationship-navigation scenario %s has no why", testCase.Name)
	}
	if len(testCase.Steps) == 0 {
		t.Fatalf("relationship-navigation scenario %s has no steps", testCase.Name)
	}
	absentTargets := map[string]bool{}
	for _, alias := range testCase.AbsentTargets {
		if alias == "" {
			t.Fatalf("relationship-navigation scenario %s declares an empty absent target", testCase.Name)
		}
		absentTargets[alias] = true
	}

	decls := make([]relationshipNavigationCreateDecl, 0, len(testCase.Steps))
	for index, step := range testCase.Steps {
		switch step.Do {
		case relationshipNavigationActionPublish:
			relationshipNavigationRequireField(t, testCase.Name, step.Do, "transcript", step.Transcript)
			relationshipNavigationRequireField(t, testCase.Name, step.Do, "local_id", step.LocalID)
			if _, ok := fixture.Markers[step.Turn]; !ok {
				t.Fatalf("relationship-navigation scenario %s action publish has unknown marker %q", testCase.Name, step.Turn)
			}
			relationshipNavigationForbidFields(t, testCase.Name, step.Do,
				[2]string{"owner", step.Owner},
				[2]string{"visibility", step.Visibility},
				[2]string{"title", step.Title},
				[2]string{"viewer", step.Viewer},
			)
			if step.Absent || len(step.Navigation) != 0 || len(step.Leaks.Transcripts) != 0 || len(step.Leaks.Titles) != 0 {
				t.Fatalf("relationship-navigation scenario %s action publish must carry no read expectations", testCase.Name)
			}
			if step.Writes != nil && *step.Writes < 1 {
				t.Fatalf("relationship-navigation scenario %s action publish declares writes=%d, want at least one object", testCase.Name, *step.Writes)
			}
			decls = append(decls, relationshipNavigationCreateDecl{name: step.Transcript, alias: step.LocalID, owner: relationshipNavigationOwnerSelf, action: step.Do, index: index})
		case relationshipNavigationActionInsert:
			relationshipNavigationRequireField(t, testCase.Name, step.Do, "transcript", step.Transcript)
			relationshipNavigationRequireField(t, testCase.Name, step.Do, "local_id", step.LocalID)
			relationshipNavigationRequireField(t, testCase.Name, step.Do, "title", step.Title)
			if _, ok := fixture.Markers[step.Title]; !ok {
				t.Fatalf("relationship-navigation scenario %s action insert names unknown marker %q", testCase.Name, step.Title)
			}
			if !relationshipNavigationClosedValue(step.Owner, relationshipNavigationOwnerSelf, relationshipNavigationOwnerOther) {
				t.Fatalf("relationship-navigation scenario %s action insert has unknown owner %q", testCase.Name, step.Owner)
			}
			if !relationshipNavigationClosedValue(step.Visibility, relationshipNavigationVisibilityPublic, relationshipNavigationVisibilityPrivate) {
				t.Fatalf("relationship-navigation scenario %s action insert has unknown visibility %q", testCase.Name, step.Visibility)
			}
			relationshipNavigationForbidFields(t, testCase.Name, step.Do,
				[2]string{"turn", step.Turn},
				[2]string{"viewer", step.Viewer},
			)
			if step.Writes != nil {
				t.Fatalf("relationship-navigation scenario %s action insert must not declare writes", testCase.Name)
			}
			if step.Absent || len(step.Navigation) != 0 || len(step.Leaks.Transcripts) != 0 || len(step.Leaks.Titles) != 0 {
				t.Fatalf("relationship-navigation scenario %s action insert must carry no read expectations", testCase.Name)
			}
			decls = append(decls, relationshipNavigationCreateDecl{name: step.Transcript, alias: step.LocalID, owner: step.Owner, action: step.Do, index: index})
		case relationshipNavigationActionRead:
			relationshipNavigationRequireField(t, testCase.Name, step.Do, "transcript", step.Transcript)
			if !relationshipNavigationClosedValue(step.Viewer, relationshipNavigationViewerOwner, relationshipNavigationViewerViewer) {
				t.Fatalf("relationship-navigation scenario %s action read has unknown viewer %q", testCase.Name, step.Viewer)
			}
			relationshipNavigationForbidFields(t, testCase.Name, step.Do,
				[2]string{"local_id", step.LocalID},
				[2]string{"owner", step.Owner},
				[2]string{"visibility", step.Visibility},
				[2]string{"turn", step.Turn},
				[2]string{"title", step.Title},
			)
			if step.Writes != nil || len(step.Relationships) != 0 {
				t.Fatalf("relationship-navigation scenario %s action read must not create a transcript", testCase.Name)
			}
			if step.Absent && len(step.Navigation) != 0 {
				t.Fatalf("relationship-navigation scenario %s action read declares both absent and navigation expectations", testCase.Name)
			}
			if !step.Absent && len(step.Navigation) == 0 {
				t.Fatalf("relationship-navigation scenario %s action read declares neither absent nor navigation expectations", testCase.Name)
			}
		case relationshipNavigationActionCaptureContent, relationshipNavigationActionExpectContentSame, relationshipNavigationActionExpectRevisionChanged:
			relationshipNavigationRequireField(t, testCase.Name, step.Do, "transcript", step.Transcript)
			relationshipNavigationForbidFields(t, testCase.Name, step.Do,
				[2]string{"local_id", step.LocalID},
				[2]string{"owner", step.Owner},
				[2]string{"visibility", step.Visibility},
				[2]string{"turn", step.Turn},
				[2]string{"title", step.Title},
				[2]string{"viewer", step.Viewer},
			)
			if step.Writes != nil || len(step.Relationships) != 0 || len(step.Navigation) != 0 || step.Absent {
				t.Fatalf("relationship-navigation scenario %s action %s must carry only a transcript", testCase.Name, step.Do)
			}
		default:
			t.Fatalf("relationship-navigation scenario %s step %d has unknown action %q", testCase.Name, index, step.Do)
		}
	}

	byLocalID := map[string][]relationshipNavigationCreateDecl{}
	byName := map[string]relationshipNavigationCreateDecl{}
	byOwnerLocal := map[string]string{}
	for _, decl := range decls {
		byLocalID[decl.alias] = append(byLocalID[decl.alias], decl)
		if existing, ok := byName[decl.name]; ok && existing.alias != decl.alias {
			t.Fatalf("relationship-navigation scenario %s transcript %s is created under both %q and %q", testCase.Name, decl.name, existing.alias, decl.alias)
		}
		byName[decl.name] = decl
		ownerLocal := decl.owner + "/" + decl.alias
		if existing, ok := byOwnerLocal[ownerLocal]; ok && existing != decl.name {
			t.Fatalf("relationship-navigation scenario %s owners %s create two transcripts under local id %q (%s and %s)", testCase.Name, decl.owner, decl.alias, existing, decl.name)
		}
		byOwnerLocal[ownerLocal] = decl.name
	}
	for alias := range absentTargets {
		if len(byLocalID[alias]) != 0 {
			t.Fatalf("relationship-navigation scenario %s lists local id %q as absent but also creates it", testCase.Name, alias)
		}
	}

	for index, step := range testCase.Steps {
		switch step.Do {
		case relationshipNavigationActionPublish, relationshipNavigationActionInsert:
			validateRelationshipNavigationIntegrationRelationships(t, testCase, step, index, byLocalID, byName, absentTargets)
		case relationshipNavigationActionRead:
			validateRelationshipNavigationIntegrationRead(t, testCase, step, byName, fixture)
		case relationshipNavigationActionExpectRevisionChanged:
			publishes := 0
			for _, decl := range decls {
				if decl.name == step.Transcript && decl.action == relationshipNavigationActionPublish {
					publishes++
				}
			}
			if publishes < 2 {
				t.Fatalf("relationship-navigation scenario %s expects %s to change revision after %d publishes", testCase.Name, step.Transcript, publishes)
			}
			if _, ok := byName[step.Transcript]; !ok {
				t.Fatalf("relationship-navigation scenario %s expects an unknown transcript %s to change revision", testCase.Name, step.Transcript)
			}
		}
	}
}

func validateRelationshipNavigationIntegrationRelationships(
	t *testing.T,
	testCase relationshipNavigationIntegrationCase,
	step relationshipNavigationIntegrationStep,
	index int,
	byLocalID map[string][]relationshipNavigationCreateDecl,
	byName map[string]relationshipNavigationCreateDecl,
	absentTargets map[string]bool,
) {
	t.Helper()
	if _, ok := byName[step.Transcript]; !ok {
		t.Fatalf("relationship-navigation scenario %s action %s creates an undeclared transcript %q", testCase.Name, step.Do, step.Transcript)
	}
	for _, relation := range step.Relationships {
		if !relation.Kind.IsValid() || !relation.State.IsValid() || !relation.Evidence.IsValid() {
			t.Fatalf("relationship-navigation scenario %s action %s has a relationship outside its closed kind, state, or evidence set", testCase.Name, step.Do)
		}
		known := relation.State == schema.RelationshipTargetKnown || relation.State == schema.RelationshipTargetKnownRetained
		if known != (relation.Target != "") {
			t.Fatalf("relationship-navigation scenario %s action %s has a target that disagrees with state %q", testCase.Name, step.Do, relation.State)
		}
		if known {
			if _, ok := byLocalID[relation.Target]; !ok && !absentTargets[relation.Target] {
				t.Fatalf("relationship-navigation scenario %s action %s targets undeclared local id %q", testCase.Name, step.Do, relation.Target)
			}
		}
		if relation.Anchor == nil {
			continue
		}
		if relation.Kind != schema.SessionRelationshipContextFrom || !known {
			t.Fatalf("relationship-navigation scenario %s action %s anchors a relationship that cannot carry one", testCase.Name, step.Do)
		}
		if !relation.Anchor.Kind.IsValid() {
			t.Fatalf("relationship-navigation scenario %s action %s has an unknown anchor kind %q", testCase.Name, step.Do, relation.Anchor.Kind)
		}
		if relation.Anchor.Kind == schema.PublicSourceAnchorGeneral {
			if relation.Anchor.Entry != "" || relation.Anchor.Revision != "" || relation.Anchor.RevisionRef != "" {
				t.Fatalf("relationship-navigation scenario %s action %s gives a general anchor an entry or revision", testCase.Name, step.Do)
			}
			continue
		}
		if relation.Anchor.Entry == "" {
			t.Fatalf("relationship-navigation scenario %s action %s has an exact anchor without an entry reference", testCase.Name, step.Do)
		}
		switch relation.Anchor.Revision {
		case relationshipNavigationRevisionLiteral:
			if relation.Anchor.RevisionRef == "" {
				t.Fatalf("relationship-navigation scenario %s action %s has a literal anchor without a revision reference", testCase.Name, step.Do)
			}
		case relationshipNavigationRevisionCurrentPublic:
			if relation.Anchor.RevisionRef != "" {
				t.Fatalf("relationship-navigation scenario %s action %s pins the current public revision and a revision reference", testCase.Name, step.Do)
			}
			publicBefore := false
			for _, decl := range byLocalID[relation.Target] {
				if decl.owner == relationshipNavigationOwnerSelf && decl.action == relationshipNavigationActionPublish && decl.index < index {
					publicBefore = true
				}
			}
			if !publicBefore {
				t.Fatalf("relationship-navigation scenario %s action %s anchors the current public revision before its owner published the target %q", testCase.Name, step.Do, relation.Target)
			}
		default:
			t.Fatalf("relationship-navigation scenario %s action %s has unknown anchor revision mode %q", testCase.Name, step.Do, relation.Anchor.Revision)
		}
	}
}

func validateRelationshipNavigationIntegrationRead(
	t *testing.T,
	testCase relationshipNavigationIntegrationCase,
	step relationshipNavigationIntegrationStep,
	byName map[string]relationshipNavigationCreateDecl,
	fixture relationshipNavigationIntegrationFixture,
) {
	t.Helper()
	if _, ok := byName[step.Transcript]; !ok {
		t.Fatalf("relationship-navigation scenario %s reads an undeclared transcript %q", testCase.Name, step.Transcript)
	}
	kinds := map[schema.SessionRelationshipKind]bool{}
	for _, want := range step.Navigation {
		if !want.Kind.IsValid() || !want.Status.IsValid() {
			t.Fatalf("relationship-navigation scenario %s expects a navigation entry outside its closed kind or status set", testCase.Name)
		}
		if kinds[want.Kind] {
			t.Fatalf("relationship-navigation scenario %s expects two %s entries; a transcript emits at most one per kind", testCase.Name, want.Kind)
		}
		kinds[want.Kind] = true
		if want.Identity == "" {
			t.Fatalf("relationship-navigation scenario %s expects %s without an identity", testCase.Name, want.Kind)
		}
		if want.Identity != relationshipNavigationIdentityNone {
			if _, ok := byName[want.Identity]; !ok {
				t.Fatalf("relationship-navigation scenario %s expects %s to name undeclared transcript %q", testCase.Name, want.Kind, want.Identity)
			}
		}
		linkable := want.Status == schema.RelationshipNavigationResolved || want.Status == schema.RelationshipNavigationGeneralLinkOnly
		if linkable != (want.Identity != relationshipNavigationIdentityNone) {
			t.Fatalf("relationship-navigation scenario %s expects status %q and identity %q, which disagree about a target", testCase.Name, want.Status, want.Identity)
		}
		if want.AnchorKind == "" {
			if want.AnchorEntry != "" || want.AnchorRevision != relationshipNavigationRevisionUnset {
				t.Fatalf("relationship-navigation scenario %s expects %s without an anchor kind but describes one", testCase.Name, want.Kind)
			}
			continue
		}
		if !want.AnchorKind.IsValid() {
			t.Fatalf("relationship-navigation scenario %s expects an unknown anchor kind %q", testCase.Name, want.AnchorKind)
		}
		if want.AnchorKind == schema.PublicSourceAnchorGeneral {
			if want.AnchorEntry != "" || want.AnchorRevision != "" {
				t.Fatalf("relationship-navigation scenario %s expects a general anchor with an entry or revision", testCase.Name)
			}
			continue
		}
		if want.AnchorEntry == "" {
			t.Fatalf("relationship-navigation scenario %s expects an exact anchor without an entry reference", testCase.Name)
		}
		if want.AnchorRevision != relationshipNavigationRevisionUnset && want.AnchorRevision != relationshipNavigationRevisionCurrentPublic {
			t.Fatalf("relationship-navigation scenario %s expects an unknown anchor revision mode %q", testCase.Name, want.AnchorRevision)
		}
	}
	for _, name := range step.Leaks.Transcripts {
		if _, ok := byName[name]; !ok {
			t.Fatalf("relationship-navigation scenario %s asserts an undeclared transcript %q does not leak", testCase.Name, name)
		}
	}
	for _, marker := range step.Leaks.Titles {
		if _, ok := fixture.Markers[marker]; !ok {
			t.Fatalf("relationship-navigation scenario %s asserts an unknown marker %q does not leak", testCase.Name, marker)
		}
	}
}

func relationshipNavigationRequireField(t *testing.T, scenario, action, field, value string) {
	t.Helper()
	if value == "" {
		t.Fatalf("relationship-navigation scenario %s action %s requires %s", scenario, action, field)
	}
}

func relationshipNavigationForbidFields(t *testing.T, scenario, action string, fields ...[2]string) {
	t.Helper()
	for _, field := range fields {
		if field[1] != "" {
			t.Fatalf("relationship-navigation scenario %s action %s must not carry %s", scenario, action, field[0])
		}
	}
}

func relationshipNavigationClosedValue(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

// relationshipNavigationTestRoutes mounts the REAL registered operations the
// metadata read uses. A nil viewer serves the route anonymously.
func relationshipNavigationTestRoutes(t *testing.T, pool *pgxpool.Pool, blobs storage.TranscriptBlobStore, viewer *AuthUser) *chi.Mux {
	t.Helper()
	h := New(&config.Config{FrontendURL: "https://example.test"}, pool, blobs)
	routes := chi.NewRouter()
	if viewer != nil {
		routes.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, viewer)))
			})
		})
	}
	routes.Post("/api/v1/transcripts/publish", h.PublishTranscript)
	routes.Get("/api/v1/transcripts/{id}", h.GetTranscript)
	routes.Get("/api/v1/transcripts/{id}/content", h.GetTranscriptContent)
	return routes
}

// relationshipNavigationRun is the per-case state the Go actions share: the
// owner-local identities that were created and the public revisions each
// publication produced.
type relationshipNavigationRun struct {
	caseName     string
	byTranscript map[string]*relationshipNavigationCreated
	byLocalID    map[string][]*relationshipNavigationCreated
}

type relationshipNavigationCreated struct {
	localID   string
	owner     string
	publicID  uuid.UUID
	revisions []string
}

func newRelationshipNavigationRun(caseName string) *relationshipNavigationRun {
	return &relationshipNavigationRun{
		caseName:     caseName,
		byTranscript: map[string]*relationshipNavigationCreated{},
		byLocalID:    map[string][]*relationshipNavigationCreated{},
	}
}

// localID derives the owner-local session id for an alias, scoped to the case so
// two cases can reuse the same readable alias without colliding on
// UNIQUE(owner_id, local_id).
func (r *relationshipNavigationRun) localID(alias string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(relationshipNavigationLocalIDNamespace+"/"+r.caseName+"/"+alias)).String()
}

func (r *relationshipNavigationRun) created(t *testing.T, name string) *relationshipNavigationCreated {
	t.Helper()
	created, ok := r.byTranscript[name]
	if !ok {
		t.Fatalf("relationship-navigation scenario %s uses transcript %q before it is created", r.caseName, name)
	}
	return created
}

// ownerLocalRevision is the current public representation revision of the child
// owner's own target row, the authority an exact anchor is checked against.
func (r *relationshipNavigationRun) ownerLocalRevision(t *testing.T, alias string) string {
	t.Helper()
	candidates := r.byLocalID[alias]
	for index := len(candidates) - 1; index >= 0; index-- {
		candidate := candidates[index]
		if candidate.owner == relationshipNavigationOwnerSelf && len(candidate.revisions) > 0 {
			return candidate.revisions[len(candidate.revisions)-1]
		}
	}
	t.Fatalf("relationship-navigation scenario %s has no published owner-local revision for target %q", r.caseName, alias)
	return ""
}

func (r *relationshipNavigationRun) relationships(t *testing.T, specs []relationshipNavigationIntegrationRelationship) []schema.SessionRelationship {
	t.Helper()
	if len(specs) == 0 {
		return nil
	}
	relations := make([]schema.SessionRelationship, 0, len(specs))
	for _, spec := range specs {
		relation := schema.SessionRelationship{Kind: spec.Kind, TargetState: spec.State, Evidence: spec.Evidence}
		if spec.Target != "" {
			target := schema.SessionID(r.localID(spec.Target))
			relation.TargetLocalID = &target
		}
		if spec.Anchor != nil {
			anchor := &schema.PublicSourceAnchor{Kind: spec.Anchor.Kind, SourceEntryRef: schema.SourceEntryRef(spec.Anchor.Entry)}
			switch spec.Anchor.Revision {
			case relationshipNavigationRevisionLiteral:
				anchor.SourceRevisionRef = schema.PublicRevisionRef(spec.Anchor.RevisionRef)
			case relationshipNavigationRevisionCurrentPublic:
				anchor.SourceRevisionRef = schema.PublicRevisionRef(r.ownerLocalRevision(t, spec.Target))
			}
			relation.Anchor = anchor
		}
		if err := relation.Validate(); err != nil {
			t.Fatalf("relationship-navigation scenario %s: %v", r.caseName, err)
		}
		relations = append(relations, relation)
	}
	return relations
}

func runRelationshipNavigationIntegrationCase(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	blobs *graphPublicationBlobObserver,
	ownerRoutes *chi.Mux,
	viewerRoutes *chi.Mux,
	owner pgtype.UUID,
	other pgtype.UUID,
	fixture relationshipNavigationIntegrationFixture,
	testCase relationshipNavigationIntegrationCase,
) {
	t.Helper()
	run := newRelationshipNavigationRun(testCase.Name)
	captured := map[string][]byte{}
	for _, step := range testCase.Steps {
		switch step.Do {
		case relationshipNavigationActionPublish:
			relationships := run.relationships(t, step.Relationships)
			var writesBefore int64
			if step.Writes != nil {
				writesBefore = blobs.writes.Load()
			}
			id, _ := publishRelationshipNavigationTranscript(t, ownerRoutes, run.localID(step.LocalID), fixture.Markers[step.Turn], relationships)
			if step.Writes != nil {
				if got := blobs.writes.Load() - writesBefore; got != int64(*step.Writes) {
					t.Fatalf("relationship-navigation scenario %s: publishing %s wrote %d objects, want %d; a publication rewrote durable content it does not own", testCase.Name, step.Transcript, got, *step.Writes)
				}
			}
			created, ok := run.byTranscript[step.Transcript]
			if !ok {
				created = &relationshipNavigationCreated{localID: run.localID(step.LocalID), owner: relationshipNavigationOwnerSelf}
				run.byTranscript[step.Transcript] = created
				run.byLocalID[step.LocalID] = append(run.byLocalID[step.LocalID], created)
			}
			created.publicID = id
			created.revisions = append(created.revisions, readStoredContentHash(t, ctx, pool, id))
		case relationshipNavigationActionInsert:
			relationships := run.relationships(t, step.Relationships)
			insertOwner := owner
			if step.Owner == relationshipNavigationOwnerOther {
				insertOwner = other
			}
			id := insertRelationshipNavigationTranscript(t, ctx, pool, insertOwner, run.localID(step.LocalID), fixture.Markers[step.Title], step.Visibility, relationships)
			created := &relationshipNavigationCreated{localID: run.localID(step.LocalID), owner: step.Owner, publicID: id}
			run.byTranscript[step.Transcript] = created
			run.byLocalID[step.LocalID] = append(run.byLocalID[step.LocalID], created)
		case relationshipNavigationActionRead:
			routes := ownerRoutes
			if step.Viewer == relationshipNavigationViewerViewer {
				routes = viewerRoutes
			}
			id := run.created(t, step.Transcript).publicID
			navigation, body, present := readRelationshipNavigation(t, routes, id)
			if step.Absent {
				if present {
					t.Fatalf("relationship-navigation scenario %s: expected no relationshipNavigation field, got %s", testCase.Name, body)
				}
			} else {
				assertRelationshipNavigation(t, run, step, navigation)
			}
			for _, name := range step.Leaks.Transcripts {
				assertNoLeak(t, body, run.created(t, name).publicID.String())
			}
			for _, marker := range step.Leaks.Titles {
				assertNoLeak(t, body, fixture.Markers[marker])
			}
		case relationshipNavigationActionCaptureContent:
			captured[step.Transcript] = readTranscriptContentBytes(t, ownerRoutes, run.created(t, step.Transcript).publicID)
		case relationshipNavigationActionExpectContentSame:
			before, ok := captured[step.Transcript]
			if !ok {
				t.Fatalf("relationship-navigation scenario %s: content of %s was never captured", testCase.Name, step.Transcript)
			}
			if got := readTranscriptContentBytes(t, ownerRoutes, run.created(t, step.Transcript).publicID); !bytes.Equal(before, got) {
				t.Fatalf("relationship-navigation scenario %s: a later publication changed the durable content of %s", testCase.Name, step.Transcript)
			}
		case relationshipNavigationActionExpectRevisionChanged:
			created := run.created(t, step.Transcript)
			if len(created.revisions) < 2 || created.revisions[len(created.revisions)-1] == created.revisions[len(created.revisions)-2] {
				t.Fatalf("relationship-navigation scenario %s: republishing %s did not change its public revision", testCase.Name, step.Transcript)
			}
		default:
			t.Fatalf("relationship-navigation scenario %s has unknown action %q", testCase.Name, step.Do)
		}
	}
}

func assertRelationshipNavigation(t *testing.T, run *relationshipNavigationRun, step relationshipNavigationIntegrationStep, navigation []schema.SessionRelationshipNavigation) {
	t.Helper()
	if len(navigation) != len(step.Navigation) {
		t.Fatalf("relationship-navigation scenario %s: expected %d navigation entries, got %d: %+v", run.caseName, len(step.Navigation), len(navigation), navigation)
	}
	byKind := map[schema.SessionRelationshipKind]schema.SessionRelationshipNavigation{}
	for _, entry := range navigation {
		byKind[entry.Kind] = entry
	}
	for _, want := range step.Navigation {
		got, ok := byKind[want.Kind]
		if !ok {
			t.Fatalf("relationship-navigation scenario %s: expected a %s navigation entry, got %+v", run.caseName, want.Kind, navigation)
		}
		if got.Status != want.Status {
			t.Fatalf("relationship-navigation scenario %s: %s status = %q, want %q", run.caseName, want.Kind, got.Status, want.Status)
		}
		if want.Identity == relationshipNavigationIdentityNone {
			if got.TranscriptID != nil || got.LocalID != nil {
				t.Fatalf("relationship-navigation scenario %s: %s with status %q must carry no target identity: %+v", run.caseName, want.Kind, want.Status, got)
			}
		} else {
			created := run.created(t, want.Identity)
			if got.TranscriptID == nil || *got.TranscriptID != schema.TranscriptID(created.publicID.String()) {
				t.Fatalf("relationship-navigation scenario %s: %s must name the public target %s: %+v", run.caseName, want.Kind, created.publicID, got)
			}
		}
		if want.AnchorKind == "" {
			if got.Anchor != nil {
				t.Fatalf("relationship-navigation scenario %s: %s must carry no anchor: %+v", run.caseName, want.Kind, got)
			}
			continue
		}
		if got.Anchor == nil || got.Anchor.Kind != want.AnchorKind {
			t.Fatalf("relationship-navigation scenario %s: %s anchor = %+v, want kind %q", run.caseName, want.Kind, got.Anchor, want.AnchorKind)
		}
		if want.AnchorEntry != "" && string(got.Anchor.SourceEntryRef) != want.AnchorEntry {
			t.Fatalf("relationship-navigation scenario %s: %s anchor entry = %q, want %q", run.caseName, want.Kind, got.Anchor.SourceEntryRef, want.AnchorEntry)
		}
		if want.AnchorRevision == relationshipNavigationRevisionCurrentPublic {
			created := run.created(t, want.Identity)
			if len(created.revisions) == 0 {
				t.Fatalf("relationship-navigation scenario %s: %s anchor revision cannot be compared because %s has no captured revision", run.caseName, want.Kind, want.Identity)
			}
			if current := created.revisions[len(created.revisions)-1]; string(got.Anchor.SourceRevisionRef) != current {
				t.Fatalf("relationship-navigation scenario %s: %s anchor revision = %q, want the current public revision %q", run.caseName, want.Kind, got.Anchor.SourceRevisionRef, current)
			}
		}
	}
}

// publishRelationshipNavigationTranscript publishes one owner/local identity
// through the real route and returns its public transcript id and the exact
// content bytes that were uploaded.
func publishRelationshipNavigationTranscript(t *testing.T, routes *chi.Mux, localID, turnContent string, relationships []schema.SessionRelationship) (uuid.UUID, []byte) {
	t.Helper()
	content := relationshipNavigationContent(t, localID, turnContent, relationships)
	detail, err := decodePublicationDetail(content)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(schema.AuthoritativePublishRequest{
		Identity:    schema.AuthoritativeSessionIdentity{SessionID: schema.SessionID(detail.ID), SchemaVersion: 11, RootSessionID: detail.RootSessionID, Purpose: detail.Purpose, Relationships: detail.Relationships},
		Model:       schema.AuthoritativeModelInfo{Harness: detail.Harness, Model: "fixture-model"},
		Timestamp:   schema.AuthoritativeTimestampInfo{Start: 1700000000000, End: 1700000001000},
		Source:      schema.AuthoritativeSourceInfo{Format: schema.SourceFormatJSON},
		Project:     schema.AuthoritativeProjectContext{Hash: testProjectHash, Name: "relationship-nav-fixture"},
		Stats:       schema.AuthoritativeSessionStats{TurnCount: detail.TurnCount, InputSubmissionCount: detail.InputSubmissionCount},
		ContentHash: schema.ComputeTranscriptContentHash(content),
	})
	if err != nil {
		t.Fatal(err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadata)}, string(content))
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, r)
	if w.Code != http.StatusCreated && w.Code != http.StatusOK {
		t.Fatalf("publish %s: status=%d body=%s", localID, w.Code, w.Body.String())
	}
	var response schema.AuthoritativePublishResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	id, err := uuid.Parse(string(response.TranscriptID))
	if err != nil {
		t.Fatal(err)
	}
	return id, content
}

func relationshipNavigationContent(t *testing.T, localID, turnContent string, relationships []schema.SessionRelationship) []byte {
	t.Helper()
	encodedRelationships, err := json.Marshal(relationships)
	if err != nil {
		t.Fatal(err)
	}
	detail := map[string]any{
		"id":            localID,
		"harness":       "codex",
		"turnCount":     1,
		"startTime":     "2023-11-14T22:13:20Z",
		"endTime":       "2023-11-14T22:14:20Z",
		"durationMins":  1,
		"tokensIn":      10,
		"tokensOut":     5,
		"totalTokens":   15,
		"toolCallCount": 0,
		"relationships": json.RawMessage(encodedRelationships),
		"turns": []any{map[string]any{
			"index": 0, "role": "user", "entryType": "text",
			"content": turnContent, "timestamp": "2023-11-14T22:13:21Z", "depth": 0,
		}},
	}
	envelope, err := json.Marshal(map[string]any{"contractVersion": "0.1.0", "kind": "session_detail", "sessionDetail": detail})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

// readRelationshipNavigation performs the real metadata read and returns the
// navigation entries, the raw body (for leak assertions), and whether the
// response carried the field at all.
func readRelationshipNavigation(t *testing.T, routes *chi.Mux, id uuid.UUID) ([]schema.SessionRelationshipNavigation, string, bool) {
	t.Helper()
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id.String(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("metadata read: status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatal(err)
	}
	raw, present := fields["relationshipNavigation"]
	if !present {
		return nil, body, false
	}
	var navigation []schema.SessionRelationshipNavigation
	if err := json.Unmarshal(raw, &navigation); err != nil {
		t.Fatal(err)
	}
	return navigation, body, true
}

func readTranscriptContentBytes(t *testing.T, routes *chi.Mux, id uuid.UUID) []byte {
	t.Helper()
	w := httptest.NewRecorder()
	routes.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id.String()+"/content", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("content read: status=%d body=%s", w.Code, w.Body.String())
	}
	return append([]byte(nil), w.Body.Bytes()...)
}

func insertRelationshipNavigationTranscript(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID, localID, title, visibility string, relationships []schema.SessionRelationship) uuid.UUID {
	t.Helper()
	id := pullInsertTranscript(t, ctx, pool, owner, localID, visibility)
	encoded, err := json.Marshal(relationships)
	if err != nil {
		t.Fatal(err)
	}
	if relationships == nil {
		encoded = []byte("[]")
	}
	execAsSystem(t, ctx, pool, "UPDATE transcripts SET title=$1, session_relationships=$2::jsonb WHERE id=$3", title, string(encoded), id)
	return uuidFromPg(id)
}

func TestRelationshipNavigationMountedMetadataIntegration(t *testing.T) {
	fixture := loadRelationshipNavigationIntegrationFixture(t)
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	ownerA := pullInsertUser(t, ctx, pool, 77665501, "relationship-nav-owner-a")
	ownerB := pullInsertUser(t, ctx, pool, 77665502, "relationship-nav-owner-b")
	viewer := pullInsertUser(t, ctx, pool, 77665503, "relationship-nav-viewer")
	defer cleanupOwners(t, ctx, pool, ownerA, ownerB, viewer)
	blobs := &graphPublicationBlobObserver{TranscriptBlobStore: authoritativeTestBlobStore(t)}
	ownerAUser := &AuthUser{ID: uuidFromPg(ownerA), Username: "relationship-nav-owner-a"}
	viewerUser := &AuthUser{ID: uuidFromPg(viewer), Username: "relationship-nav-viewer"}
	ownerARoutes := relationshipNavigationTestRoutes(t, pool, blobs, ownerAUser)
	viewerRoutes := relationshipNavigationTestRoutes(t, pool, blobs, viewerUser)

	covered := map[string]bool{}
	for _, testCase := range fixture.Cases {
		testCase := testCase
		t.Run(testCase.Name, func(t *testing.T) {
			covered[testCase.Name] = true
			runRelationshipNavigationIntegrationCase(t, ctx, pool, blobs, ownerARoutes, viewerRoutes, ownerA, ownerB, fixture, testCase)
		})
	}
	for _, name := range fixture.Required {
		if !covered[name] {
			t.Errorf("required relationship-navigation scenario %s did not run", name)
		}
	}
}

func assertNoLeak(t *testing.T, body, forbidden string) {
	t.Helper()
	if forbidden == "" {
		return
	}
	if strings.Contains(body, forbidden) {
		t.Fatalf("metadata response leaked a withheld target identifier %q: %s", forbidden, body)
	}
}

func readStoredContentHash(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var hash pgtype.Text
	if err := pool.QueryRow(ctx, "SELECT content_hash FROM transcripts WHERE id=$1", toPgUUID(id)).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !hash.Valid || hash.String == "" {
		t.Fatalf("transcript %s has no public content hash", id)
	}
	return hash.String
}
