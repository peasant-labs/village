//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/publish-evidence.yaml
var publishEvidenceYAML []byte

type publishEvidenceStep struct {
	Action            string `yaml:"action"`
	Content           string `yaml:"content"`
	ContentFixture    string `yaml:"content_fixture"`
	Client            string `yaml:"client"`
	WantStatus        int    `yaml:"want_status"`
	WantWrites        int64  `yaml:"want_writes"`
	WantNewGeneration bool   `yaml:"want_new_generation"`
}

type publishEvidenceRewrite struct {
	ReplacedStep int  `yaml:"replaced_step"`
	Legacy       bool `yaml:"legacy"`
}

type publishEvidenceCase struct {
	Name             string                  `yaml:"name"`
	Why              string                  `yaml:"why"`
	Fault            string                  `yaml:"fault"`
	Steps            []publishEvidenceStep   `yaml:"steps"`
	WantMetadataStep *int                    `yaml:"want_metadata_step"`
	WantDeleted      bool                    `yaml:"want_deleted"`
	WantRewrite      *publishEvidenceRewrite `yaml:"want_rewrite"`
}

var requiredPublishEvidenceCases = []string{
	"authoritative_publish_stores_metadata",
	"client_is_inside_the_stored_metadata",
	"legacy_publish_stores_none",
	"client_only_republish_is_a_replay",
	"republish_replaces_metadata_and_deletes_the_old_object",
	"legacy_republish_clears_metadata",
	"fingerprint_replay_writes_nothing",
	"delete_removes_both_objects",
	"rewrite_reencrypts_metadata_under_the_new_key",
	"rewrite_records_the_replaced_hash",
	"rewrite_of_raw_jsonl_is_legacy",
	"orphaned_body_on_metadata_failure_emits_reconciliation",
}

func loadPublishEvidenceCases(t *testing.T) []publishEvidenceCase {
	t.Helper()
	cases, err := decodeFixtureRows[publishEvidenceCase](publishEvidenceYAML)
	if err != nil {
		t.Fatalf("load the publish evidence corpus: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || c.Why == "" || present[c.Name] || len(c.Steps) == 0 {
			t.Fatalf("publish evidence case %+v must carry a unique name, a reason and steps", c)
		}
		present[c.Name] = true
	}
	for _, name := range requiredPublishEvidenceCases {
		if !present[name] {
			t.Fatalf("the publish evidence corpus no longer contains %q; restore it", name)
		}
	}
	return cases
}

// countingGenerationStore counts every generation the handlers ask for.
type countingGenerationStore struct {
	storage.TranscriptBlobStore
	writes atomic.Int64
}

func (s *countingGenerationStore) Write(ctx context.Context, id uuid.UUID, body []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	s.writes.Add(1)
	return s.TranscriptBlobStore.Write(ctx, id, body)
}

func (s *countingGenerationStore) WriteGeneration(ctx context.Context, id uuid.UUID, body, metadata []byte) (storage.BlobDescriptor, storage.ContentIdentity, *storage.MetadataObject, error) {
	s.writes.Add(1)
	return s.TranscriptBlobStore.WriteGeneration(ctx, id, body, metadata)
}

// metadataFaultObjectStore fails the second upload (the metadata object of
// the first generation) and every deletion, so the uploaded body is orphaned.
type metadataFaultObjectStore struct {
	storage.ObjectStore
	mu   sync.Mutex
	puts int
}

func (s *metadataFaultObjectStore) Put(ctx context.Context, key storage.ObjectKey, body []byte, contentType string) error {
	s.mu.Lock()
	s.puts++
	n := s.puts
	s.mu.Unlock()
	if n == 2 {
		return errors.New("fixture metadata upload refused")
	}
	return s.ObjectStore.Put(ctx, key, body, contentType)
}

func (*metadataFaultObjectStore) Delete(context.Context, storage.ObjectKey) error {
	return errors.New("fixture object deletion refused")
}

func publishEvidenceObjects(t *testing.T) storage.ObjectStore {
	t.Helper()
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI must configure TEST_S3_ENDPOINT")
		}
		t.Skip("set TEST_S3_ENDPOINT for mounted RustFS evidence")
	}
	cfg := &config.Config{S3Endpoint: endpoint, S3AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"), S3SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), S3Bucket: os.Getenv("TEST_S3_BUCKET"), S3UsePathStyle: true}
	objects, err := storage.NewS3ObjectStore(cfg)
	if err != nil {
		t.Fatalf("compose publish evidence object store: %v", err)
	}
	return objects
}

