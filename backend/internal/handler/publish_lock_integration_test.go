//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/association_annotation_ingress/publish_lock_cases.yaml
var publishLockCasesYAML []byte

type publishLockAssociation struct {
	AssociationID      string `yaml:"associationId"`
	ObservedCommitHash string `yaml:"observedCommitHash"`
}

type publishLockExpectedTopology struct {
	Name    string `yaml:"name"`
	Granted int    `yaml:"granted"`
	Waiting int    `yaml:"waiting"`
}

type publishLockCase struct {
	Name                      string                        `yaml:"name"`
	LocalID                   string                        `yaml:"localId"`
	CompetingLocalID          string                        `yaml:"competingLocalId"`
	PrimaryContent            string                        `yaml:"primaryContent"`
	CompetingContent          string                        `yaml:"competingContent"`
	ExpectedPrimaryStatus     int                           `yaml:"expectedPrimaryStatus"`
	ExpectedPrimaryBody       string                        `yaml:"expectedPrimaryBody"`
	ExpectedCompetingStatus   int                           `yaml:"expectedCompetingStatus"`
	ExpectedCompetingBody     string                        `yaml:"expectedCompetingBody"`
	PrimaryAssociationOrder   []string                      `yaml:"primaryAssociationOrder"`
	CompetingAssociationOrder []string                      `yaml:"competingAssociationOrder"`
	ExternalGateAssociationID string                        `yaml:"externalGateAssociationId"`
	Associations              []publishLockAssociation      `yaml:"associations"`
	RecoveryAssociations      []publishLockAssociation      `yaml:"recoveryAssociations"`
	ExpectedTopology          []publishLockExpectedTopology `yaml:"expectedTopology"`
}

type publishLockFixture struct {
	ExpectedCaseCount int                      `yaml:"expectedCaseCount"`
	Cases             []publishLockCase        `yaml:"cases"`
	InvalidCases      []publishLockInvalidCase `yaml:"invalidCases"`
}

type publishLockInvalidCase struct {
	Name          string          `yaml:"name"`
	Case          publishLockCase `yaml:"case"`
	ExpectedError string          `yaml:"expectedError"`
}

const publishLockFixturePath = "testdata/association_annotation_ingress/publish_lock_cases.yaml"

