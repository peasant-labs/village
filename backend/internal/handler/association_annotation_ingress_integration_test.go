//go:build integration

package handler

// Real-Postgres coverage for the association ingress production path. It drives
// the handler transaction, sqlc queries, migration constraints, and annotation
// HTTP endpoint together; mocked tests cannot prove owner-scoped FKs or rollback.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/association_annotation_ingress/ledger_cases.yaml
var associationLedgerCasesYAML []byte

type associationLedgerCase struct {
	Name                   string                         `yaml:"name"`
	AssociationID          string                         `yaml:"associationId"`
	ObservedCommitHash     string                         `yaml:"observedCommitHash"`
	AdditionalAssociations []associationLedgerAssociation `yaml:"additionalAssociations"`
}

type associationLedgerAssociation struct {
	AssociationID      string `yaml:"associationId"`
	ObservedCommitHash string `yaml:"observedCommitHash"`
}

type associationLedgerFixture struct {
	ExpectedCaseCount int                     `yaml:"expectedCaseCount"`
	RequiredCaseNames []string                `yaml:"requiredCaseNames"`
	Cases             []associationLedgerCase `yaml:"cases"`
}

func loadAssociationLedgerFixture(t *testing.T) map[string]associationLedgerCase {
	t.Helper()
	fixture, err := decodeAssociationLedgerFixture(associationLedgerCasesYAML)
	if err != nil {
		t.Fatalf("decode association ledger fixture: %v", err)
	}
	if len(fixture.Cases) != fixture.ExpectedCaseCount {
		t.Fatalf("association ledger fixture has %d cases, want declared %d", len(fixture.Cases), fixture.ExpectedCaseCount)
	}
	cases := make(map[string]associationLedgerCase, len(fixture.Cases))
	for _, c := range fixture.Cases {
		if c.Name == "" || c.AssociationID == "" || c.ObservedCommitHash == "" {
			t.Fatalf("association ledger fixture has incomplete case %+v", c)
		}
		if _, duplicate := cases[c.Name]; duplicate {
			t.Fatalf("association ledger fixture repeats case %q", c.Name)
		}
		for _, additional := range c.AdditionalAssociations {
			if additional.AssociationID == "" || additional.ObservedCommitHash == "" {
				t.Fatalf("association ledger fixture case %q has incomplete additional association %+v", c.Name, additional)
			}
		}
		cases[c.Name] = c
	}
	for _, name := range fixture.RequiredCaseNames {
		if _, exists := cases[name]; !exists {
			t.Fatalf("association ledger fixture omits required case %q", name)
		}
	}
	return cases
}

func decodeAssociationLedgerFixture(data []byte) (associationLedgerFixture, error) {
	var fixture associationLedgerFixture
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&fixture); err != nil {
		return associationLedgerFixture{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return associationLedgerFixture{}, errors.New("fixture contains a trailing YAML document")
		}
		return associationLedgerFixture{}, err
	}
	return fixture, nil
}

func TestAssociationLedgerFixtureRejectsTrailingDocument(t *testing.T) {
	_, err := decodeAssociationLedgerFixture(append(append([]byte{}, associationLedgerCasesYAML...), []byte("\n---\nunexpected: document\n")...))
	if err == nil || !strings.Contains(err.Error(), "trailing YAML document") {
		t.Fatalf("trailing fixture document error = %v, want explicit rejection", err)
	}
}

func requireAssociationLedgerCase(t *testing.T, cases map[string]associationLedgerCase, name string) associationLedgerCase {
	t.Helper()
	c, exists := cases[name]
	if !exists {
		t.Fatalf("association ledger fixture has no %q case", name)
	}
	return c
}

func callAssociationAnnotationPush(t *testing.T, h *Handler, ownerID uuid.UUID, req schema.AnnotationPushRequest) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal association annotation push: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/annotations", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: ownerID, Username: "association-owner"}))
	w := httptest.NewRecorder()
	h.UploadAnnotations(w, r)
	return w
}

