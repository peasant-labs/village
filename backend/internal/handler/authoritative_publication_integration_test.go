//go:build integration

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

func TestAuthoritativePublicationMountedIntegration(t *testing.T) {
	fixture := loadAuthoritativePublicationFixture(t)
	pool := publishLockPool(t, 8)
	ctx := context.Background()
	owner := pullInsertUser(t, ctx, pool, 991031, "authoritative-publisher")
	defer cleanupOwners(t, ctx, pool, owner)
	blobs := authoritativeTestBlobStore(t)
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct title pipeline: %v", err)
	}
	guardedBlobs := &stagingProbeBlobStore{TranscriptBlobStore: blobs, plaintext: make(map[string][]byte), descriptors: make(map[string]storage.BlobDescriptor)}
	h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: guardedBlobs, titles: titles, cfg: &config.Config{FrontendURL: "https://village.example"}}
	localID := "550e8400-e29b-41d4-a716-446655440031"
	user := &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "authoritative-publisher"}

	created := mountedAuthoritativePublish(t, h, user, localID, fixture.Publish[0], false)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var createResponse schema.AuthoritativePublishResponse
	if err := json.Unmarshal(created.Body.Bytes(), &createResponse); err != nil {
		t.Fatal(err)
	}
	if err := createResponse.Validate(); err != nil {
		t.Fatal(err)
	}
	tid := toPgUUID(uuid.MustParse(createResponse.TranscriptID.String()))
	defer func() {
		row, err := sqlc.New(pool).GetTranscriptByID(ctx, tid)
		if err == nil {
			if descriptor, descriptorErr := descriptorFromTranscript(row); descriptorErr == nil {
				_ = blobs.Delete(ctx, descriptor)
			}
		}
	}()
	assertPersistedPublicationCurrency(t, ctx, pool, tid, createResponse)
	assertParentAwarePublication(t, ctx, pool, tid, localID, fixture.Publish[0], createResponse)

	updated := mountedAuthoritativePublish(t, h, user, localID, fixture.Publish[1], false)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updated.Code, updated.Body.String())
	}
	var updateResponse schema.AuthoritativePublishResponse
	if err := json.Unmarshal(updated.Body.Bytes(), &updateResponse); err != nil {
		t.Fatal(err)
	}
	if len(updateResponse.Applied.Associations) != 2 {
		t.Fatalf("complete associations=%+v", updateResponse.Applied.Associations)
	}
	assertPersistedPublicationCurrency(t, ctx, pool, tid, updateResponse)
	assertParentAwarePublication(t, ctx, pool, tid, localID, fixture.Publish[1], updateResponse)
	replayed := mountedAuthoritativePublish(t, h, user, localID, fixture.Publish[1], false)
	if replayed.Code != http.StatusOK {
		t.Fatalf("exact replay status=%d body=%s", replayed.Code, replayed.Body.String())
	}
	var replayResponse schema.AuthoritativePublishResponse
	if err := json.Unmarshal(replayed.Body.Bytes(), &replayResponse); err != nil {
		t.Fatal(err)
	}
	if replayResponse.RequestOperationFingerprint != updateResponse.RequestOperationFingerprint || len(replayResponse.Applied.Associations) != 2 {
		t.Fatalf("exact replay receipt=%+v", replayResponse)
	}
	if replayResponse.BlobKey != updateResponse.BlobKey {
		t.Fatalf("exact replay blob key=%q want current key %q", replayResponse.BlobKey, updateResponse.BlobKey)
	}
	assertParentAwarePublication(t, ctx, pool, tid, localID, fixture.Publish[1], replayResponse)
	currentBytes := guardedBlobs.plaintextBytes(t, updateResponse.BlobKey)
	durableKeys := guardedBlobs.keys()
	if len(durableKeys) != 1 || durableKeys[0] != updateResponse.BlobKey {
		t.Fatalf("durable encrypted object set after replacement=%v, want only current key %q", durableKeys, updateResponse.BlobKey)
	}
	currentFingerprint := updateResponse.RequestOperationFingerprint.String()
	wantParent := fixture.Publish[0].ParentSessionID

	for index := 2; index <= 4; index++ {
		response := mountedAuthoritativePublish(t, h, user, localID, fixture.Publish[index], index == 2)
		if response.Code != fixture.Publish[index].WantStatus {
			t.Fatalf("%s status=%d body=%s", fixture.Publish[index].Name, response.Code, response.Body.String())
		}
		assertUnchangedPublication(t, ctx, pool, guardedBlobs, tid, updateResponse.BlobKey, currentBytes, currentFingerprint, wantParent, 2)
	}

	if err := h.inTxAs(ctx, owner, func(q Querier) error {
		public := dbVisibilityPublic
		_, err := applyMetadataPatch(ctx, q, tid, metadataPatch{Visibility: &public})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	guardedBlobs.observePool = pool
	guardedBlobs.observeTranscript = tid
	guardedBlobs.expectedOwner = owner
	if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(seq), 0) FROM transcript_governance_events_audit WHERE transcript_id=$1`, tid).Scan(&guardedBlobs.priorAuditSeq); err != nil {
		t.Fatal(err)
	}
	guardedBlobs.observeNext.Store(true)
	installFingerprintFailureTrigger(t, ctx, pool, tid)
	failed := mountedAuthoritativePublish(t, h, user, localID, fixture.Publish[5], false)
	removeFingerprintFailureTrigger(t, ctx, pool)
	if failed.Code != fixture.Publish[5].WantStatus {
		t.Fatalf("staged replacement failure status=%d body=%s", failed.Code, failed.Body.String())
	}
	assertUnchangedPublication(t, ctx, pool, guardedBlobs, tid, updateResponse.BlobKey, currentBytes, currentFingerprint, wantParent, 2)
	if got := guardedBlobs.keys(); !slices.Equal(got, durableKeys) {
		t.Fatalf("transaction failure retained staged object: keys=%v want durable set %v", got, durableKeys)
	}
	var visibility string
	if err := pool.QueryRow(ctx, `SELECT visibility FROM transcripts WHERE id=$1`, tid).Scan(&visibility); err != nil {
		t.Fatal(err)
	}
	if visibility != dbVisibilityPrivate {
		t.Fatalf("visibility after failed replacement=%q want private", visibility)
	}
	if !guardedBlobs.observedPrivate.Load() {
		t.Fatal("S3 replacement began before the widened transcript was durably private")
	}
	if !guardedBlobs.observedNarrowingAudit.Load() {
		t.Fatal("S3 replacement began before the trigger-written owner-attributed narrowing audit was durable")
	}

	guardedBlobs.failNextDelete.Store(true)
	installFingerprintFailureTrigger(t, ctx, pool, tid)
	cleanupFailed := mountedAuthoritativePublish(t, h, user, localID, fixture.Publish[6], false)
	orphanKey := guardedBlobs.lastWrittenKey()
	t.Cleanup(func() {
		if err := guardedBlobs.deleteRecordedIfPresent(context.Background(), orphanKey); err != nil {
			t.Errorf("cleanup authoritative orphan %q: %v", orphanKey, err)
		}
	})
	removeFingerprintFailureTrigger(t, ctx, pool)
	responseBody := cleanupFailed.Body.String()
	if cleanupFailed.Code != fixture.Publish[6].WantStatus || orphanKey == "" || !strings.Contains(responseBody, orphanKey) || !strings.Contains(responseBody, "operator may remove the orphaned staged object") {
		t.Fatalf("staged cleanup failure status=%d body=%s", cleanupFailed.Code, cleanupFailed.Body.String())
	}
	assertUnchangedPublication(t, ctx, pool, guardedBlobs, tid, updateResponse.BlobKey, currentBytes, currentFingerprint, wantParent, 2)
	wantKeys := append(append([]string(nil), durableKeys...), orphanKey)
	slices.Sort(wantKeys)
	if got := guardedBlobs.keys(); !slices.Equal(got, wantKeys) {
		t.Fatalf("cleanup failure object evidence=%v, want durable key plus exactly orphan %v", got, wantKeys)
	}
	if err := guardedBlobs.deleteRecorded(ctx, orphanKey); err != nil {
		t.Fatalf("remove intentionally orphaned integration object after evidence: %v", err)
	}
	var fingerprint string
	if err := pool.QueryRow(ctx, `SELECT accepted_request_operation_fingerprint FROM transcripts WHERE id=$1`, tid).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}
	if fingerprint != currentFingerprint {
		t.Fatalf("fingerprint changed after staged cleanup failure: %q", fingerprint)
	}

	assertMutationHandlersWaitForSessionLock(t, ctx, pool, h, user, tid, localID)
	guardedBlobs.failNextDelete.Store(true)
	installBlobReplacementFailureTrigger(t, ctx, pool, tid)
	legacyCleanupFailed := mountedLegacyPublish(t, h, user, localID, fixture.LegacyUpdate)
	legacyOrphanKey := guardedBlobs.lastWrittenKey()
	t.Cleanup(func() {
		if err := guardedBlobs.deleteRecordedIfPresent(context.Background(), legacyOrphanKey); err != nil {
			t.Errorf("cleanup legacy orphan: %v", err)
		}
	})
	removeBlobReplacementFailureTrigger(t, ctx, pool)
	legacyFailureBody := legacyCleanupFailed.Body.String()
	if legacyCleanupFailed.Code != http.StatusInternalServerError || strings.Contains(legacyFailureBody, legacyOrphanKey) || strings.Contains(legacyFailureBody, "transcripts/") || strings.Contains(legacyFailureBody, ".bin") || !strings.Contains(legacyFailureBody, "staged-object cleanup evidence was logged") || !strings.Contains(legacyFailureBody, "operator cleanup") || !strings.Contains(legacyFailureBody, "publish retry") {
		t.Fatalf("legacy cleanup failure mapping status=%d body=%q orphan=%q", legacyCleanupFailed.Code, legacyFailureBody, legacyOrphanKey)
	}
	legacyWantKeys := append(append([]string(nil), durableKeys...), legacyOrphanKey)
	slices.Sort(legacyWantKeys)
	if got := guardedBlobs.keys(); !slices.Equal(got, legacyWantKeys) {
		t.Fatalf("legacy cleanup failure object evidence=%v, want durable key plus exactly orphan %v", got, legacyWantKeys)
	}
	if err := guardedBlobs.deleteRecorded(ctx, legacyOrphanKey); err != nil {
		t.Fatalf("remove intentionally orphaned legacy integration object after evidence: %v", err)
	}
	legacy := mountedLegacyPublish(t, h, user, localID, fixture.LegacyUpdate)
	if legacy.Code != fixture.LegacyUpdate.WantStatus {
		t.Fatalf("legacy update status=%d body=%s", legacy.Code, legacy.Body.String())
	}
	var cleared *string
	var legacyHash string
	if err := pool.QueryRow(ctx, `SELECT accepted_request_operation_fingerprint, content_hash FROM transcripts WHERE id=$1`, tid).Scan(&cleared, &legacyHash); err != nil {
		t.Fatal(err)
	}
	if cleared != nil || legacyHash != schema.ComputeTranscriptContentHash([]byte(fixture.LegacyUpdate.Content)).String() {
		t.Fatalf("legacy currency fingerprint=%v hash=%q", cleared, legacyHash)
	}
	if err := h.inTxAs(ctx, owner, func(q Querier) error {
		shared := dbVisibilityShared
		_, err := applyMetadataPatch(ctx, q, tid, metadataPatch{Visibility: &shared})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var titleBefore pgtype.Text
	var auditBefore int
	if err := pool.QueryRow(ctx, `SELECT title FROM transcripts WHERE id=$1`, tid).Scan(&titleBefore); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_governance_events_audit WHERE transcript_id=$1`, tid).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	refused := mountedOwnerPatchBody(h, user, tid, `{"title":"must not persist"}`)
	if refused.Code != http.StatusBadRequest {
		t.Fatalf("shared preimage refusal status=%d body=%s", refused.Code, refused.Body.String())
	}
	var titleAfter pgtype.Text
	var auditAfter int
	if err := pool.QueryRow(ctx, `SELECT title FROM transcripts WHERE id=$1`, tid).Scan(&titleAfter); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_governance_events_audit WHERE transcript_id=$1`, tid).Scan(&auditAfter); err != nil {
		t.Fatal(err)
	}
	if titleAfter != titleBefore || auditAfter != auditBefore {
		t.Fatalf("refused shared PATCH changed state: title before=%+v after=%+v audit before=%d after=%d", titleBefore, titleAfter, auditBefore, auditAfter)
	}
}

type stagingProbeBlobStore struct {
	storage.TranscriptBlobStore
	mu                     sync.Mutex
	plaintext              map[string][]byte
	descriptors            map[string]storage.BlobDescriptor
	lastWritten            string
	failNextDelete         atomic.Bool
	observeNext            atomic.Bool
	observedPrivate        atomic.Bool
	observedNarrowingAudit atomic.Bool
	observePool            *pgxpool.Pool
	observeTranscript      pgtype.UUID
	expectedOwner          pgtype.UUID
	priorAuditSeq          int64
}

func (s *stagingProbeBlobStore) Write(ctx context.Context, id uuid.UUID, contents []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	if s.observeNext.CompareAndSwap(true, false) {
		var visibility string
		if s.observePool.QueryRow(ctx, `SELECT visibility FROM transcripts WHERE id=$1`, s.observeTranscript).Scan(&visibility) == nil && visibility == dbVisibilityPrivate {
			s.observedPrivate.Store(true)
		}
		var auditCount int
		if s.observePool.QueryRow(ctx, `SELECT count(*) FROM transcript_governance_events_audit WHERE transcript_id=$1 AND event_type IN ('visibility_changed','governance_changed') AND visibility='private' AND changed_by=$2 AND seq>$3`, s.observeTranscript, s.expectedOwner, s.priorAuditSeq).Scan(&auditCount) == nil && auditCount > 0 {
			s.observedNarrowingAudit.Store(true)
		}
	}
	descriptor, identity, err := s.TranscriptBlobStore.Write(ctx, id, contents)
	if err == nil {
		s.mu.Lock()
		key := string(descriptor.ObjectKey())
		s.plaintext[key] = append([]byte(nil), contents...)
		s.descriptors[key] = descriptor
		s.lastWritten = key
		s.mu.Unlock()
	}
	return descriptor, identity, err
}

func (s *stagingProbeBlobStore) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.plaintext))
	for key := range s.plaintext {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func (s *stagingProbeBlobStore) lastWrittenKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastWritten
}

func (s *stagingProbeBlobStore) Delete(ctx context.Context, descriptor storage.BlobDescriptor) error {
	if s.failNextDelete.CompareAndSwap(true, false) {
		return io.ErrUnexpectedEOF
	}
	err := s.TranscriptBlobStore.Delete(ctx, descriptor)
	if err == nil {
		s.mu.Lock()
		delete(s.plaintext, string(descriptor.ObjectKey()))
		delete(s.descriptors, string(descriptor.ObjectKey()))
		s.mu.Unlock()
	}
	return err
}

func (s *stagingProbeBlobStore) deleteRecordedIfPresent(ctx context.Context, key string) error {
	s.mu.Lock()
	descriptor, ok := s.descriptors[key]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return s.Delete(ctx, descriptor)
}

func (s *stagingProbeBlobStore) deleteRecorded(ctx context.Context, key string) error {
	s.mu.Lock()
	descriptor, ok := s.descriptors[key]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("explicit staged-object evidence cleanup cannot delete key %q because its recorded descriptor is absent; preserve the failure evidence and inspect the probe lifecycle before retrying cleanup", key)
	}
	return s.Delete(ctx, descriptor)
}

func (s *stagingProbeBlobStore) plaintextBytes(t *testing.T, key string) []byte {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	contents, ok := s.plaintext[key]
	if !ok {
		t.Fatalf("recorded encrypted-store plaintext is absent for authoritative locator %q", key)
	}
	return append([]byte(nil), contents...)
}

func authoritativeTestBlobStore(t *testing.T) storage.TranscriptBlobStore {
	t.Helper()
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI must configure TEST_S3_ENDPOINT")
		}
		t.Skip("set TEST_S3_ENDPOINT for mounted MinIO evidence")
	}
	cfg := &config.Config{S3Endpoint: endpoint, S3AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"), S3SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), S3Bucket: os.Getenv("TEST_S3_BUCKET"), S3UsePathStyle: true}
	objects, err := storage.NewS3ObjectStore(cfg)
	if err != nil {
		t.Fatalf("compose authoritative publication object store: %v", err)
	}
	keyring, err := config.ParseTranscriptKeyring(os.Getenv("TRANSCRIPT_KEK_ACTIVE_VERSION"), os.Getenv("TRANSCRIPT_KEK_KEYRING"))
	if err != nil {
		t.Fatalf("load authoritative publication test keyring: %v", err)
	}
	blobs, err := storage.NewEncryptedTranscriptStore(objects, keyring)
	if err != nil {
		t.Fatalf("compose authoritative publication encrypted blob store: %v", err)
	}
	return blobs
}

func mountedAuthoritativePublish(t *testing.T, h *Handler, user *AuthUser, localID string, row authoritativePublishCase, mismatch bool) *httptest.ResponseRecorder {
	t.Helper()
	req := authoritativePublishRequest(t, localID, row, mismatch)
	metadata, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadata)}, row.Content)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, user))
	w := httptest.NewRecorder()
	h.PublishTranscript(w, r)
	return w
}

func authoritativePublishRequest(t *testing.T, localID string, row authoritativePublishCase, mismatch bool) schema.AuthoritativePublishRequest {
	t.Helper()
	id, err := schema.NewAssociationID(row.AssociationID)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(row.Content)
	hash := schema.ComputeTranscriptContentHash(content)
	if mismatch {
		hash = schema.ComputeTranscriptContentHash([]byte("different"))
	}
	identity := schema.AuthoritativeSessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2}
	if row.ParentSessionID != "" {
		parent := schema.SessionID(row.ParentSessionID)
		identity.ParentSessionID = &parent
	}
	return schema.AuthoritativePublishRequest{Identity: identity, Model: schema.AuthoritativeModelInfo{Harness: schema.HarnessClaudeCode, Model: "fixture-model"}, Timestamp: schema.AuthoritativeTimestampInfo{Start: 1700000000000, End: 1700000001000}, Source: schema.AuthoritativeSourceInfo{FilePath: "/fixture/session.jsonl", Format: schema.SourceFormatJSONL}, Project: schema.AuthoritativeProjectContext{Hash: schema.ProjectHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), Name: "fixture"}, Git: schema.AuthoritativeGitContext{Associations: []schema.PublishedAssociation{{ID: id, ObservedCommitHash: row.ObservedCommitHash}}}, ContentHash: hash, VisibilityIntent: schema.VisibilityIntentPrivate}
}

func mountedLegacyPublish(t *testing.T, h *Handler, user *AuthUser, localID string, row authoritativeLegacyCase) *httptest.ResponseRecorder {
	t.Helper()
	req := schema.PublishRequest{Identity: schema.SessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2}, Model: schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "fixture-model"}, Timestamp: schema.TimestampInfo{Start: 1700000000000, End: 1700000001000}, Source: schema.SourceInfo{FilePath: "/fixture/session.jsonl", Format: schema.SourceFormatJSONL}, Project: schema.ProjectContext{Hash: schema.ProjectHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), Name: "fixture"}}
	metadata, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadata)}, row.Content)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, user))
	w := httptest.NewRecorder()
	h.PublishTranscript(w, r)
	return w
}

func assertPersistedPublicationCurrency(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tid pgtype.UUID, response schema.AuthoritativePublishResponse) {
	t.Helper()
	var fingerprint, hash string
	if err := pool.QueryRow(ctx, `SELECT accepted_request_operation_fingerprint, content_hash FROM transcripts WHERE id=$1`, tid).Scan(&fingerprint, &hash); err != nil {
		t.Fatal(err)
	}
	if fingerprint != response.RequestOperationFingerprint.String() || hash != response.ContentHash.String() {
		t.Fatalf("persisted currency fingerprint=%q hash=%q response=%+v", fingerprint, hash, response)
	}
}

func assertParentAwarePublication(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tid pgtype.UUID, localID string, row authoritativePublishCase, response schema.AuthoritativePublishResponse) {
	t.Helper()
	request := authoritativePublishRequest(t, localID, row, false)
	operation, err := schema.CanonicalizePublishRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := schema.FingerprintPublishOperation(operation)
	if err != nil {
		t.Fatal(err)
	}
	rootRequest := request
	rootRequest.Identity.ParentSessionID = nil
	rootOperation, err := schema.CanonicalizePublishRequest(rootRequest)
	if err != nil {
		t.Fatal(err)
	}
	rootFingerprint, err := schema.FingerprintPublishOperation(rootOperation)
	if err != nil {
		t.Fatal(err)
	}
	if want == rootFingerprint {
		t.Fatalf("parent-aware fingerprint %q equals otherwise-identical root fingerprint", want)
	}
	var persistedFingerprint string
	var persistedParent pgtype.Text
	if err := pool.QueryRow(ctx, `SELECT accepted_request_operation_fingerprint, parent_session_id FROM transcripts WHERE id=$1`, tid).Scan(&persistedFingerprint, &persistedParent); err != nil {
		t.Fatal(err)
	}
	if response.RequestOperationFingerprint != want || persistedFingerprint != want.String() {
		t.Fatalf("parent-aware fingerprint receipt=%q persisted=%q canonical=%q root=%q", response.RequestOperationFingerprint, persistedFingerprint, want, rootFingerprint)
	}
	if !persistedParent.Valid || persistedParent.String != row.ParentSessionID {
		t.Fatalf("persisted parent=%+v want exact fixture parent %q", persistedParent, row.ParentSessionID)
	}
	t.Logf("parent-aware fingerprint=%s root-fingerprint=%s persisted-parent=%s", want, rootFingerprint, persistedParent.String)
}

func assertUnchangedPublication(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blobs *stagingProbeBlobStore, tid pgtype.UUID, key string, wantBytes []byte, wantFingerprint, wantParent string, wantAssociations int) {
	t.Helper()
	var fingerprint string
	var parent pgtype.Text
	var count int
	if err := pool.QueryRow(ctx, `SELECT accepted_request_operation_fingerprint, parent_session_id FROM transcripts WHERE id=$1`, tid).Scan(&fingerprint, &parent); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_associations WHERE transcript_id=$1`, tid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if fingerprint != wantFingerprint || !parent.Valid || parent.String != wantParent || count != wantAssociations || !bytes.Equal(blobs.plaintextBytes(t, key), wantBytes) {
		t.Fatalf("publication changed after rejection: fingerprint=%q parent=%+v associations=%d", fingerprint, parent, count)
	}
}

func installFingerprintFailureTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tid pgtype.UUID) {
	t.Helper()
	t.Cleanup(func() { removeFingerprintFailureTrigger(t, context.Background(), pool) })
	sql := `CREATE OR REPLACE FUNCTION reject_test_fingerprint() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id = '` + uuid.UUID(tid.Bytes).String() + `'::uuid AND NEW.accepted_request_operation_fingerprint IS NOT NULL THEN RAISE EXCEPTION 'injected fingerprint failure'; END IF; RETURN NEW; END $$; DROP TRIGGER IF EXISTS reject_test_fingerprint ON transcripts; CREATE TRIGGER reject_test_fingerprint BEFORE UPDATE OF accepted_request_operation_fingerprint ON transcripts FOR EACH ROW EXECUTE FUNCTION reject_test_fingerprint()`
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
}
func removeFingerprintFailureTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DROP TRIGGER IF EXISTS reject_test_fingerprint ON transcripts; DROP FUNCTION IF EXISTS reject_test_fingerprint()`); err != nil {
		t.Fatal(err)
	}
}

func installBlobReplacementFailureTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tid pgtype.UUID) {
	t.Helper()
	t.Cleanup(func() { removeBlobReplacementFailureTrigger(t, context.Background(), pool) })
	sql := `CREATE OR REPLACE FUNCTION reject_test_blob_replacement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id = '` + uuid.UUID(tid.Bytes).String() + `'::uuid AND NEW.blob_key IS DISTINCT FROM OLD.blob_key THEN RAISE EXCEPTION 'injected blob replacement failure'; END IF; RETURN NEW; END $$; DROP TRIGGER IF EXISTS reject_test_blob_replacement ON transcripts; CREATE TRIGGER reject_test_blob_replacement BEFORE UPDATE OF blob_key ON transcripts FOR EACH ROW EXECUTE FUNCTION reject_test_blob_replacement()`
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
}

func removeBlobReplacementFailureTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DROP TRIGGER IF EXISTS reject_test_blob_replacement ON transcripts; DROP FUNCTION IF EXISTS reject_test_blob_replacement()`); err != nil {
		t.Fatal(err)
	}
}

func assertMutationHandlersWaitForSessionLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, h *Handler, user *AuthUser, tid pgtype.UUID, localID string) {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	key := publishLockKeys(user.PgID(), localID, nil)[0]
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key)
	done := make(chan int, 1)
	go func() { done <- mountedOwnerPatch(h, user, tid) }()
	select {
	case <-done:
		t.Fatal("owner PATCH escaped the session advisory lock")
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key); err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-done:
		if status != http.StatusOK {
			t.Fatalf("owner PATCH status=%d", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner PATCH did not resume after advisory unlock")
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key); err != nil {
		t.Fatal(err)
	}
	done = make(chan int, 1)
	go func() { done <- mountedShareProbe(h, user, tid) }()
	select {
	case <-done:
		t.Fatal("share escaped the session advisory lock")
	case <-time.After(150 * time.Millisecond):
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key); err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-done:
		if status != http.StatusOK {
			t.Fatalf("share status=%d", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("share did not resume after advisory unlock")
	}
}

func mountedOwnerPatch(h *Handler, user *AuthUser, tid pgtype.UUID) int {
	return mountedOwnerPatchBody(h, user, tid, `{"visibility":"public"}`).Code
}

func mountedOwnerPatchBody(h *Handler, user *AuthUser, tid pgtype.UUID, body string) *httptest.ResponseRecorder {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuid.UUID(tid.Bytes).String())
	ctx := context.WithValue(context.Background(), UserContextKey, user)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/transcripts/"+uuid.UUID(tid.Bytes).String(), bytes.NewBufferString(body)).WithContext(ctx)
	w := httptest.NewRecorder()
	h.UpdateTranscript(w, r)
	return w
}
func mountedShareProbe(h *Handler, user *AuthUser, tid pgtype.UUID) int {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuid.UUID(tid.Bytes).String())
	ctx := context.WithValue(context.Background(), UserContextKey, user)
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/"+uuid.UUID(tid.Bytes).String()+"/share", bytes.NewBufferString(`{"group_ids":[]}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ShareTranscript(w, r)
	return w.Code
}