func decodePublishLockFixture(raw []byte) (publishLockFixture, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	var fixture publishLockFixture
	if err := decoder.Decode(&fixture); err != nil {
		return publishLockFixture{}, fmt.Errorf("publish lock fixture %s could not be decoded with known fields; correct the named YAML field and value: %w", publishLockFixturePath, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return publishLockFixture{}, fmt.Errorf("publish lock fixture %s must contain exactly one YAML document; remove the trailing document: %v", publishLockFixturePath, err)
	}
	if len(fixture.Cases) != fixture.ExpectedCaseCount || fixture.ExpectedCaseCount != 5 {
		return publishLockFixture{}, fmt.Errorf("publish lock fixture %s field expectedCaseCount=%d does not match cases=%d and required count 5; update the declaration and complete case corpus together", publishLockFixturePath, fixture.ExpectedCaseCount, len(fixture.Cases))
	}
	return fixture, nil
}

func validatePublishLockCase(c publishLockCase) error {
	problem := func(field string, bad any, remediation string) error {
		return fmt.Errorf("publish lock fixture %s case %q field %s has invalid value %v; %s", publishLockFixturePath, c.Name, field, bad, remediation)
	}
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.LocalID) == "" || len(c.Associations) == 0 {
		return problem("name/localId/associations", fmt.Sprintf("%q/%q/%d", c.Name, c.LocalID, len(c.Associations)), "provide a named case, nonblank localId, and at least one declared association")
	}
	primaryIDs, allIDs, hashes := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, association := range c.Associations {
		if strings.TrimSpace(association.AssociationID) == "" || strings.TrimSpace(association.ObservedCommitHash) == "" {
			return problem("associations", association, "provide nonblank associationId and observedCommitHash values")
		}
		if _, exists := allIDs[association.AssociationID]; exists {
			return problem("associations.associationId", association.AssociationID, "declare every association ID exactly once")
		}
		if _, exists := hashes[association.ObservedCommitHash]; exists {
			return problem("associations.observedCommitHash", association.ObservedCommitHash, "declare every observed commit hash exactly once")
		}
		primaryIDs[association.AssociationID], allIDs[association.AssociationID], hashes[association.ObservedCommitHash] = struct{}{}, struct{}{}, struct{}{}
	}
	for _, association := range c.RecoveryAssociations {
		if strings.TrimSpace(association.AssociationID) == "" || strings.TrimSpace(association.ObservedCommitHash) == "" {
			return problem("recoveryAssociations", association, "provide nonblank recovery associationId and observedCommitHash values")
		}
		if _, exists := allIDs[association.AssociationID]; exists {
			return problem("recoveryAssociations.associationId", association.AssociationID, "use an ID distinct from every failed association")
		}
		if _, exists := hashes[association.ObservedCommitHash]; exists {
			return problem("recoveryAssociations.observedCommitHash", association.ObservedCommitHash, "use a hash distinct from every failed association")
		}
		allIDs[association.AssociationID], hashes[association.ObservedCommitHash] = struct{}{}, struct{}{}
	}
	validateOrder := func(field string, order []string) error {
		if len(order) != len(c.Associations) {
			return problem(field, order, "list every declared association exactly once")
		}
		seen := make(map[string]struct{}, len(order))
		for _, id := range order {
			if strings.TrimSpace(id) == "" {
				return problem(field, id, "replace the blank member with a declared association ID")
			}
			if _, known := primaryIDs[id]; !known {
				return problem(field, id, "use only IDs declared in associations")
			}
			if _, duplicate := seen[id]; duplicate {
				return problem(field, id, "list every declared association exactly once without duplicates")
			}
			seen[id] = struct{}{}
		}
		return nil
	}
	if c.Name == "reversed association order" {
		if len(c.Associations) != 3 || len(c.PrimaryAssociationOrder) != 3 || len(c.CompetingAssociationOrder) != 3 {
			return problem("associations/primaryAssociationOrder/competingAssociationOrder", fmt.Sprintf("%d/%d/%d", len(c.Associations), len(c.PrimaryAssociationOrder), len(c.CompetingAssociationOrder)), "declare exactly three primary associations and list all three in each order")
		}
		if err := validateOrder("primaryAssociationOrder", c.PrimaryAssociationOrder); err != nil {
			return err
		}
		if err := validateOrder("competingAssociationOrder", c.CompetingAssociationOrder); err != nil {
			return err
		}
		if _, known := primaryIDs[c.ExternalGateAssociationID]; !known {
			return problem("externalGateAssociationId", c.ExternalGateAssociationID, "select one ID declared in associations")
		}
		if c.PrimaryContent == "" || c.CompetingContent == "" || c.ExpectedPrimaryStatus == 0 || c.ExpectedCompetingStatus == 0 || c.ExpectedCompetingBody == "" {
			return problem("topology outcome", c, "provide both contents, statuses, and the exact competing error body")
		}
		if !(c.PrimaryAssociationOrder[0] < c.PrimaryAssociationOrder[2] && c.PrimaryAssociationOrder[2] < c.PrimaryAssociationOrder[1]) {
			return problem("primaryAssociationOrder", c.PrimaryAssociationOrder, "preserve the A<B<C lexical topology")
		}
		if len(c.ExpectedTopology) != 3 {
			return problem("expectedTopology", c.ExpectedTopology, "declare exactly the A, B, and C lock topology rows")
		}
		topologyNames := make(map[string]struct{}, 3)
		for _, expected := range c.ExpectedTopology {
			if expected.Name != "A" && expected.Name != "B" && expected.Name != "C" {
				return problem("expectedTopology.name", expected.Name, "use only the discovered lock names A, B, and C")
			}
			if _, duplicate := topologyNames[expected.Name]; duplicate {
				return problem("expectedTopology.name", expected.Name, "declare each lock name exactly once")
			}
			if expected.Granted < 0 || expected.Waiting < 0 || expected.Granted+expected.Waiting == 0 {
				return problem("expectedTopology counts", expected, "provide nonnegative granted/waiting counts with at least one observed lock")
			}
			topologyNames[expected.Name] = struct{}{}
		}
	}
	if (c.Name == "canceled association publish" || c.Name == "post insert rollback") && (c.CompetingLocalID == "" || c.PrimaryContent == "" || c.CompetingContent == "" || c.ExpectedPrimaryStatus == 0 || c.ExpectedCompetingStatus == 0) {
		return problem("recovery fields", c, "provide competingLocalId, both contents, and both statuses")
	}
	if c.Name == "pool one association publish" && (c.PrimaryContent == "" || c.ExpectedPrimaryStatus == 0) {
		return problem("primaryContent/expectedPrimaryStatus", c, "provide exact pool-one content and status")
	}
	if c.Name == "mixed source publish" && (c.PrimaryContent == "" || c.CompetingContent == "" || c.ExpectedPrimaryStatus == 0 || c.ExpectedCompetingStatus == 0) {
		return problem("mixed outcome", c, "provide both contents and statuses")
	}
	if c.Name == "post insert rollback" && len(c.RecoveryAssociations) == 0 {
		return problem("recoveryAssociations", c.RecoveryAssociations, "declare at least one recovery association")
	}
	return nil
}

func loadPublishLockCases(t *testing.T) map[string]publishLockCase {
	t.Helper()
	fixture, err := decodePublishLockFixture(publishLockCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]publishLockCase, len(fixture.Cases))
	for _, c := range fixture.Cases {
		if err := validatePublishLockCase(c); err != nil {
			t.Fatal(err)
		}
		if _, duplicate := result[c.Name]; duplicate {
			t.Fatalf("publish lock fixture %s case %q field name duplicates an earlier case; choose a unique case name", publishLockFixturePath, c.Name)
		}
		result[c.Name] = c
	}
	return result
}