func associationAnnotationManifest(t *testing.T, h *Handler, ownerID uuid.UUID) schema.AnnotationManifestResponse {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/annotations/manifest", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: ownerID, Username: "association-owner"}))
	w := httptest.NewRecorder()
	h.GetAnnotationManifest(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("annotation manifest status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var manifest schema.AnnotationManifestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("decode annotation manifest: %v", err)
	}
	return manifest
}

func requireSingleManifestHash(t *testing.T, manifest schema.AnnotationManifestResponse, want string) {
	t.Helper()
	if len(manifest.Hashes) != 1 || manifest.Hashes[0] != want {
		t.Fatalf("owner-scoped manifest hashes=%v, want the shared computed hash only", manifest.Hashes)
	}
}

func requireManifestContains(t *testing.T, manifest schema.AnnotationManifestResponse, want string) {
	t.Helper()
	for _, hash := range manifest.Hashes {
		if hash == want {
			return
		}
	}
	t.Fatalf("owner-scoped manifest hashes=%v do not contain %q", manifest.Hashes, want)
}

func associationPushItem(contentHash, associationID string) schema.AnnotationPushItem {
	id := schema.AssociationID(associationID)
	return schema.AnnotationPushItem{
		ContentHash:         contentHash,
		TargetKind:          schema.TargetAssociation,
		TargetAssociationID: &id,
		TypeID:              "quality.session_outcome",
		Value:               "resolved",
	}
}

func computedAssociationPushItem(associationID string) schema.AnnotationPushItem {
	item := associationPushItem("", associationID)
	item.ContentHash = item.ComputeContentHash()
	return item
}

type recordingTranscriptBlobStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func newRecordingTranscriptBlobStore() *recordingTranscriptBlobStore {
	return &recordingTranscriptBlobStore{blobs: make(map[string][]byte)}
}

func (s *recordingTranscriptBlobStore) Write(_ context.Context, _ uuid.UUID, contents []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	key := "transcripts/" + uuid.NewString() + ".bin"
	descriptor, err := storage.NewBlobDescriptor(storage.ObjectKey(key), []byte("test-wrapped-key"), storage.EncryptionAES256GCMRandomNonceV1, 1)
	if err != nil {
		return storage.BlobDescriptor{}, storage.ContentIdentity{}, err
	}
	s.mu.Lock()
	s.blobs[key] = append([]byte(nil), contents...)
	s.mu.Unlock()
	identity, err := storage.NewContentIdentity(schema.ComputeTranscriptHash(contents), int64(len(contents)))
	return descriptor, identity, err
}

func (s *recordingTranscriptBlobStore) Read(_ context.Context, _ uuid.UUID, descriptor storage.BlobDescriptor, loaded storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := string(descriptor.ObjectKey())
	contents, exists := s.blobs[key]
	if !exists {
		return nil, storage.ContentIdentity{}, fmt.Errorf("recording transcript blob %q is missing", key)
	}
	content := append([]byte(nil), contents...)
	if known, ok := loaded.Known(); ok {
		return content, known, nil
	}
	identity, err := storage.NewContentIdentity(schema.ComputeTranscriptHash(content), int64(len(content)))
	return content, identity, err
}

func (*recordingTranscriptBlobStore) Rewrap(context.Context, uuid.UUID, storage.BlobDescriptor) (storage.BlobDescriptor, error) {
	return storage.BlobDescriptor{}, errors.New("recording transcript blob rewrap not configured")
}

func (s *recordingTranscriptBlobStore) Delete(_ context.Context, descriptor storage.BlobDescriptor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blobs, string(descriptor.ObjectKey()))
	return nil
}

func (s *recordingTranscriptBlobStore) contents(t *testing.T, key string) []byte {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	contents, exists := s.blobs[key]
	if !exists {
		t.Fatalf("recording S3 blob %q is missing", key)
	}
	return append([]byte(nil), contents...)
}

func (s *recordingTranscriptBlobStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.blobs)
}

// publishAssociationTranscript drives the mounted publish endpoint so the
// ledger assertion below covers wire decode, schema enforcement, blob upload,
// actor attribution, transcript insertion, and association insertion together.
func publishAssociationTranscript(t *testing.T, ctx context.Context, h *Handler, owner pgtype.UUID, username, localID string, association schema.PublishedAssociation) (int, string) {
	return publishAssociationTranscriptBatch(t, ctx, h, owner, username, localID, []schema.PublishedAssociation{association}, `[{"role":"user","content":"association fixture"}]`)
}

func publishAssociationTranscriptContent(t *testing.T, ctx context.Context, h *Handler, owner pgtype.UUID, username, localID string, association schema.PublishedAssociation, content string) (int, string) {
	return publishAssociationTranscriptBatch(t, ctx, h, owner, username, localID, []schema.PublishedAssociation{association}, content)
}

func publishAssociationTranscriptBatch(t *testing.T, ctx context.Context, h *Handler, owner pgtype.UUID, username, localID string, associations []schema.PublishedAssociation, content string) (int, string) {
	t.Helper()
	metadata := schema.PublishRequest{
		Identity:    schema.SessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2},
		Model:       schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "association-test"},
		Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
		Source:      schema.SourceInfo{FilePath: "/fixtures/association.jsonl", Format: "jsonl"},
		Git:         schema.GitContext{Branch: strPtr("main"), Associations: associations},
		Project:     schema.ProjectContext{Hash: testProjectHash, Name: "association-fixture"},
		Stats:       schema.SessionStats{TurnCount: 1, ToolCallCount: 1, DurationMs: 1000, TokensIn: 1, TokensOut: 1},
		Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal association publish metadata: %v", err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadataJSON)}, content)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(context.WithValue(ctx, UserContextKey, &AuthUser{ID: uuid.UUID(owner.Bytes), Username: username}))
	w := httptest.NewRecorder()
	h.PublishTranscript(w, r)
	return w.Code, w.Body.String()
}