func publishEvidenceStore(t *testing.T, objects storage.ObjectStore) storage.TranscriptBlobStore {
	t.Helper()
	keyring, err := config.ParseTranscriptKeyring(os.Getenv("TRANSCRIPT_KEK_ACTIVE_VERSION"), os.Getenv("TRANSCRIPT_KEK_KEYRING"))
	if err != nil {
		t.Fatalf("load publish evidence keyring: %v", err)
	}
	blobs, err := storage.NewEncryptedTranscriptStore(objects, keyring)
	if err != nil {
		t.Fatalf("compose publish evidence encrypted store: %v", err)
	}
	return blobs
}

func publishEvidenceRequest(localID string, content []byte, client string) schema.AuthoritativePublishRequest {
	req := schema.AuthoritativePublishRequest{
		Identity:         schema.AuthoritativeSessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2},
		Model:            schema.AuthoritativeModelInfo{Harness: schema.HarnessClaudeCode, Model: "fixture-model"},
		Timestamp:        schema.AuthoritativeTimestampInfo{Start: 1700000000000, End: 1700000001000},
		Source:           schema.AuthoritativeSourceInfo{FilePath: "/fixture/session.jsonl", Format: schema.SourceFormatJSONL},
		Project:          schema.AuthoritativeProjectContext{Hash: schema.ProjectHash(testProjectHash), Name: "evidence"},
		ContentHash:      schema.ComputeTranscriptContentHash(content),
		VisibilityIntent: schema.VisibilityIntentPrivate,
	}
	if client != "" {
		name, version, _ := strings.Cut(client, " ")
		req.Client = &schema.AuthoritativeClientInfo{Name: name, Version: version}
	}
	return req
}

func publishEvidencePost(t *testing.T, h *Handler, user *AuthUser, metadata any, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(encoded)}, string(content))
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, user))
	w := httptest.NewRecorder()
	h.PublishTranscript(w, r)
	return w
}

type evidenceGeneration struct {
	row         sqlc.Transcript
	exists      bool
	metadataKey string
}

func objectExists(t *testing.T, ctx context.Context, objects storage.ObjectStore, key string) bool {
	t.Helper()
	_, _, err := objects.Get(ctx, storage.ObjectKey(key))
	if err == nil {
		return true
	}
	if errors.Is(err, storage.ErrObjectNotFound) {
		return false
	}
	t.Fatalf("read object %q from RustFS: %v", key, err)
	return false
}