func TestPublishLockFixtureValidation(t *testing.T) {
	fixture, err := decodePublishLockFixture(publishLockCasesYAML)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixture.InvalidCases) != 7 {
		t.Fatalf("publish lock fixture %s invalidCases=%d, want 7 fixture-owned semantic mutations", publishLockFixturePath, len(fixture.InvalidCases))
	}
	invalidNames := make(map[string]struct{}, len(fixture.InvalidCases))
	for _, invalid := range fixture.InvalidCases {
		if strings.TrimSpace(invalid.Name) == "" || strings.TrimSpace(invalid.ExpectedError) == "" {
			t.Fatalf("publish lock fixture %s invalid case name=%q expectedError=%q, want both nonblank", publishLockFixturePath, invalid.Name, invalid.ExpectedError)
		}
		if _, duplicate := invalidNames[invalid.Name]; duplicate {
			t.Fatalf("publish lock fixture %s invalid case name=%q duplicates an earlier case; choose a unique name", publishLockFixturePath, invalid.Name)
		}
		invalidNames[invalid.Name] = struct{}{}
		if err := validatePublishLockCase(invalid.Case); err == nil || !strings.Contains(err.Error(), invalid.ExpectedError) {
			t.Errorf("invalid fixture %q error=%v, want substring %q", invalid.Name, err, invalid.ExpectedError)
		}
	}
	unknownField := append(append([]byte(nil), publishLockCasesYAML...), []byte("\nunknownTopLevelField: true\n")...)
	if _, err := decodePublishLockFixture(unknownField); err == nil || !strings.Contains(err.Error(), "field unknownTopLevelField not found") {
		t.Errorf("fixture with unknown top-level field error=%v, want known-fields decode failure", err)
	}
	secondDocument := append(append([]byte(nil), publishLockCasesYAML...), []byte("\n---\n{}\n")...)
	if _, err := decodePublishLockFixture(secondDocument); err == nil || !strings.Contains(err.Error(), "must contain exactly one YAML document") {
		t.Errorf("fixture with second YAML document error=%v, want exactly-one-document failure", err)
	}
}

func publishLockPool(t *testing.T, maxConns int32) *pgxpool.Pool {
	t.Helper()
	cfg, err := database.PoolConfig(pullTestDatabaseURL(t))
	if err != nil {
		t.Fatalf("build publish lock pool config: %v", err)
	}
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create publish lock pool: %v", err)
	}
	if pool.Config().MaxConns != maxConns {
		pool.Close()
		t.Fatalf("publish lock pool max connections=%d, want %d", pool.Config().MaxConns, maxConns)
	}
	if err := database.RunMigrations(pool); err != nil {
		pool.Close()
		t.Fatalf("migrate publish lock database: %v", err)
	}
	return pool
}

type publishOutcome struct {
	status int
	body   string
	err    error
}

type gatedRecordingTranscriptBlobStore struct {
	*recordingTranscriptBlobStore
	entered       chan struct{}
	release       chan struct{}
	once          sync.Once
	updateEntered chan struct{}
	updateRelease chan struct{}
	updateOnce    sync.Once
	mu            sync.Mutex
	uploads       int
}

func newGatedRecordingTranscriptBlobStore() *gatedRecordingTranscriptBlobStore {
	return &gatedRecordingTranscriptBlobStore{recordingTranscriptBlobStore: newRecordingTranscriptBlobStore(), entered: make(chan struct{}), release: make(chan struct{}), updateEntered: make(chan struct{}), updateRelease: make(chan struct{})}
}

func (s *gatedRecordingTranscriptBlobStore) Write(ctx context.Context, id uuid.UUID, content []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	s.mu.Lock()
	s.uploads++
	stage := s.uploads
	s.mu.Unlock()
	waitFor := func(once *sync.Once, entered, release chan struct{}) error {
		wait := false
		once.Do(func() { wait = true; close(entered) })
		if !wait {
			return nil
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if stage == 1 {
		if err := waitFor(&s.once, s.entered, s.release); err != nil {
			return storage.BlobDescriptor{}, storage.ContentIdentity{}, err
		}
	} else if stage == 2 {
		if err := waitFor(&s.updateOnce, s.updateEntered, s.updateRelease); err != nil {
			return storage.BlobDescriptor{}, storage.ContentIdentity{}, err
		}
	}
	return s.recordingTranscriptBlobStore.Write(ctx, id, content)
}

func preparePublish(owner pgtype.UUID, username, localID string, associations []schema.PublishedAssociation, content string) (func(context.Context, *Handler) publishOutcome, error) {
	metadata := schema.PublishRequest{Identity: schema.SessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2}, Model: schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "association-test"}, Timestamp: schema.TimestampInfo{Start: 1700000000000, End: 1700000060000}, Source: schema.SourceInfo{FilePath: "/fixtures/association.jsonl", Format: "jsonl"}, Git: schema.GitContext{Branch: strPtr("main"), Associations: associations}, Project: schema.ProjectContext{Hash: testProjectHash, Name: "association-fixture"}, Stats: schema.SessionStats{TurnCount: 1, ToolCallCount: 1, DurationMs: 1000, TokensIn: 1, TokensOut: 1}, Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}}}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	var raw bytes.Buffer
	w := multipart.NewWriter(&raw)
	if err := w.WriteField("metadata", string(metadataJSON)); err != nil {
		return nil, err
	}
	part, err := w.CreateFormFile("transcript_file", "fixture.jsonl")
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(part, content); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	body, boundary := append([]byte(nil), raw.Bytes()...), w.Boundary()
	return func(ctx context.Context, h *Handler) publishOutcome {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", bytes.NewReader(body))
		r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
		r = r.WithContext(context.WithValue(ctx, UserContextKey, &AuthUser{ID: uuid.UUID(owner.Bytes), Username: username}))
		response := httptest.NewRecorder()
		h.PublishTranscript(response, r)
		return publishOutcome{status: response.Code, body: response.Body.String()}
	}, nil
}