func associationFixtureUser(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, ordinal int, suffix string) pgtype.UUID {
	t.Helper()
	var owner pgtype.UUID
	githubID := time.Now().UnixNano() + int64(ordinal)
	username := fmt.Sprintf("association-ingress-%s-%d", suffix, githubID)
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES ($1, $2, $3) RETURNING id
	`, githubID, username, username).Scan(&owner); err != nil {
		t.Fatalf("insert association fixture user %q: %v", username, err)
	}
	return owner
}

func transcriptAssociationSnapshot(t *testing.T, ctx context.Context, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, s3 *recordingTranscriptBlobStore, transcriptID pgtype.UUID) string {
	t.Helper()
	var transcriptJSON, blobKey, associationsJSON, auditJSON string
	if err := pool.QueryRow(ctx, `SELECT row_to_json(t)::text, blob_key FROM transcripts t WHERE id = $1`, transcriptID).Scan(&transcriptJSON, &blobKey); err != nil {
		t.Fatalf("snapshot transcript %s: %v", uuid.UUID(transcriptID.Bytes), err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(json_agg(row_to_json(ta) ORDER BY ta.association_id)::text, '[]')
		FROM transcript_associations ta
		WHERE ta.transcript_id = $1
	`, transcriptID).Scan(&associationsJSON); err != nil {
		t.Fatalf("snapshot associations for transcript %s: %v", uuid.UUID(transcriptID.Bytes), err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(json_agg(row_to_json(a) ORDER BY a.seq)::text, '[]')
		FROM transcript_governance_events_audit a
		WHERE a.transcript_id = $1
	`, transcriptID).Scan(&auditJSON); err != nil {
		t.Fatalf("snapshot governance audit for transcript %s: %v", uuid.UUID(transcriptID.Bytes), err)
	}
	return fmt.Sprintf("%s\n%s\n%s\n%s", transcriptJSON, associationsJSON, auditJSON, s3.contents(t, blobKey))
}

func recordAssociationBinding(t *testing.T, ctx context.Context, h *Handler, ownerID, transcriptID pgtype.UUID, association schema.PublishedAssociation) {
	t.Helper()
	if err := h.inTxAs(ctx, ownerID, func(q Querier) error {
		newAssociations, err := validatePublishedAssociationBindings(ctx, q, ownerID, transcriptID, []schema.PublishedAssociation{association})
		if err != nil {
			return err
		}
		return insertPublishedAssociationBindings(ctx, q, ownerID, transcriptID, newAssociations)
	}); err != nil {
		t.Fatalf("record association %q: %v", association.ID, err)
	}
}

func listTranscriptAssociationAnnotations(t *testing.T, h *Handler, ownerID, transcriptID uuid.UUID) []schema.AnnotationSummary {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+transcriptID.String()+"/annotations", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: ownerID, Username: "association-owner"}))
	r = withChiURLParam(r, "id", transcriptID.String())
	w := httptest.NewRecorder()
	h.ListTranscriptAnnotations(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list transcript annotations status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var response struct {
		Annotations []schema.AnnotationSummary `json:"annotations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode transcript annotation list: %v", err)
	}
	return response.Annotations
}

