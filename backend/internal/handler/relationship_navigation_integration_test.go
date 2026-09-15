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

type relationshipNavigationIntegrationFixture struct {
	Required    []string `yaml:"required_names"`
	ParentTitle string   `yaml:"parent_title"`
	ChildTitle  string   `yaml:"child_title"`
}

func loadRelationshipNavigationIntegrationFixture(t *testing.T) relationshipNavigationIntegrationFixture {
	t.Helper()
	var f relationshipNavigationIntegrationFixture
	d := yaml.NewDecoder(bytes.NewReader(relationshipNavigationIntegrationYAML))
	d.KnownFields(true)
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if len(f.Required) == 0 || f.ParentTitle == "" || f.ChildTitle == "" {
		t.Fatal("relationship-navigation integration fixture is incomplete")
	}
	return f
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

type relationshipNavigationTarget struct {
	Relationships []schema.SessionRelationship
	TurnContent   string
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
	cover := func(name string) {
		if covered[name] {
			t.Fatalf("scenario %s ran twice", name)
		}
		covered[name] = true
	}

	t.Run("child-first-parent-later", func(t *testing.T) {
		cover("child-first-parent-later")
		parentLocalID := uuid.NewString()
		childLocalID := uuid.NewString()
		startedBy := schema.SessionRelationship{
			Kind: schema.SessionRelationshipStartedBy, TargetState: schema.RelationshipTargetKnown,
			TargetLocalID: sessionIDPtr(parentLocalID), Evidence: schema.EvidenceNativeTyped,
		}
		contextFrom := schema.SessionRelationship{
			Kind: schema.SessionRelationshipContextFrom, TargetState: schema.RelationshipTargetKnownRetained,
			TargetLocalID: sessionIDPtr(parentLocalID), Evidence: schema.EvidenceRetainedLastGood,
			Anchor: &schema.PublicSourceAnchor{Kind: schema.PublicSourceAnchorThrough, SourceEntryRef: "e_parent", SourceRevisionRef: "rev-before-parent-publish"},
		}
		childID, _ := publishRelationshipNavigationTranscript(t, ownerARoutes, childLocalID, fixture.ChildTitle, []schema.SessionRelationship{startedBy, contextFrom})
		before, _, present := readRelationshipNavigation(t, ownerARoutes, childID)
		if !present || len(before) != 2 {
			t.Fatalf("child before the parent is published must expose both owned relationships: %+v", before)
		}
		for _, navigation := range before {
			if navigation.Status != schema.RelationshipNavigationKnownUnavailable || navigation.TranscriptID != nil {
				t.Fatalf("child before the parent is published leaked a target: %+v", navigation)
			}
		}
		contentBefore := readTranscriptContentBytes(t, ownerARoutes, childID)
		writesBefore := blobs.writes.Load()
		parentID, _ := publishRelationshipNavigationTranscript(t, ownerARoutes, parentLocalID, fixture.ParentTitle, nil)
		if blobs.writes.Load() != writesBefore+1 {
			t.Fatal("publishing the parent rewrote the child's encrypted object")
		}
		after, _, present := readRelationshipNavigation(t, ownerARoutes, childID)
		if !present || len(after) != 2 {
			t.Fatalf("child after the parent is published: %+v", after)
		}
		startedByNav, contextNav := navigationByKind(after)
		if startedByNav == nil || startedByNav.Status != schema.RelationshipNavigationResolved || startedByNav.TranscriptID == nil || *startedByNav.TranscriptID != schema.TranscriptID(parentID.String()) {
			t.Fatalf("started_by did not resolve to the current parent: %+v", startedByNav)
		}
		// The child captured no public revision of the parent, so the context
		// link stays general and must not claim the stale anchor.
		if contextNav == nil || contextNav.Status != schema.RelationshipNavigationGeneralLinkOnly || contextNav.Anchor == nil || contextNav.Anchor.Kind != schema.PublicSourceAnchorGeneral {
			t.Fatalf("context_from without a matching public revision must stay general: %+v", contextNav)
		}
		if got := readTranscriptContentBytes(t, ownerARoutes, childID); !bytes.Equal(contentBefore, got) {
			t.Fatal("later parent publication changed the child's durable content")
		}
	})

	t.Run("matching-public-revision", func(t *testing.T) {
		cover("matching-public-revision")
		parentLocalID := uuid.NewString()
		childLocalID := uuid.NewString()
		parentID, _ := publishRelationshipNavigationTranscript(t, ownerARoutes, parentLocalID, fixture.ParentTitle, nil)
		parentHash := readStoredContentHash(t, ctx, pool, parentID)
		contextFrom := schema.SessionRelationship{
			Kind: schema.SessionRelationshipContextFrom, TargetState: schema.RelationshipTargetKnownRetained,
			TargetLocalID: sessionIDPtr(parentLocalID), Evidence: schema.EvidenceRetainedLastGood,
			Anchor: &schema.PublicSourceAnchor{Kind: schema.PublicSourceAnchorThrough, SourceEntryRef: "e_parent", SourceRevisionRef: schema.PublicRevisionRef(parentHash)},
		}
		childID, _ := publishRelationshipNavigationTranscript(t, ownerARoutes, childLocalID, fixture.ChildTitle, []schema.SessionRelationship{contextFrom})
		after, _, present := readRelationshipNavigation(t, ownerARoutes, childID)
		if !present || len(after) != 1 {
			t.Fatalf("matching-revision child navigation: %+v", after)
		}
		if after[0].Status != schema.RelationshipNavigationResolved || after[0].Anchor == nil || after[0].Anchor.Kind != schema.PublicSourceAnchorThrough || after[0].Anchor.SourceRevisionRef != schema.PublicRevisionRef(parentHash) {
			t.Fatalf("context_from with a matching public revision must keep the exact anchor: %+v", after[0])
		}
	})

	t.Run("changed-public-revision", func(t *testing.T) {
		cover("changed-public-revision")
		parentLocalID := uuid.NewString()
		childLocalID := uuid.NewString()
		parentID, _ := publishRelationshipNavigationTranscript(t, ownerARoutes, parentLocalID, fixture.ParentTitle, nil)
		parentHash := readStoredContentHash(t, ctx, pool, parentID)
		contextFrom := schema.SessionRelationship{
			Kind: schema.SessionRelationshipContextFrom, TargetState: schema.RelationshipTargetKnownRetained,
			TargetLocalID: sessionIDPtr(parentLocalID), Evidence: schema.EvidenceRetainedLastGood,
			Anchor: &schema.PublicSourceAnchor{Kind: schema.PublicSourceAnchorBefore, SourceEntryRef: "e_parent", SourceRevisionRef: schema.PublicRevisionRef(parentHash)},
		}
		childID, childContent := publishRelationshipNavigationTranscript(t, ownerARoutes, childLocalID, fixture.ChildTitle, []schema.SessionRelationship{contextFrom})
		if _, _, present := readRelationshipNavigation(t, ownerARoutes, childID); !present {
			t.Fatal("matching child navigation missing before the parent revision changed")
		}
		// Republishing the parent with different bytes changes its public
		// revision; the child's stored anchor no longer identifies it.
		publishRelationshipNavigationTranscript(t, ownerARoutes, parentLocalID, fixture.ParentTitle+" revised", nil)
		if readStoredContentHash(t, ctx, pool, parentID) == parentHash {
			t.Fatal("parent republish did not change the public revision")
		}
		after, _, present := readRelationshipNavigation(t, ownerARoutes, childID)
		if !present || len(after) != 1 {
			t.Fatalf("changed-revision child navigation: %+v", after)
		}
		if after[0].Status != schema.RelationshipNavigationGeneralLinkOnly || after[0].Anchor == nil || after[0].Anchor.Kind != schema.PublicSourceAnchorGeneral || after[0].TranscriptID == nil || *after[0].TranscriptID != schema.TranscriptID(parentID.String()) {
			t.Fatalf("a changed public revision must downgrade to a general link to the current parent: %+v", after[0])
		}
		if got := readTranscriptContentBytes(t, ownerARoutes, childID); !bytes.Equal(childContent, got) {
			t.Fatal("parent republish changed the child's durable content")
		}
	})

	t.Run("missing-target-no-leak", func(t *testing.T) {
		cover("missing-target-no-leak")
		childLocalID := uuid.NewString()
		startedBy := schema.SessionRelationship{
			Kind: schema.SessionRelationshipStartedBy, TargetState: schema.RelationshipTargetKnown,
			TargetLocalID: sessionIDPtr(uuid.NewString()), Evidence: schema.EvidenceNativeTyped,
		}
		childID, _ := publishRelationshipNavigationTranscript(t, ownerARoutes, childLocalID, fixture.ChildTitle, []schema.SessionRelationship{startedBy})
		navigation, body, present := readRelationshipNavigation(t, ownerARoutes, childID)
		if !present || len(navigation) != 1 || navigation[0].Status != schema.RelationshipNavigationKnownUnavailable || navigation[0].TranscriptID != nil {
			t.Fatalf("absent target must be known_unavailable with no public id: %+v", navigation)
		}
		assertNoLeak(t, body, fixture.ParentTitle)
	})

	t.Run("same-owner-inaccessible", func(t *testing.T) {
		cover("same-owner-inaccessible")
		parentLocalID := uuid.NewString()
		childLocalID := uuid.NewString()
		parentID := insertRelationshipNavigationTranscript(t, ctx, pool, ownerA, parentLocalID, fixture.ParentTitle, "private", nil)
		startedBy := schema.SessionRelationship{
			Kind: schema.SessionRelationshipStartedBy, TargetState: schema.RelationshipTargetKnown,
			TargetLocalID: sessionIDPtr(parentLocalID), Evidence: schema.EvidenceNativeTyped,
		}
		childID := insertRelationshipNavigationTranscript(t, ctx, pool, ownerA, childLocalID, fixture.ChildTitle, "public", []schema.SessionRelationship{startedBy})
		navigation, body, present := readRelationshipNavigation(t, viewerRoutes, childID)
		if !present || len(navigation) != 1 || navigation[0].Status != schema.RelationshipNavigationInaccessible || navigation[0].TranscriptID != nil {
			t.Fatalf("same-owner unreadable target must be inaccessible with no public id: %+v", navigation)
		}
		assertNoLeak(t, body, fixture.ParentTitle)
		assertNoLeak(t, body, parentID.String())
	})

	t.Run("cross-owner-collision", func(t *testing.T) {
		cover("cross-owner-collision")
		sharedLocalID := uuid.NewString()
		childLocalID := uuid.NewString()
		// Another owner holds the same local id; the child's owner does not.
		otherID := insertRelationshipNavigationTranscript(t, ctx, pool, ownerB, sharedLocalID, fixture.ParentTitle, "public", nil)
		startedBy := schema.SessionRelationship{
			Kind: schema.SessionRelationshipStartedBy, TargetState: schema.RelationshipTargetKnown,
			TargetLocalID: sessionIDPtr(sharedLocalID), Evidence: schema.EvidenceNativeTyped,
		}
		childID := insertRelationshipNavigationTranscript(t, ctx, pool, ownerA, childLocalID, fixture.ChildTitle, "public", []schema.SessionRelationship{startedBy})
		navigation, body, present := readRelationshipNavigation(t, ownerARoutes, childID)
		if !present || len(navigation) != 1 || navigation[0].Status != schema.RelationshipNavigationKnownUnavailable || navigation[0].TranscriptID != nil {
			t.Fatalf("another owner's same-local-id transcript must not resolve: %+v", navigation)
		}
		assertNoLeak(t, body, otherID.String())
		assertNoLeak(t, body, fixture.ParentTitle)
	})

	t.Run("owner-match-beats-collision", func(t *testing.T) {
		cover("owner-match-beats-collision")
		sharedLocalID := uuid.NewString()
		childLocalID := uuid.NewString()
		ownerBID := insertRelationshipNavigationTranscript(t, ctx, pool, ownerB, sharedLocalID, fixture.ParentTitle, "public", nil)
		ownerAID := insertRelationshipNavigationTranscript(t, ctx, pool, ownerA, sharedLocalID, fixture.ParentTitle, "public", nil)
		startedBy := schema.SessionRelationship{
			Kind: schema.SessionRelationshipStartedBy, TargetState: schema.RelationshipTargetKnown,
			TargetLocalID: sessionIDPtr(sharedLocalID), Evidence: schema.EvidenceNativeTyped,
		}
		childID := insertRelationshipNavigationTranscript(t, ctx, pool, ownerA, childLocalID, fixture.ChildTitle, "public", []schema.SessionRelationship{startedBy})
		navigation, body, present := readRelationshipNavigation(t, ownerARoutes, childID)
		if !present || len(navigation) != 1 || navigation[0].Status != schema.RelationshipNavigationResolved || navigation[0].TranscriptID == nil || *navigation[0].TranscriptID != schema.TranscriptID(ownerAID.String()) {
			t.Fatalf("the child owner's own target must win the collision: %+v", navigation)
		}
		assertNoLeak(t, body, ownerBID.String())
	})

	t.Run("explicit-none", func(t *testing.T) {
		cover("explicit-none")
		childLocalID := uuid.NewString()
		explicitNone := schema.SessionRelationship{
			Kind: schema.SessionRelationshipStartedBy, TargetState: schema.RelationshipTargetExplicitNone, Evidence: schema.EvidenceNativeTyped,
		}
		childID := insertRelationshipNavigationTranscript(t, ctx, pool, ownerA, childLocalID, fixture.ChildTitle, "public", []schema.SessionRelationship{explicitNone})
		_, body, present := readRelationshipNavigation(t, ownerARoutes, childID)
		if present {
			t.Fatalf("explicit-none must leave no navigation link: %s", body)
		}
	})

	for _, name := range fixture.Required {
		if !covered[name] {
			t.Errorf("required relationship-navigation scenario %s did not run", name)
		}
	}
}

func navigationByKind(navigation []schema.SessionRelationshipNavigation) (*schema.SessionRelationshipNavigation, *schema.SessionRelationshipNavigation) {
	var startedBy, contextFrom *schema.SessionRelationshipNavigation
	for i := range navigation {
		switch navigation[i].Kind {
		case schema.SessionRelationshipStartedBy:
			startedBy = &navigation[i]
		case schema.SessionRelationshipContextFrom:
			contextFrom = &navigation[i]
		}
	}
	return startedBy, contextFrom
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