func awaitAdvisoryWait(ctx context.Context, pool *pgxpool.Pool) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND pid <> pg_backend_pid())`).Scan(&waiting); err != nil {
			return err
		}
		if waiting {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("advisory-lock waiter was not observed: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func fixtureAssociations(c publishLockCase) []schema.PublishedAssociation {
	associations := make([]schema.PublishedAssociation, 0, len(c.Associations))
	for _, association := range c.Associations {
		associations = append(associations, schema.PublishedAssociation{ID: schema.AssociationID(association.AssociationID), ObservedCommitHash: association.ObservedCommitHash})
	}
	return associations
}

func fixtureRecoveryAssociations(c publishLockCase) []schema.PublishedAssociation {
	associations := make([]schema.PublishedAssociation, 0, len(c.RecoveryAssociations))
	for _, association := range c.RecoveryAssociations {
		associations = append(associations, schema.PublishedAssociation{ID: schema.AssociationID(association.AssociationID), ObservedCommitHash: association.ObservedCommitHash})
	}
	return associations
}

func orderedFixtureAssociations(c publishLockCase, order []string) ([]schema.PublishedAssociation, error) {
	byID := make(map[string]schema.PublishedAssociation, len(c.Associations))
	for _, association := range fixtureAssociations(c) {
		byID[association.ID.String()] = association
	}
	result := make([]schema.PublishedAssociation, 0, len(order))
	for _, id := range order {
		association, exists := byID[id]
		if !exists {
			return nil, fmt.Errorf("publish lock fixture %s case %q order contains unknown association ID %q; use only IDs declared in associations", publishLockFixturePath, c.Name, id)
		}
		result = append(result, association)
	}
	return result, nil
}

func publishLockKeys(owner pgtype.UUID, localID string, associations []schema.PublishedAssociation) []string {
	keys := []string{fmt.Sprintf("village:association-publish:%x:session:%s", owner.Bytes, localID)}
	seen := map[string]struct{}{keys[0]: {}}
	for _, association := range associations {
		key := fmt.Sprintf("village:association-publish:%x:association:%s", owner.Bytes, association.ID)
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	return keys
}

type mountedPublishWorkers struct {
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	releases []func()
}

type publishBarrier struct {
	release chan struct{}
	once    sync.Once
}

func newPublishBarrier() *publishBarrier { return &publishBarrier{release: make(chan struct{})} }

func (b *publishBarrier) wait(ctx context.Context) error {
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *publishBarrier) open() { b.once.Do(func() { close(b.release) }) }

func newMountedPublishWorkers(parent context.Context, releases ...func()) *mountedPublishWorkers {
	ctx, cancel := context.WithCancel(parent)
	return &mountedPublishWorkers{ctx: ctx, cancel: cancel, releases: releases}
}

func (w *mountedPublishWorkers) start(run func(context.Context) publishOutcome) <-chan publishOutcome {
	out := make(chan publishOutcome, 1)
	w.wg.Add(1)
	go func() { defer w.wg.Done(); out <- run(w.ctx) }()
	return out
}

func (w *mountedPublishWorkers) cleanup(t *testing.T) {
	t.Helper()
	w.cancel()
	for _, release := range w.releases {
		release()
	}
	done := make(chan struct{})
	go func() { w.wg.Wait(); close(done) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-done:
	case <-ctx.Done():
		t.Errorf("mounted publish workers did not stop within cleanup deadline: %v", ctx.Err())
	}
}

func receivePublish(ctx context.Context, outcome <-chan publishOutcome) (publishOutcome, error) {
	select {
	case result := <-outcome:
		return result, nil
	case <-ctx.Done():
		return publishOutcome{}, ctx.Err()
	}
}

type publishResponse struct {
	Transcript struct {
		ID      string `json:"id"`
		LocalID string `json:"local_id"`
		BlobKey string `json:"blob_key"`
	} `json:"transcript"`
	PersistedBlobKey string `json:"-"`
}

func assertSuccessfulPublish(t *testing.T, ctx context.Context, pool *pgxpool.Pool, s3 *recordingTranscriptBlobStore, owner pgtype.UUID, localID, content string, associations []publishLockAssociation, expectedAssociationCount int, outcome publishOutcome) publishResponse {
	t.Helper()
	if outcome.err != nil {
		t.Fatalf("mounted publish for local ID %q returned worker error: %v", localID, outcome.err)
	}
	var response publishResponse
	if err := json.Unmarshal([]byte(outcome.body), &response); err != nil {
		t.Fatalf("decode successful mounted publish response for local ID %q: %v; body=%q", localID, err, outcome.body)
	}
	var transcriptID pgtype.UUID
	var persistedLocalID, blobKey, contentHash string
	if err := pool.QueryRow(ctx, `SELECT id,local_id,blob_key,content_hash FROM transcripts WHERE owner_id=$1 AND local_id=$2`, owner, localID).Scan(&transcriptID, &persistedLocalID, &blobKey, &contentHash); err != nil {
		t.Fatalf("read successful mounted publish row for local ID %q: %v", localID, err)
	}
	wantID := uuid.UUID(transcriptID.Bytes).String()
	keyPrefix := "transcripts/"
	keyID := strings.TrimSuffix(strings.TrimPrefix(blobKey, keyPrefix), ".bin")
	_, keyIDErr := uuid.Parse(keyID)
	wantHash := schema.ComputeTranscriptHash([]byte(content))
	validKey := strings.HasPrefix(blobKey, keyPrefix) && strings.HasSuffix(blobKey, ".bin") && keyIDErr == nil
	if response.Transcript.ID != wantID || response.Transcript.LocalID != persistedLocalID || persistedLocalID != localID || response.Transcript.BlobKey != "" || !validKey || contentHash != wantHash {
		t.Fatalf("mounted publish identity response=%+v row=id:%s local:%q key:%q hash:%q, want local=%q internal key contract=%q+UUID+.bin, no response key, hash=%q (key UUID error=%v)", response.Transcript, wantID, persistedLocalID, blobKey, contentHash, localID, keyPrefix, wantHash, keyIDErr)
	}
	for _, association := range associations {
		var gotTranscript pgtype.UUID
		var gotHash string
		if err := pool.QueryRow(ctx, `SELECT transcript_id,observed_commit_sha FROM transcript_associations WHERE owner_id=$1 AND association_id=$2`, owner, association.AssociationID).Scan(&gotTranscript, &gotHash); err != nil || gotTranscript != transcriptID || gotHash != association.ObservedCommitHash {
			t.Fatalf("association %q binding transcript=%v hash=%q err=%v, want transcript=%v hash=%q", association.AssociationID, gotTranscript, gotHash, err, transcriptID, association.ObservedCommitHash)
		}
	}
	var associationCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_associations WHERE owner_id=$1 AND transcript_id=$2`, owner, transcriptID).Scan(&associationCount); err != nil || associationCount != expectedAssociationCount {
		t.Fatalf("mounted publish association count=%d err=%v, want %d for transcript=%s", associationCount, err, expectedAssociationCount, wantID)
	}
	if got := s3.contents(t, blobKey); !bytes.Equal(got, []byte(content)) {
		t.Fatalf("recording transcript object key %q plaintext=%q, want exact fixture bytes %q", blobKey, got, content)
	}
	response.PersistedBlobKey = blobKey
	return response
}