func mustDescriptor(t *testing.T, row sqlc.Transcript) storage.BlobDescriptor {
	t.Helper()
	d, err := descriptorFromTranscript(row)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustLoadedIdentity(t *testing.T, row sqlc.Transcript) storage.LoadedContentIdentity {
	t.Helper()
	l, err := identityFromTranscript(row)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

var reconciliationObjectKey = regexp.MustCompile(`object_key=(transcripts/[0-9a-f-]{36}\.bin)`)

func TestPublishEvidenceLifecycle(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct title pipeline: %v", err)
	}
	rawObjects := publishEvidenceObjects(t)

	for ordinal, c := range loadPublishEvidenceCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			owner := pullInsertUser(t, ctx, pool, int64(994400+ordinal), "evidence-owner-"+uuid.NewString()[:8])
			defer cleanupOwners(t, context.Background(), pool, owner)

			var objects storage.ObjectStore = rawObjects
			switch c.Fault {
			case "":
			case "metadata_upload_and_cleanup":
				objects = &metadataFaultObjectStore{ObjectStore: rawObjects}
			default:
				t.Fatalf("unknown fault %q", c.Fault)
			}
			store := &countingGenerationStore{TranscriptBlobStore: publishEvidenceStore(t, objects)}
			reader := publishEvidenceStore(t, rawObjects)
			queries := sqlc.New(pool)
			h := &Handler{cfg: minimalConfig(), pool: pool, queries: queries, blobs: store, titles: titles}
			user := &AuthUser{ID: uuidFromPg(owner), Username: "evidence-owner"}
			localID := uuid.NewString()

			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(previous)

			load := func() evidenceGeneration {
				id, err := queries.GetTranscriptIDByOwnerAndLocalID(ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: owner, LocalID: localID})
				if errors.Is(err, pgx.ErrNoRows) {
					return evidenceGeneration{}
				}
				if err != nil {
					t.Fatalf("load transcript id: %v", err)
				}
				row, err := queries.GetTranscriptByID(ctx, id)
				if err != nil {
					t.Fatalf("load transcript: %v", err)
				}
				return evidenceGeneration{row: row, exists: true, metadataKey: row.PublishMetadataKey.String}
			}

			requests := make([][]byte, len(c.Steps))
			generations := make([]evidenceGeneration, len(c.Steps))
			seenKeys := map[string]bool{}
			before := evidenceGeneration{}
			for index, step := range c.Steps {
				content := []byte(step.Content)
				if step.ContentFixture == "legacy_bare_payload" {
					content = legacyBarePayloadJSON("claude")
				}
				writesBefore := store.writes.Load()
				var w *httptest.ResponseRecorder
				switch step.Action {
				case "publish_authoritative":
					req := publishEvidenceRequest(localID, content, step.Client)
					requests[index], err = json.Marshal(req)
					if err != nil {
						t.Fatal(err)
					}
					w = publishEvidencePost(t, h, user, req, content)
				case "publish_legacy":
					req := schema.PublishRequest{Identity: schema.SessionIdentity{SessionID: schema.SessionID(localID), SchemaVersion: 2}, Model: schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "fixture-model"}, Timestamp: schema.TimestampInfo{Start: 1700000000000, End: 1700000001000}, Source: schema.SourceInfo{FilePath: "/fixture/session.jsonl", Format: schema.SourceFormatJSONL}, Project: schema.ProjectContext{Hash: testProjectHash, Name: "evidence"}}
					w = publishEvidencePost(t, h, user, req, content)
				case "read_content":
					id := uuidFromPg(before.row.ID).String()
					r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id+"/content", nil)
					r = withChiURLParam(r, "id", id)
					r = r.WithContext(withUserID(r.Context(), uuidFromPg(owner)))
					w = httptest.NewRecorder()
					h.GetTranscriptContent(w, r)
				case "rewrite":
					stored, _, err := reader.Read(ctx, uuidFromPg(before.row.ID), mustDescriptor(t, before.row), mustLoadedIdentity(t, before.row))
					if err != nil {
						t.Fatalf("read the stored generation: %v", err)
					}
					w = httptest.NewRecorder()
					installed, err := h.rewriteCanonicalTranscript(ctx, before.row, canonicalStoredContent(t), sniffShape(stored))
					if err != nil || installed == nil {
						t.Fatalf("rewrite: installed=%v err=%v", installed, err)
					}
				case "delete":
					id := uuidFromPg(before.row.ID).String()
					r := httptest.NewRequest(http.MethodDelete, "/api/v1/transcripts/"+id, nil)
					r = withChiURLParam(r, "id", id)
					r = r.WithContext(withUserID(r.Context(), uuidFromPg(owner)))
					w = httptest.NewRecorder()
					h.DeleteTranscript(w, r)
				default:
					t.Fatalf("unknown step action %q", step.Action)
				}
				if w.Code != step.WantStatus {
					t.Fatalf("step %d %s: status %d, want %d: %s", index, step.Action, w.Code, step.WantStatus, w.Body.String())
				}
				if writes := store.writes.Load() - writesBefore; writes != step.WantWrites {
					t.Fatalf("step %d %s: generations written = %d, want %d", index, step.Action, writes, step.WantWrites)
				}
				after := load()
				if after.exists {
					seenKeys[after.row.BlobKey] = true
					if after.metadataKey != "" {
						seenKeys[after.metadataKey] = true
					}
					if after.exists && after.row.ID.Valid && index == 0 {
						defer purgeAuditRows(t, context.Background(), pool, []pgtype.UUID{after.row.ID})
					}
				}
				newGeneration := after.exists && (!before.exists || after.row.BlobKey != before.row.BlobKey)
				if newGeneration != step.WantNewGeneration {
					t.Fatalf("step %d %s: new generation = %v, want %v", index, step.Action, newGeneration, step.WantNewGeneration)
				}
				if newGeneration && before.exists && before.metadataKey != "" && after.metadataKey != "" {
					if after.metadataKey == before.metadataKey {
						t.Fatalf("step %d %s: the new generation kept the old metadata object %q", index, step.Action, after.metadataKey)
					}
					if bytes.Equal(after.row.WrappedDataKey, before.row.WrappedDataKey) {
						t.Fatalf("step %d %s: the new generation kept the old wrapped data key", index, step.Action)
					}
				}
				generations[index] = after
				before = after
			}

			final := before
			if c.Fault != "" {
				matches := reconciliationObjectKey.FindAllStringSubmatch(logs.String(), -1)
				if len(matches) != 1 || !strings.Contains(logs.String(), "msg=transcript_blob_reconciliation_required") {
					t.Fatalf("want exactly one reconciliation event naming the orphaned body, got: %s", logs.String())
				}
				orphan := matches[0][1]
				if !objectExists(t, ctx, rawObjects, orphan) {
					t.Fatalf("the reported orphan %q is not in RustFS", orphan)
				}
				if err := rawObjects.Delete(ctx, storage.ObjectKey(orphan)); err != nil {
					t.Fatalf("remove the orphaned fixture body: %v", err)
				}
			}
			if c.WantDeleted {
				if final.exists {
					t.Fatalf("the transcript row still exists after %s", c.Name)
				}
				for key := range seenKeys {
					if objectExists(t, ctx, rawObjects, key) {
						t.Fatalf("object %q survived the delete", key)
					}
				}
				return
			}
			if !final.exists {
				t.Fatal("the transcript row is missing")
			}
			defer func() {
				_ = rawObjects.Delete(context.Background(), storage.ObjectKey(final.row.BlobKey))
				if final.metadataKey != "" {
					_ = rawObjects.Delete(context.Background(), storage.ObjectKey(final.metadataKey))
				}
			}()

			descriptor, err := descriptorFromTranscript(final.row)
			if err != nil {
				t.Fatal(err)
			}
			metadataObject, err := metadataObjectFromTranscript(final.row)
			if err != nil {
				t.Fatalf("map the metadata columns: %v", err)
			}
			if c.WantMetadataStep == nil {
				if metadataObject != nil || final.row.PublishMetadataKey.Valid || final.row.PublishMetadataHash.Valid || final.row.PublishMetadataSize.Valid {
					t.Fatalf("metadata columns = %v %v %v, want all NULL", final.row.PublishMetadataKey, final.row.PublishMetadataHash, final.row.PublishMetadataSize)
				}
			} else {
				if metadataObject == nil {
					t.Fatal("the row carries no metadata object")
				}
				want := requests[*c.WantMetadataStep]
				got, err := reader.ReadMetadata(ctx, uuidFromPg(final.row.ID), descriptor, *metadataObject)
				if err != nil {
					t.Fatalf("read the stored metadata against the current descriptor: %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("stored metadata:\n%s\nwant json.Marshal of step %d:\n%s", got, *c.WantMetadataStep, want)
				}
				if client := c.Steps[*c.WantMetadataStep].Client; client != "" {
					var decoded struct {
						Client *schema.AuthoritativeClientInfo `json:"client"`
					}
					if err := json.Unmarshal(got, &decoded); err != nil || decoded.Client == nil || decoded.Client.Name+" "+decoded.Client.Version != client {
						t.Fatalf("stored client = %+v (%v), want %q", decoded.Client, err, client)
					}
				}
			}
			live := map[string]bool{final.row.BlobKey: true}
			if final.metadataKey != "" {
				live[final.metadataKey] = true
			}
			for key := range seenKeys {
				if exists := objectExists(t, ctx, rawObjects, key); exists != live[key] {
					t.Fatalf("object %q exists=%v, want %v (live: %v)", key, exists, live[key], live)
				}
			}

			rows, err := pool.Query(ctx, `SELECT replaced_content_hash, installed_content_hash, replaced_was_legacy FROM transcript_content_rewrites WHERE transcript_id = $1`, final.row.ID)
			if err != nil {
				t.Fatal(err)
			}
			type rewriteRow struct {
				replaced, installed string
				legacy              bool
			}
			var recorded []rewriteRow
			for rows.Next() {
				var r rewriteRow
				if err := rows.Scan(&r.replaced, &r.installed, &r.legacy); err != nil {
					t.Fatal(err)
				}
				recorded = append(recorded, r)
			}
			rows.Close()
			if c.WantRewrite == nil {
				if len(recorded) != 0 {
					t.Fatalf("rewrite records = %+v, want none", recorded)
				}
				return
			}
			want := rewriteRow{replaced: generations[c.WantRewrite.ReplacedStep].row.ContentHash.String, installed: final.row.ContentHash.String, legacy: c.WantRewrite.Legacy}
			if len(recorded) != 1 || recorded[0] != want {
				t.Fatalf("rewrite records = %+v, want exactly %+v", recorded, want)
			}
		})
	}
}