func pullTranscriptAssociationAnnotations(t *testing.T, h *Handler, ownerID, transcriptID uuid.UUID) []schema.PullAnnotation {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+transcriptID.String()+"/annotations", nil)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &AuthUser{ID: ownerID, Username: "association-owner"}))
	r = withChiURLParam(r, "id", transcriptID.String())
	w := httptest.NewRecorder()
	h.GetPullTranscriptAnnotations(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("pull transcript annotations status: got %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var annotations []schema.PullAnnotation
	if err := json.Unmarshal(w.Body.Bytes(), &annotations); err != nil {
		t.Fatalf("decode pull annotations: %v", err)
	}
	return annotations
}

func installAssociationInsertFailureTrigger(t *testing.T, ctx context.Context, pool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}) func() {
	t.Helper()
	if _, err := pool.Exec(ctx, "DROP TRIGGER IF EXISTS trg_test_reject_forced_association_insert ON transcript_associations"); err != nil {
		t.Fatalf("clear stale forced association trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, "DROP FUNCTION IF EXISTS test_reject_forced_association_insert()"); err != nil {
		t.Fatalf("clear stale forced association trigger function: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION test_reject_forced_association_insert()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			IF NEW.association_id = 'assoc-ledger-forced-rollback' THEN
				RAISE EXCEPTION 'forced post-insert association failure';
			END IF;
			RETURN NEW;
		END;
		$$
	`); err != nil {
		t.Fatalf("install forced association trigger function: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TRIGGER trg_test_reject_forced_association_insert
		AFTER INSERT ON transcript_associations
		FOR EACH ROW EXECUTE FUNCTION test_reject_forced_association_insert()
	`); err != nil {
		t.Fatalf("install forced association trigger: %v", err)
	}
	return func() {
		if _, err := pool.Exec(context.Background(), "DROP TRIGGER IF EXISTS trg_test_reject_forced_association_insert ON transcript_associations"); err != nil {
			t.Errorf("drop forced association trigger: %v", err)
		}
		if _, err := pool.Exec(context.Background(), "DROP FUNCTION IF EXISTS test_reject_forced_association_insert()"); err != nil {
			t.Errorf("drop forced association trigger function: %v", err)
		}
	}
}

// TestAssociationAnnotationIngress_RealPostgres proves the production path's
// durable owner-scoped identity, query discoverability, exact replay, conflict
// rejection, and all-or-nothing push semantics.
func TestAssociationAnnotationIngress_RealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping association annotation integration test in -short mode")
	}
	cases := loadAssociationLedgerFixture(t)
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	s3 := newRecordingTranscriptBlobStore()
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: s3}

	ownerA := associationFixtureUser(t, ctx, pool, 1, "a")
	ownerB := associationFixtureUser(t, ctx, pool, 2, "b")
	defer cleanupOwners(t, ctx, pool, ownerA, ownerB)

	// The mounted publish contract restricts identity.sessionId to the producer
	// wire grammar; a UUID is both valid wire data and a stable local-id lookup.
	localA := uuid.NewString()
	roundTrip := requireAssociationLedgerCase(t, cases, "owner A association round trip")
	if status, body := publishAssociationTranscript(t, ctx, h, ownerA, "association-owner-a", localA, schema.PublishedAssociation{
		ID:                 schema.AssociationID(roundTrip.AssociationID),
		ObservedCommitHash: roundTrip.ObservedCommitHash,
	}); status != http.StatusCreated {
		t.Fatalf("publish owner A association transcript: got %d, want 201 (body: %s)", status, body)
	}
	transcriptA, err := h.queries.GetTranscriptIDByOwnerAndLocalID(ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: ownerA, LocalID: localA})
	if err != nil {
		t.Fatalf("resolve published owner A transcript: %v", err)
	}
	ownerAID := uuid.UUID(ownerA.Bytes)
	ownerAItem := computedAssociationPushItem(roundTrip.AssociationID)
	created := postBulkAnnotations(t, h, ownerAID, schema.AnnotationPushRequest{Annotations: []schema.AnnotationPushItem{ownerAItem}})
	if created.Created != 1 || created.Errors != 0 {
		t.Fatalf("owner A association annotation result: %+v", created)
	}
	listed, err := h.queries.ListAnnotationsByTranscriptID(ctx, transcriptA)
	if err != nil {
		t.Fatalf("list owner A association annotations: %v", err)
	}
	if len(listed) != 1 || !listed[0].TargetAssociationID.Valid || listed[0].TargetAssociationID.String != roundTrip.AssociationID {
		t.Fatalf("owner A association query discovery: got %+v, want one target %q", listed, roundTrip.AssociationID)
	}

	replay := requireAssociationLedgerCase(t, cases, "exact association replay")
	if status, body := publishAssociationTranscript(t, ctx, h, ownerA, "association-owner-a", localA, schema.PublishedAssociation{
		ID:                 schema.AssociationID(replay.AssociationID),
		ObservedCommitHash: replay.ObservedCommitHash,
	}); status != http.StatusOK {
		t.Fatalf("exact association replay: got %d, want 200 (body: %s)", status, body)
	}
	var associationCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcript_associations WHERE owner_id = $1", ownerA).Scan(&associationCount); err != nil {
		t.Fatalf("count owner A associations: %v", err)
	}
	if associationCount != 1 {
		t.Fatalf("exact replay created %d association rows, want 1", associationCount)
	}
	beforeRejectedPublish := transcriptAssociationSnapshot(t, ctx, pool, s3, transcriptA)
	if _, err := pool.Exec(ctx,
		"UPDATE transcript_associations SET observed_commit_sha = $1 WHERE owner_id = $2 AND association_id = $3",
		"commit-mutation-attempt", ownerA, roundTrip.AssociationID); err == nil {
		t.Fatal("association ledger update succeeded; immutable trigger must reject rebinding bypasses")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO transcript_associations (owner_id, association_id, transcript_id, observed_commit_sha)
		VALUES ($1, $2, $3, $4)
	`, ownerB, "assoc-owner-b-cross-owner", transcriptA, "commit-cross-owner"); err == nil {
		t.Fatal("cross-owner association insert succeeded; composite transcript FK must reject it")
	}

	rebind := requireAssociationLedgerCase(t, cases, "association rebind rejection")
	if status, body := publishAssociationTranscriptContent(t, ctx, h, ownerA, "association-owner-a", localA, schema.PublishedAssociation{
		ID:                 schema.AssociationID(rebind.AssociationID),
		ObservedCommitHash: rebind.ObservedCommitHash,
	}, `[{"role":"user","content":"rebind must not replace canonical blob"}]`); status != http.StatusUnprocessableEntity || !strings.Contains(body, "cannot be rebound") {
		t.Fatalf("association rebind: got %d body %s, want 422 with rebind remediation", status, body)
	}
	if afterRejectedPublish := transcriptAssociationSnapshot(t, ctx, pool, s3, transcriptA); afterRejectedPublish != beforeRejectedPublish {
		t.Fatal("rejected association rebind changed canonical transcript metadata, ledger, or blob bytes")
	}
	alias := requireAssociationLedgerCase(t, cases, "association alias rejection")
	if status, body := publishAssociationTranscriptContent(t, ctx, h, ownerA, "association-owner-a", localA, schema.PublishedAssociation{
		ID:                 schema.AssociationID(alias.AssociationID),
		ObservedCommitHash: alias.ObservedCommitHash,
	}, `[{"role":"user","content":"alias must not replace canonical blob"}]`); status != http.StatusUnprocessableEntity || !strings.Contains(body, "creating an alias") {
		t.Fatalf("association alias: got %d body %s, want 422 with alias remediation", status, body)
	}
	if afterRejectedPublish := transcriptAssociationSnapshot(t, ctx, pool, s3, transcriptA); afterRejectedPublish != beforeRejectedPublish {
		t.Fatal("rejected association alias changed canonical transcript metadata, ledger, or blob bytes")
	}

	multiFixture := requireAssociationLedgerCase(t, cases, "multi association publish persists atomically")
	multiAssociations := make([]schema.PublishedAssociation, 0, 1+len(multiFixture.AdditionalAssociations))
	multiAssociations = append(multiAssociations, schema.PublishedAssociation{ID: schema.AssociationID(multiFixture.AssociationID), ObservedCommitHash: multiFixture.ObservedCommitHash})
	for _, additional := range multiFixture.AdditionalAssociations {
		multiAssociations = append(multiAssociations, schema.PublishedAssociation{ID: schema.AssociationID(additional.AssociationID), ObservedCommitHash: additional.ObservedCommitHash})
	}
	localMulti := uuid.NewString()
	if status, body := publishAssociationTranscriptBatch(t, ctx, h, ownerA, "association-owner-a", localMulti, multiAssociations, `[{"role":"user","content":"multi association fixture"}]`); status != http.StatusCreated {
		t.Fatalf("multi association publish: got %d, want 201 (body: %s)", status, body)
	}
	multiTranscriptID, err := h.queries.GetTranscriptIDByOwnerAndLocalID(ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: ownerA, LocalID: localMulti})
	if err != nil {
		t.Fatalf("resolve multi association transcript: %v", err)
	}
	var multiAssociationCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcript_associations WHERE transcript_id = $1", multiTranscriptID).Scan(&multiAssociationCount); err != nil {
		t.Fatalf("count multi association ledger rows: %v", err)
	}
	if multiAssociationCount != len(multiAssociations) {
		t.Fatalf("multi association ledger rows=%d, want %d", multiAssociationCount, len(multiAssociations))
	}
	multiAnnotationItems := make([]schema.AnnotationPushItem, 0, len(multiAssociations))
	for _, association := range multiAssociations {
		multiAnnotationItems = append(multiAnnotationItems, computedAssociationPushItem(association.ID.String()))
	}
	if response := postBulkAnnotations(t, h, ownerAID, schema.AnnotationPushRequest{Annotations: multiAnnotationItems}); response.Created != len(multiAnnotationItems) || response.Errors != 0 {
		t.Fatalf("multi association annotation batch result: %+v", response)
	}
	if rows, err := h.queries.ListAnnotationsByTranscriptID(ctx, multiTranscriptID); err != nil || len(rows) != len(multiAnnotationItems) {
		t.Fatalf("multi association transcript discovery rows=%+v err=%v, want %d rows", rows, err, len(multiAnnotationItems))
	}
	if status, body := publishAssociationTranscriptBatch(t, ctx, h, ownerA, "association-owner-a", localMulti, multiAssociations, `[{"role":"user","content":"multi association fixture"}]`); status != http.StatusOK {
		t.Fatalf("multi association exact replay: got %d, want 200 (body: %s)", status, body)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcript_associations WHERE transcript_id = $1", multiTranscriptID).Scan(&multiAssociationCount); err != nil {
		t.Fatalf("count replayed multi association ledger rows: %v", err)
	}
	if multiAssociationCount != len(multiAssociations) {
		t.Fatalf("multi association replay ledger rows=%d, want %d", multiAssociationCount, len(multiAssociations))
	}

	foreign := requireAssociationLedgerCase(t, cases, "owner B cannot target owner A association")
	ownerBID := uuid.UUID(ownerB.Bytes)
	foreignResponse := callAssociationAnnotationPush(t, h, ownerBID, schema.AnnotationPushRequest{Annotations: []schema.AnnotationPushItem{
		associationPushItem("association-owner-b-foreign", foreign.AssociationID),
	}})
	if foreignResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("foreign association status: got %d, want 422 (body: %s)", foreignResponse.Code, foreignResponse.Body.String())
	}
	if got := decodeError(t, foreignResponse.Body.Bytes()); !strings.Contains(got, "not recorded for the authenticated owner") {
		t.Errorf("foreign association error %q does not explain owner isolation", got)
	}
	if n := countOwnerAnnotations(t, ctx, pool, ownerB); n != 0 {
		t.Fatalf("foreign association target wrote %d owner B annotations, want 0", n)
	}

	ownerBCase := requireAssociationLedgerCase(t, cases, "owner B records the same opaque ID independently")
	localB := uuid.NewString()
	if status, body := publishAssociationTranscript(t, ctx, h, ownerB, "association-owner-b", localB, schema.PublishedAssociation{
		ID:                 schema.AssociationID(ownerBCase.AssociationID),
		ObservedCommitHash: ownerBCase.ObservedCommitHash,
	}); status != http.StatusCreated {
		t.Fatalf("publish owner B association transcript: got %d, want 201 (body: %s)", status, body)
	}
	ownerBItem := computedAssociationPushItem(ownerBCase.AssociationID)
	if ownerAItem.ContentHash != ownerBItem.ContentHash {
		t.Fatalf("owner isolation fixture did not produce byte-identical computed hashes: %q != %q", ownerAItem.ContentHash, ownerBItem.ContentHash)
	}
	ownerAAnnotationCountBeforeOwnerB := countOwnerAnnotations(t, ctx, pool, ownerA)
	ownerBAnnotation := postBulkAnnotations(t, h, ownerBID, schema.AnnotationPushRequest{Annotations: []schema.AnnotationPushItem{ownerBItem}})
	if ownerBAnnotation.Created != 1 || ownerBAnnotation.Errors != 0 {
		t.Fatalf("owner B association annotation result: %+v", ownerBAnnotation)
	}
	if n := countOwnerAnnotations(t, ctx, pool, ownerA); n != ownerAAnnotationCountBeforeOwnerB {
		t.Fatalf("owner A identical-hash annotation count=%d, want unchanged %d", n, ownerAAnnotationCountBeforeOwnerB)
	}
	if n := countOwnerAnnotations(t, ctx, pool, ownerB); n != 1 {
		t.Fatalf("owner B identical-hash annotation count=%d, want 1", n)
	}
	requireManifestContains(t, associationAnnotationManifest(t, h, ownerAID), ownerAItem.ContentHash)
	requireSingleManifestHash(t, associationAnnotationManifest(t, h, ownerBID), ownerAItem.ContentHash)
	transcriptB, err := h.queries.GetTranscriptIDByOwnerAndLocalID(ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: ownerB, LocalID: localB})
	if err != nil {
		t.Fatalf("resolve published owner B transcript: %v", err)
	}
	ownerBListed, err := h.queries.ListAnnotationsByTranscriptID(ctx, transcriptB)
	if err != nil {
		t.Fatalf("list owner B association annotations: %v", err)
	}
	if len(ownerBListed) != 1 || !ownerBListed[0].TargetAssociationID.Valid || ownerBListed[0].TargetAssociationID.String != ownerBCase.AssociationID {
		t.Fatalf("owner B association query discovery: got %+v, want one target %q", ownerBListed, ownerBCase.AssociationID)
	}
	ownerAAfterB, err := h.queries.ListAnnotationsByTranscriptID(ctx, transcriptA)
	if err != nil {
		t.Fatalf("re-list owner A association annotations: %v", err)
	}
	if len(ownerAAfterB) != 1 || ownerAAfterB[0].OwnerID != ownerA {
		t.Fatalf("owner B association leaked into owner A transcript discovery: got %+v", ownerAAfterB)
	}

	batch := requireAssociationLedgerCase(t, cases, "annotation batch rolls back on an unknown association")
	beforeBatch := countOwnerAnnotations(t, ctx, pool, ownerA)
	batchResponse := callAssociationAnnotationPush(t, h, ownerAID, schema.AnnotationPushRequest{Annotations: []schema.AnnotationPushItem{
		associationPushItem("association-owner-a-valid-in-batch", roundTrip.AssociationID),
		associationPushItem("association-owner-a-unknown-in-batch", batch.AssociationID),
	}})
	if batchResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mixed association batch status: got %d, want 422 (body: %s)", batchResponse.Code, batchResponse.Body.String())
	}
	if afterBatch := countOwnerAnnotations(t, ctx, pool, ownerA); afterBatch != beforeBatch {
		t.Fatalf("unknown association batch changed owner A annotation count from %d to %d", beforeBatch, afterBatch)
	}

	// A transaction failure after transcript and association inserts must roll back
	// both, including the governance trigger side effect, before it reaches any
	// observable transcript or ledger state.
	rollbackLocalID := "association-rollback-" + uuid.NewString()
	rollbackAssociationID := "assoc-ledger-rollback"
	rollbackErr := errors.New("force association publish rollback")
	err = h.inTxAs(ctx, ownerA, func(q Querier) error {
		request := schema.PublishRequest{
			License:  schema.LicenseCC0,
			Identity: schema.SessionIdentity{SessionID: schema.SessionID(rollbackLocalID), SchemaVersion: 2},
			Model:    schema.ModelInfo{Harness: "claude-code", Model: "association-test"},
		}
		params := schemaToTranscriptParams(request, "blob/"+rollbackLocalID, 1, "2")
		params.OwnerID = ownerA
		params.LocalID = rollbackLocalID
		params = completeEncryptedFixtureParams(params)
		transcript, createErr := q.CreateTranscript(ctx, params)
		if createErr != nil {
			return createErr
		}
		newAssociations, validateErr := validatePublishedAssociationBindings(ctx, q, ownerA, transcript.ID, []schema.PublishedAssociation{{
			ID:                 schema.AssociationID(rollbackAssociationID),
			ObservedCommitHash: "commit-rollback",
		}})
		if validateErr != nil {
			return validateErr
		}
		if insertErr := insertPublishedAssociationBindings(ctx, q, ownerA, transcript.ID, newAssociations); insertErr != nil {
			return insertErr
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("forced association publish rollback error = %v, want %v", err, rollbackErr)
	}
	var rolledBackTranscripts, rolledBackAssociations int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcripts WHERE owner_id = $1 AND local_id = $2", ownerA, rollbackLocalID).Scan(&rolledBackTranscripts); err != nil {
		t.Fatalf("count rolled-back transcript: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcript_associations WHERE owner_id = $1 AND association_id = $2", ownerA, rollbackAssociationID).Scan(&rolledBackAssociations); err != nil {
		t.Fatalf("count rolled-back association: %v", err)
	}
	if rolledBackTranscripts != 0 || rolledBackAssociations != 0 {
		t.Fatalf("forced rollback persisted transcripts=%d associations=%d, want 0/0", rolledBackTranscripts, rolledBackAssociations)
	}

}

// TestAssociationAnnotationIngressSameLocalIDOwnerIsolationRealPostgres proves
// every transcript-discovery surface keys association annotations by transcript
// UUID. Two owners intentionally use the same local session id and opaque
// association id, a valid state that used to cross-list/cross-count rows.
func TestAssociationAnnotationIngressSameLocalIDOwnerIsolationRealPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping same-local-ID association integration test in -short mode")
	}
	cases := loadAssociationLedgerFixture(t)
	caseFixture := requireAssociationLedgerCase(t, cases, "same local ID association targets remain owner scoped")
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: newRecordingTranscriptBlobStore()}

	ownerA := associationFixtureUser(t, ctx, pool, 21, "same-local-a")
	ownerB := associationFixtureUser(t, ctx, pool, 22, "same-local-b")
	defer cleanupOwners(t, ctx, pool, ownerA, ownerB)
	sharedLocalID := uuid.NewString()
	transcriptA := govStore(t, ctx, h, ownerA, sharedLocalID, schema.LicenseCC0)
	transcriptB := govStore(t, ctx, h, ownerB, sharedLocalID, schema.LicenseCC0)
	association := schema.PublishedAssociation{ID: schema.AssociationID(caseFixture.AssociationID), ObservedCommitHash: caseFixture.ObservedCommitHash}
	recordAssociationBinding(t, ctx, h, ownerA, transcriptA.ID, association)
	recordAssociationBinding(t, ctx, h, ownerB, transcriptB.ID, association)

	ownerAID := uuid.UUID(ownerA.Bytes)
	ownerBID := uuid.UUID(ownerB.Bytes)
	ownerAItem := computedAssociationPushItem(caseFixture.AssociationID)
	ownerBItem := computedAssociationPushItem(caseFixture.AssociationID)
	if ownerAItem.ContentHash != ownerBItem.ContentHash {
		t.Fatalf("same-local-ID fixture did not generate identical annotation hashes: %q != %q", ownerAItem.ContentHash, ownerBItem.ContentHash)
	}
	if response := postBulkAnnotations(t, h, ownerAID, schema.AnnotationPushRequest{Annotations: []schema.AnnotationPushItem{ownerAItem}}); response.Created != 1 || response.Errors != 0 {
		t.Fatalf("owner A same-local association push: %+v", response)
	}
	if response := postBulkAnnotations(t, h, ownerBID, schema.AnnotationPushRequest{Annotations: []schema.AnnotationPushItem{ownerBItem}}); response.Created != 1 || response.Errors != 0 {
		t.Fatalf("owner B same-local association push: %+v", response)
	}
	rowsA, err := h.queries.ListAnnotationsByTranscriptID(ctx, transcriptA.ID)
	if err != nil || len(rowsA) != 1 || rowsA[0].OwnerID != ownerA {
		t.Fatalf("same-local SQL discovery for owner A = rows=%+v err=%v, want exactly owner A row", rowsA, err)
	}
	rowsB, err := h.queries.ListAnnotationsByTranscriptID(ctx, transcriptB.ID)
	if err != nil || len(rowsB) != 1 || rowsB[0].OwnerID != ownerB {
		t.Fatalf("same-local SQL discovery for owner B = rows=%+v err=%v, want exactly owner B row", rowsB, err)
	}

	annotationsA := listTranscriptAssociationAnnotations(t, h, ownerAID, uuid.UUID(transcriptA.ID.Bytes))
	if len(annotationsA) != 1 || annotationsA[0].ContentHash == nil || *annotationsA[0].ContentHash != ownerAItem.ContentHash {
		t.Fatalf("same-local transcript list for owner A = %+v, want only owner A annotation", annotationsA)
	}
	annotationsB := listTranscriptAssociationAnnotations(t, h, ownerBID, uuid.UUID(transcriptB.ID.Bytes))
	if len(annotationsB) != 1 || annotationsB[0].ContentHash == nil || *annotationsB[0].ContentHash != ownerBItem.ContentHash {
		t.Fatalf("same-local transcript list for owner B = %+v, want only owner B annotation", annotationsB)
	}
	if count, err := h.queries.CountTranscriptAnnotations(ctx, transcriptA.ID); err != nil || count != 1 {
		t.Fatalf("same-local owner A annotation count=%d err=%v, want 1/nil", count, err)
	}
	if count, err := h.queries.CountTranscriptAnnotations(ctx, transcriptB.ID); err != nil || count != 1 {
		t.Fatalf("same-local owner B annotation count=%d err=%v, want 1/nil", count, err)
	}
	pullListA, err := h.queries.ListPullableTranscripts(ctx, sqlc.ListPullableTranscriptsParams{UserID: ownerA, Limit: 10})
	if err != nil {
		t.Fatalf("list pullable transcripts for owner A: %v", err)
	}
	ownerAFound := false
	for _, row := range pullListA {
		if row.ID != transcriptA.ID {
			continue
		}
		ownerAFound = true
		if row.AnnotationCount != 1 {
			t.Fatalf("same-local pull list annotation count=%d, want 1", row.AnnotationCount)
		}
	}
	if !ownerAFound {
		t.Fatal("same-local owner A transcript is absent from owner A pull list")
	}
	pulledA := pullTranscriptAssociationAnnotations(t, h, ownerAID, uuid.UUID(transcriptA.ID.Bytes))
	if len(pulledA) != 1 || pulledA[0].AuthorUserID != ownerAID.String() || pulledA[0].ContentHash == nil || *pulledA[0].ContentHash != ownerAItem.ContentHash {
		t.Fatalf("same-local pull annotations for owner A = %+v, want only owner A annotation", pulledA)
	}

	// Owner B can pull owner A's shared transcript, but B's annotation is bound to
	// B's same-local-ID transcript only. The mounted skip gate must therefore see
	// an empty set for A and B's hash only for B.
	execAsSystem(t, ctx, pool, "UPDATE transcripts SET visibility = 'shared' WHERE id = $1", transcriptA.ID)
	group := pullInsertGroup(t, ctx, pool, ownerA, "association-same-local-share-"+uuid.NewString())
	pullAddMember(t, ctx, pool, group, ownerB, "member")
	pullShare(t, ctx, pool, transcriptA.ID, group, "approved")
	skipItems := make([]schema.PullSkipGateItem, 0, 2)
	skipItems = append(skipItems, schema.PullSkipGateItem{TranscriptID: wireTranscriptID(transcriptA.ID), AnnotationHashes: []string{}})
	skipItems = append(skipItems, schema.PullSkipGateItem{TranscriptID: wireTranscriptID(transcriptB.ID), AnnotationHashes: []string{ownerBItem.ContentHash}})
	skip := callSkipGate(t, h, ownerBID, schema.PullSkipGateRequest{Items: skipItems})
	if len(skip.Results) != 2 {
		t.Fatalf("same-local skip-gate result count=%d, want 2", len(skip.Results))
	}
	for _, result := range skip.Results {
		switch result.TranscriptID {
		case wireTranscriptID(transcriptA.ID):
			if !result.AnnotationsCurrent {
				t.Fatalf("same-local skip-gate treated owner B annotation as belonging to owner A transcript: %+v", result)
			}
		case wireTranscriptID(transcriptB.ID):
			if !result.AnnotationsCurrent {
				t.Fatalf("same-local skip-gate missed owner B annotation on owner B transcript: %+v", result)
			}
		default:
			t.Fatalf("same-local skip-gate returned unexpected transcript %q", result.TranscriptID)
		}
	}
	requireSingleManifestHash(t, associationAnnotationManifest(t, h, ownerAID), ownerAItem.ContentHash)
	requireSingleManifestHash(t, associationAnnotationManifest(t, h, ownerBID), ownerBItem.ContentHash)
}

// TestAssociationAnnotationIngressMountedPublishRollback proves the mounted
// PublishTranscript transaction rolls back its transcript, ledger, and audit
// writes when storage rejects a ledger append after the transcript INSERT.
func TestAssociationAnnotationIngressMountedPublishRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mounted association rollback integration test in -short mode")
	}
	cases := loadAssociationLedgerFixture(t)
	rollbackCase := requireAssociationLedgerCase(t, cases, "post-insert ledger failure rolls back mounted publish")
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	removeAssociationInsertFailureTrigger := installAssociationInsertFailureTrigger(t, ctx, pool)
	defer removeAssociationInsertFailureTrigger()
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: newRecordingTranscriptBlobStore()}
	owner := associationFixtureUser(t, ctx, pool, 41, "mounted-rollback")
	defer cleanupOwners(t, ctx, pool, owner)
	var transcriptsBefore, associationsBefore, auditsBefore int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcripts WHERE owner_id = $1", owner).Scan(&transcriptsBefore); err != nil {
		t.Fatalf("count rollback fixture transcripts before publish: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcript_associations WHERE owner_id = $1", owner).Scan(&associationsBefore); err != nil {
		t.Fatalf("count rollback fixture associations before publish: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcript_governance_events_audit WHERE changed_by = $1", owner).Scan(&auditsBefore); err != nil {
		t.Fatalf("count rollback fixture audits before publish: %v", err)
	}
	if status, body := publishAssociationTranscript(t, ctx, h, owner, "association-mounted-rollback", uuid.NewString(), schema.PublishedAssociation{
		ID:                 schema.AssociationID(rollbackCase.AssociationID),
		ObservedCommitHash: rollbackCase.ObservedCommitHash,
	}); status != http.StatusInternalServerError || !strings.Contains(body, "Failed to save transcript") {
		t.Fatalf("forced post-insert failure publish: got %d body %s, want 500 save failure", status, body)
	}
	var transcriptsAfter, associationsAfter, auditsAfter int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcripts WHERE owner_id = $1", owner).Scan(&transcriptsAfter); err != nil {
		t.Fatalf("count rollback fixture transcripts after publish: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcript_associations WHERE owner_id = $1", owner).Scan(&associationsAfter); err != nil {
		t.Fatalf("count rollback fixture associations after publish: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcript_governance_events_audit WHERE changed_by = $1", owner).Scan(&auditsAfter); err != nil {
		t.Fatalf("count rollback fixture audits after publish: %v", err)
	}
	if transcriptsAfter != transcriptsBefore || associationsAfter != associationsBefore || auditsAfter != auditsBefore {
		t.Fatalf("mounted publish rollback changed transcripts/associations/audits from %d/%d/%d to %d/%d/%d", transcriptsBefore, associationsBefore, auditsBefore, transcriptsAfter, associationsAfter, auditsAfter)
	}
}

// TestAssociationAnnotationIngressConcurrentPublishLocks proves a duplicate
// owner-scoped durable association cannot pass preflight in two concurrent
// publishes. One call wins; the loser observes the committed ledger binding
// while holding the same narrow advisory lock and never uploads its blob.
func TestAssociationAnnotationIngressConcurrentPublishLocks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent association publish integration test in -short mode")
	}
	cases := loadAssociationLedgerFixture(t)
	concurrentCase := requireAssociationLedgerCase(t, cases, "concurrent association publish rejects before second blob write")
	pool := govTestPool(t)
	defer pool.Close()
	ctx := context.Background()
	s3 := newRecordingTranscriptBlobStore()
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: s3}
	owner := associationFixtureUser(t, ctx, pool, 51, "concurrent")
	defer cleanupOwners(t, ctx, pool, owner)
	association := schema.PublishedAssociation{ID: schema.AssociationID(concurrentCase.AssociationID), ObservedCommitHash: concurrentCase.ObservedCommitHash}
	type publishOutcome struct {
		status int
		body   string
	}
	start := make(chan struct{})
	outcomes := make(chan publishOutcome, 2)
	publish := func(localID string) {
		<-start
		status, body := publishAssociationTranscript(t, ctx, h, owner, "association-concurrent", localID, association)
		outcomes <- publishOutcome{status: status, body: body}
	}
	go publish(uuid.NewString())
	go publish(uuid.NewString())
	close(start)
	first := <-outcomes
	second := <-outcomes
	created := 0
	rejected := 0
	evaluate := func(outcome publishOutcome) {
		switch outcome.status {
		case http.StatusCreated:
			created++
		case http.StatusUnprocessableEntity:
			if !strings.Contains(outcome.body, "cannot be rebound") {
				t.Fatalf("concurrent association rejection body %q lacks remediation", outcome.body)
			}
			rejected++
		default:
			t.Fatalf("concurrent association publish status=%d body=%s", outcome.status, outcome.body)
		}
	}
	evaluate(first)
	evaluate(second)
	if created != 1 || rejected != 1 {
		t.Fatalf("concurrent association outcomes created=%d rejected=%d, want 1/1", created, rejected)
	}
	if len(s3.blobs) != 1 {
		t.Fatalf("concurrent association publish wrote %d blobs, want exactly one winning blob", len(s3.blobs))
	}
	var associationCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcript_associations WHERE owner_id = $1 AND association_id = $2", owner, concurrentCase.AssociationID).Scan(&associationCount); err != nil {
		t.Fatalf("count concurrent association bindings: %v", err)
	}
	if associationCount != 1 {
		t.Fatalf("concurrent association ledger rows=%d, want 1", associationCount)
	}
}