func TestAssociationAnnotationIngressPoolOnePublishLock(t *testing.T) {
	c := loadPublishLockCases(t)["pool one association publish"]
	pool := publishLockPool(t, 1)
	defer pool.Close()
	s3 := newRecordingTranscriptBlobStore()
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: s3}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owner := associationFixtureUser(t, ctx, pool, 61, "pool-one")
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanupOwners(t, cleanupCtx, pool, owner)
	}()
	status, body := publishAssociationTranscriptBatch(t, ctx, h, owner, "pool-one", c.LocalID, fixtureAssociations(c), c.PrimaryContent)
	if status != c.ExpectedPrimaryStatus {
		t.Fatalf("pool-one publish status=%d body=%s", status, body)
	}
	assertSuccessfulPublish(t, ctx, pool, s3, owner, c.LocalID, c.PrimaryContent, c.Associations, len(c.Associations), publishOutcome{status: status, body: body})
}

func TestAssociationAnnotationIngressMixedPublishLock(t *testing.T) {
	c := loadPublishLockCases(t)["mixed source publish"]
	pool := publishLockPool(t, 4)
	defer pool.Close()
	s3 := newGatedRecordingTranscriptBlobStore()
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: s3}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	owner := associationFixtureUser(t, ctx, pool, 62, "mixed")
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanupOwners(t, cleanupCtx, pool, owner)
	}()
	firstPublish, err := preparePublish(owner, "mixed", c.LocalID, nil, c.PrimaryContent)
	if err != nil {
		t.Fatal(err)
	}
	secondPublish, err := preparePublish(owner, "mixed", c.LocalID, fixtureAssociations(c), c.CompetingContent)
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	var updateReleaseOnce sync.Once
	workers := newMountedPublishWorkers(ctx, func() { releaseOnce.Do(func() { close(s3.release) }) }, func() { updateReleaseOnce.Do(func() { close(s3.updateRelease) }) })
	defer workers.cleanup(t)
	firstOutcome := workers.start(func(workerCtx context.Context) publishOutcome { return firstPublish(workerCtx, h) })
	select {
	case <-s3.entered:
	case <-ctx.Done():
		t.Fatal("first mixed publish did not reach encrypted transcript storage")
	}
	secondOutcome := workers.start(func(workerCtx context.Context) publishOutcome { return secondPublish(workerCtx, h) })
	if err := awaitAdvisoryWait(ctx, pool); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(s3.release) })
	first, err := receivePublish(ctx, firstOutcome)
	if err != nil {
		t.Fatal(err)
	}
	if first.status != c.ExpectedPrimaryStatus {
		t.Fatalf("mixed create status=%d body=%s, want %d", first.status, first.body, c.ExpectedPrimaryStatus)
	}
	created := assertSuccessfulPublish(t, ctx, pool, s3.recordingTranscriptBlobStore, owner, c.LocalID, c.PrimaryContent, nil, 0, first)
	select {
	case <-s3.updateEntered:
	case <-ctx.Done():
		t.Fatal("mixed update did not reach its gated transcript-storage stage")
	}
	updateReleaseOnce.Do(func() { close(s3.updateRelease) })
	second, err := receivePublish(ctx, secondOutcome)
	if err != nil {
		t.Fatal(err)
	}
	if second.status != c.ExpectedCompetingStatus {
		t.Fatalf("mixed update status=%d body=%s, want %d", second.status, second.body, c.ExpectedCompetingStatus)
	}
	updated := assertSuccessfulPublish(t, ctx, pool, s3.recordingTranscriptBlobStore, owner, c.LocalID, c.CompetingContent, c.Associations, len(c.Associations), second)
	if created.Transcript.ID != updated.Transcript.ID || created.PersistedBlobKey == updated.PersistedBlobKey {
		t.Fatalf("mixed create/update must preserve transcript identity and replace the persisted encrypted key: create=%+v update=%+v", created, updated)
	}
}

func TestAssociationAnnotationIngressReversedPublishLock(t *testing.T) {
	c := loadPublishLockCases(t)["reversed association order"]
	pool := publishLockPool(t, 4)
	defer pool.Close()
	s3 := newRecordingTranscriptBlobStore()
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: s3}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	owner := associationFixtureUser(t, ctx, pool, 63, "reversed")
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanupOwners(t, cleanupCtx, pool, owner)
	}()
	primaryAssociations, err := orderedFixtureAssociations(c, c.PrimaryAssociationOrder)
	if err != nil {
		t.Fatal(err)
	}
	competingAssociations, err := orderedFixtureAssociations(c, c.CompetingAssociationOrder)
	if err != nil {
		t.Fatal(err)
	}
	primaryPublish, err := preparePublish(owner, "reversed", c.LocalID, primaryAssociations, c.PrimaryContent)
	if err != nil {
		t.Fatal(err)
	}
	competingPublish, err := preparePublish(owner, "reversed", c.CompetingLocalID, competingAssociations, c.CompetingContent)
	if err != nil {
		t.Fatal(err)
	}
	observer, err := pgx.Connect(ctx, pullTestDatabaseURL(t))
	if err != nil {
		t.Fatalf("connect independent reversed lock observer: %v", err)
	}
	var closeObserverOnce sync.Once
	closeObserver := func(closeCtx context.Context) error {
		var closeErr error
		closeObserverOnce.Do(func() { closeErr = observer.Close(closeCtx) })
		return closeErr
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := closeObserver(closeCtx); err != nil {
			t.Errorf("close independent reversed lock observer during fallback cleanup: %v", err)
		}
	}()
	associationKey := func(id string) string {
		return fmt.Sprintf("village:association-publish:%x:association:%s", owner.Bytes, id)
	}
	type lockTag struct {
		classID, objectID uint32
		subID             uint16
	}
	tags := map[string]lockTag{}
	discover := func(name, key string, retain bool) {
		if _, err := observer.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1,0))", key); err != nil {
			t.Fatal(err)
		}
		var tag lockTag
		if err := observer.QueryRow(ctx, `SELECT classid::bigint, objid::bigint, objsubid FROM pg_locks WHERE pid=pg_backend_pid() AND locktype='advisory' ORDER BY classid,objid LIMIT 1`).Scan(&tag.classID, &tag.objectID, &tag.subID); err != nil {
			t.Fatal(err)
		}
		tags[name] = tag
		if !retain {
			if _, err := observer.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1,0))", key); err != nil {
				t.Fatal(err)
			}
		}
	}
	discover("A", associationKey(c.PrimaryAssociationOrder[0]), false)
	discover("B", associationKey(c.PrimaryAssociationOrder[2]), false)
	discover("C", associationKey(c.ExternalGateAssociationID), true)
	var releaseGateOnce sync.Once
	workers := newMountedPublishWorkers(ctx, func() {
		releaseGateOnce.Do(func() {
			var unlocked bool
			if err := observer.QueryRow(ctx, "SELECT pg_advisory_unlock(hashtextextended($1,0))", associationKey(c.ExternalGateAssociationID)).Scan(&unlocked); err != nil || !unlocked {
				t.Errorf("release external reversed lock gate unlocked=%v err=%v", unlocked, err)
			}
		})
	})
	defer workers.cleanup(t)
	primaryReady, competingReady := make(chan struct{}), make(chan struct{})
	startPrimary, startCompeting := newPublishBarrier(), newPublishBarrier()
	workers.releases = append(workers.releases, startPrimary.open, startCompeting.open)
	primaryOutcome := workers.start(func(workerCtx context.Context) publishOutcome {
		close(primaryReady)
		if err := startPrimary.wait(workerCtx); err != nil {
			return publishOutcome{err: err}
		}
		return primaryPublish(workerCtx, h)
	})
	competingOutcome := workers.start(func(workerCtx context.Context) publishOutcome {
		close(competingReady)
		if err := startCompeting.wait(workerCtx); err != nil {
			return publishOutcome{err: err}
		}
		return competingPublish(workerCtx, h)
	})
	<-primaryReady
	<-competingReady
	startPrimary.open()
	// Establish which mounted request is the deterministic winner while C remains externally held.
	for {
		var granted int
		tag := tags["A"]
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND classid=$1 AND objid=$2 AND objsubid=$3 AND granted`, tag.classID, tag.objectID, tag.subID).Scan(&granted); err != nil {
			t.Fatal(err)
		}
		if granted == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("primary did not acquire A before topology setup")
		case <-time.After(10 * time.Millisecond):
		}
	}
	startCompeting.open()
	for {
		diagnostics := make([]string, 0, 3)
		matches := true
		for _, expected := range c.ExpectedTopology {
			tag := tags[expected.Name]
			var granted, waiting int
			var grantedPIDs, waitingPIDs []int32
			if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE granted), count(*) FILTER (WHERE NOT granted), coalesce(array_agg(pid) FILTER (WHERE granted),'{}'), coalesce(array_agg(pid) FILTER (WHERE NOT granted),'{}') FROM pg_locks WHERE locktype='advisory' AND classid=$1 AND objid=$2 AND objsubid=$3`, tag.classID, tag.objectID, tag.subID).Scan(&granted, &waiting, &grantedPIDs, &waitingPIDs); err != nil {
				t.Fatal(err)
			}
			diagnostics = append(diagnostics, fmt.Sprintf("%s granted=%d pids=%v waiting=%d pids=%v", expected.Name, granted, grantedPIDs, waiting, waitingPIDs))
			matches = matches && granted == expected.Granted && waiting == expected.Waiting
		}
		if matches {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("canonical lock topology not observed: %s", strings.Join(diagnostics, "; "))
		case <-time.After(10 * time.Millisecond):
		}
	}
	releaseGateOnce.Do(func() {
		var unlocked bool
		if err := observer.QueryRow(ctx, "SELECT pg_advisory_unlock(hashtextextended($1,0))", associationKey(c.ExternalGateAssociationID)).Scan(&unlocked); err != nil || !unlocked {
			t.Fatalf("release external reversed lock gate unlocked=%v err=%v", unlocked, err)
		}
	})
	primary, err := receivePublish(ctx, primaryOutcome)
	if err != nil {
		t.Fatal(err)
	}
	competing, err := receivePublish(ctx, competingOutcome)
	if err != nil {
		t.Fatal(err)
	}
	if primary.status != c.ExpectedPrimaryStatus || primary.body == "" {
		t.Fatalf("primary outcome status=%d body=%q", primary.status, primary.body)
	}
	if competing.status != c.ExpectedCompetingStatus || competing.body != c.ExpectedCompetingBody {
		t.Fatalf("competing outcome status=%d body=%q, want %d/%q", competing.status, competing.body, c.ExpectedCompetingStatus, c.ExpectedCompetingBody)
	}
	winner := assertSuccessfulPublish(t, ctx, pool, s3, owner, c.LocalID, c.PrimaryContent, c.Associations, len(c.Associations), primary)
	s3.mu.Lock()
	blobCount := len(s3.blobs)
	if blobCount != 1 {
		s3.mu.Unlock()
		t.Fatalf("recording transcript objects=%d, want exactly one winning object", blobCount)
	}
	_, winnerKeyExists := s3.blobs[winner.PersistedBlobKey]
	s3.mu.Unlock()
	if !winnerKeyExists {
		t.Fatalf("recording transcript store sole object does not use winning key %q", winner.PersistedBlobKey)
	}
	var loserRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcripts WHERE owner_id=$1 AND local_id=$2`, owner, c.CompetingLocalID).Scan(&loserRows); err != nil || loserRows != 0 {
		t.Fatalf("loser transcript rows=%d err=%v", loserRows, err)
	}
	if err := closeObserver(ctx); err != nil {
		t.Fatalf("close independent reversed lock observer after checked unlock: %v", err)
	}
}

func TestAssociationAnnotationIngressCanceledPublishLockAndTransactionFailure(t *testing.T) {
	c := loadPublishLockCases(t)["canceled association publish"]
	pool := publishLockPool(t, 4)
	defer pool.Close()
	s3 := newGatedRecordingTranscriptBlobStore()
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: s3}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := associationFixtureUser(t, ctx, pool, 64, "canceled")
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanupOwners(t, cleanupCtx, pool, owner)
	}()

	winnerPublish, err := preparePublish(owner, "canceled", c.LocalID, fixtureAssociations(c), c.PrimaryContent)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, cancelWait := context.WithCancel(ctx)
	waiterPublish, err := preparePublish(owner, "canceled", c.CompetingLocalID, fixtureAssociations(c), c.CompetingContent)
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	workers := newMountedPublishWorkers(ctx, cancelWait, func() { releaseOnce.Do(func() { close(s3.release) }) })
	defer workers.cleanup(t)
	winner := workers.start(func(workerCtx context.Context) publishOutcome { return winnerPublish(workerCtx, h) })
	select {
	case <-s3.entered:
	case <-ctx.Done():
		t.Fatal("winner did not acquire its lock and reach encrypted transcript storage")
	}
	waiter := workers.start(func(context.Context) publishOutcome { return waiterPublish(waitCtx, h) })
	if err := awaitAdvisoryWait(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cancelWait()
	canceled, err := receivePublish(ctx, waiter)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.status != c.ExpectedCompetingStatus || canceled.body != c.ExpectedCompetingBody {
		t.Fatalf("canceled mounted outcome=%d/%q, want %d/%q", canceled.status, canceled.body, c.ExpectedCompetingStatus, c.ExpectedCompetingBody)
	}
	var canceledRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcripts WHERE owner_id=$1 AND local_id=$2`, owner, c.CompetingLocalID).Scan(&canceledRows); err != nil || canceledRows != 0 {
		t.Fatalf("canceled transcript rows=%d err=%v", canceledRows, err)
	}
	releaseOnce.Do(func() { close(s3.release) })
	winnerResult, err := receivePublish(ctx, winner)
	if err != nil {
		t.Fatal(err)
	}
	if winnerResult.status != c.ExpectedPrimaryStatus {
		t.Fatalf("winner status=%d body=%q", winnerResult.status, winnerResult.body)
	}
	winnerResponse := assertSuccessfulPublish(t, ctx, pool, s3.recordingTranscriptBlobStore, owner, c.LocalID, c.PrimaryContent, c.Associations, len(c.Associations), winnerResult)
	observer, err := pgx.Connect(ctx, pullTestDatabaseURL(t))
	if err != nil {
		t.Fatalf("connect independent cancellation lock observer: %v", err)
	}
	defer func() { _ = observer.Close(context.Background()) }()
	for _, key := range append(publishLockKeys(owner, c.LocalID, fixtureAssociations(c)), publishLockKeys(owner, c.CompetingLocalID, fixtureAssociations(c))...) {
		var available bool
		if err := observer.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", key).Scan(&available); err != nil || !available {
			t.Fatalf("publish lock %q reusable=%v err=%v", key, available, err)
		}
		if _, err := observer.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1,0))", key); err != nil {
			t.Fatal(err)
		}
	}
	if err := observer.Close(ctx); err != nil {
		t.Fatalf("close independent cancellation lock observer before mounted retry: %v", err)
	}
	close(s3.updateRelease)
	retry, err := preparePublish(owner, "canceled", c.LocalID, fixtureAssociations(c), c.PrimaryContent)
	if err != nil {
		t.Fatal(err)
	}
	result := retry(ctx, h)
	if result.status != http.StatusOK {
		t.Fatalf("mounted recovery status=%d body=%q, want 200", result.status, result.body)
	}
	retryResponse := assertSuccessfulPublish(t, ctx, pool, s3.recordingTranscriptBlobStore, owner, c.LocalID, c.PrimaryContent, c.Associations, len(c.Associations), result)
	if retryResponse.Transcript.ID != winnerResponse.Transcript.ID || retryResponse.Transcript.BlobKey != winnerResponse.Transcript.BlobKey {
		t.Fatalf("cancellation retry changed transcript identity: winner=%+v retry=%+v", winnerResponse.Transcript, retryResponse.Transcript)
	}
}

func TestAssociationAnnotationIngressPostInsertRollbackLockRecovery(t *testing.T) {
	c := loadPublishLockCases(t)["post insert rollback"]
	pool := publishLockPool(t, 1)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := associationFixtureUser(t, ctx, pool, 65, "rollback-lock")
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		cleanupOwners(t, cleanupCtx, pool, owner)
	}()
	removeTrigger := installAssociationInsertFailureTrigger(t, ctx, pool)
	defer removeTrigger()
	s3 := newRecordingTranscriptBlobStore()
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: s3}
	var lifecycleLogs bytes.Buffer
	priorLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lifecycleLogs, nil)))
	t.Cleanup(func() { slog.SetDefault(priorLogger) })
	failing, err := preparePublish(owner, "rollback-lock", c.LocalID, fixtureAssociations(c), c.PrimaryContent)
	if err != nil {
		t.Fatal(err)
	}
	workers := newMountedPublishWorkers(ctx)
	defer workers.cleanup(t)
	outcome := workers.start(func(workerCtx context.Context) publishOutcome { return failing(workerCtx, h) })
	failed, err := receivePublish(ctx, outcome)
	if err != nil {
		t.Fatal(err)
	}
	if failed.status != c.ExpectedPrimaryStatus || failed.body != c.ExpectedPrimaryBody {
		t.Fatalf("rollback outcome=%d/%q, want %d/%q", failed.status, failed.body, c.ExpectedPrimaryStatus, c.ExpectedPrimaryBody)
	}
	var transcripts, associations, audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcripts WHERE owner_id=$1 AND local_id=$2`, owner, c.LocalID).Scan(&transcripts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_associations WHERE owner_id=$1`, owner).Scan(&associations); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_governance_events_audit WHERE changed_by=$1`, owner).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if transcripts != 0 || associations != 0 || audits != 0 {
		t.Fatalf("rollback persisted transcripts=%d associations=%d audits=%d", transcripts, associations, audits)
	}
	if retained := s3.count(); retained != 0 {
		t.Fatalf("known-rollback mounted publish retained %d candidate objects, want candidate cleanup", retained)
	}
	if strings.Contains(lifecycleLogs.String(), "transcript_blob_reconciliation_required") {
		t.Fatalf("known-rollback mounted publish emitted ambiguous reconciliation evidence: %q", lifecycleLogs.String())
	}
	observer, err := pgx.Connect(ctx, pullTestDatabaseURL(t))
	if err != nil {
		t.Fatalf("connect independent rollback lock observer: %v", err)
	}
	defer func() { _ = observer.Close(context.Background()) }()
	for _, key := range publishLockKeys(owner, c.LocalID, fixtureAssociations(c)) {
		var available bool
		if err := observer.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", key).Scan(&available); err != nil || !available {
			t.Fatalf("rollback lock %q reusable=%v err=%v", key, available, err)
		}
		if _, err := observer.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1,0))", key); err != nil {
			t.Fatal(err)
		}
	}
	if err := observer.Close(ctx); err != nil {
		t.Fatalf("close independent rollback lock observer before mounted recovery: %v", err)
	}
	removeTrigger()
	recoveryAssociations := fixtureRecoveryAssociations(c)
	recovery, err := preparePublish(owner, "rollback-lock", c.CompetingLocalID, recoveryAssociations, c.CompetingContent)
	if err != nil {
		t.Fatal(err)
	}
	recovered := recovery(ctx, h)
	if recovered.status != c.ExpectedCompetingStatus {
		t.Fatalf("rollback recovery status=%d body=%q, want %d", recovered.status, recovered.body, c.ExpectedCompetingStatus)
	}
	assertSuccessfulPublish(t, ctx, pool, s3, owner, c.CompetingLocalID, c.CompetingContent, c.RecoveryAssociations, len(c.RecoveryAssociations), recovered)
}
