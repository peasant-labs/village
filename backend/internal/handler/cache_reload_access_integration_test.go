//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/cache_reload_access.yaml
var cacheReloadAccessYAML []byte

type reloadAccessCase struct {
	Name        string `yaml:"name"`
	Replacement string `yaml:"replacement"`
}

type reloadGateStore struct {
	storage.TranscriptBlobStore
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (s *reloadGateStore) Read(ctx context.Context, id uuid.UUID, d storage.BlobDescriptor, loaded storage.LoadedContentIdentity) ([]byte, storage.ContentIdentity, error) {
	first := false
	s.once.Do(func() { first = true; close(s.entered) })
	if first {
		select {
		case <-s.release:
			return nil, storage.ContentIdentity{}, storage.ErrObjectLookupUncertain
		case <-ctx.Done():
			return nil, storage.ContentIdentity{}, ctx.Err()
		}
	}
	return s.TranscriptBlobStore.Read(ctx, id, d, loaded)
}

func TestCacheReloadReauthorizesEachViewer(t *testing.T) {
	cases, err := decodeFixtureRows[reloadAccessCase](cacheReloadAccessYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{"joined-reload-reauthorizes-each-viewer": true}
	fixture, err := decodeFixtureDocument[struct {
		Content        string                       `yaml:"content"`
		HistoryContent string                       `yaml:"history_content"`
		Cases          []contentSourceEncryptedCase `yaml:"cases"`
	}](contentSourceEncryptedYAML)
	if err != nil {
		t.Fatal(err)
	}
	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	objects := publishEvidenceObjects(t)
	for _, tc := range cases {
		if !required[tc.Name] {
			t.Fatalf("unexpected case %q", tc.Name)
		}
		delete(required, tc.Name)
		t.Run(tc.Name, func(t *testing.T) {
			w := newOrderingWorld(t, pool, objects, 69001, []byte(fixture.Content))
			// Test-only identity escape establishes the legacy identity-free pre-image;
			// the subsequent replacement runs with the real revision guard enabled.
			tx, err := pool.Begin(w.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(w.ctx)
			if _, err = tx.Exec(w.ctx, "SET LOCAL session_replication_role = replica"); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(w.ctx, "UPDATE transcripts SET content_hash=NULL, segmented_identity_hash=NULL, segmented_identity_size=NULL WHERE id=$1", w.id); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(w.ctx); err != nil {
				t.Fatal(err)
			}
			row := w.row()
			key := transcriptCacheKeyForRow(row)
			w.handler.contentCache = newTranscriptContentCache(transcriptContentCacheConfig{})
			gate := &reloadGateStore{TranscriptBlobStore: w.handler.blobs, entered: make(chan struct{}), release: make(chan struct{})}
			w.handler.blobs = gate
			mux := chi.NewRouter()
			mux.Get("/transcripts/{id}/header", w.handler.GetTranscriptHeader)
			base := "/transcripts/" + uuidFromPg(w.id).String() + "/header?revision=1"
			// Distinct viewers: the owner retains access, an authenticated non-owner loses it.
			viewerID := pullInsertUser(t, w.ctx, pool, 9989999, "cache-reload-viewer-"+uuid.NewString()[:8])
			t.Cleanup(func() { cleanupOwners(t, context.Background(), pool, viewerID) })
			viewer := &AuthUser{ID: uuidFromPg(viewerID), Username: "viewer"}
			request := func(path string, user *AuthUser) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				r = r.WithContext(context.WithValue(r.Context(), UserContextKey, user))
				out := httptest.NewRecorder()
				mux.ServeHTTP(out, r)
				return out
			}
			ownerCh := make(chan *httptest.ResponseRecorder, 1)
			viewerCh := make(chan *httptest.ResponseRecorder, 1)
			go func() { ownerCh <- request(base, w.owner) }()
			<-gate.entered
			go func() { viewerCh <- request(base, viewer) }()
			cacheEventually(t, "both viewers to join the same flight", func() bool { return cacheWaiters(w.handler.contentCache, key) == 2 })
			replacement := strings.Replace(fixture.Content, "first prompt", tc.Replacement, 1)
			desc, identity, err := gate.TranscriptBlobStore.Write(w.ctx, uuidFromPg(w.id), []byte(replacement))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = objects.Delete(context.Background(), desc.ObjectKey()) })
			tx2, err := pool.Begin(w.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx2.Rollback(w.ctx)
			if _, err = tx2.Exec(w.ctx, "SELECT set_config('app.transcript_writer_version','1',true)"); err != nil {
				t.Fatal(err)
			}
			_, err = tx2.Exec(w.ctx, `UPDATE transcripts SET blob_key=$2, wrapped_data_key=$3, encryption_algorithm=$4, key_version=$5, content_hash=$6, blob_size_bytes=$7, content_revision=content_revision WHERE id=$1`, w.id, string(desc.ObjectKey()), desc.WrappedDEK(), string(desc.Algorithm()), int32(desc.KeyVersion()), string(identity.Hash()), identity.PlaintextSize())
			if err != nil {
				t.Fatal(err)
			}
			if err = tx2.Commit(w.ctx); err != nil {
				t.Fatal(err)
			}
			if fresh := w.row(); fresh.ContentRevision != row.ContentRevision {
				t.Fatalf("revision moved: %d to %d", row.ContentRevision, fresh.ContentRevision)
			}
			if resp := transcriptVisibilityPatch(t, w.handler, w.owner, w.id, dbVisibilityPrivate); resp.Code != http.StatusOK {
				t.Fatalf("revoke: %d %s", resp.Code, resp.Body.String())
			}
			close(gate.release)
			owner, denied := <-ownerCh, <-viewerCh
			if owner.Code != http.StatusOK {
				t.Fatalf("owner status %d: %s", owner.Code, owner.Body.String())
			}
			if !strings.Contains(owner.Body.String(), string(identity.Hash())) {
				t.Fatal("owner did not receive the replacement generation")
			}
			missing := request("/transcripts/00000000-0000-4000-8000-000000000001/header?revision=1", viewer)
			if denied.Code != http.StatusNotFound || !bytes.Equal(denied.Body.Bytes(), missing.Body.Bytes()) || !reflect.DeepEqual(denied.Header(), missing.Header()) || strings.Contains(denied.Body.String(), tc.Replacement) {
				t.Fatalf("revoked viewer: status=%d body=%s headers=%v; missing status=%d body=%s headers=%v", denied.Code, denied.Body, denied.Header(), missing.Code, missing.Body, missing.Header())
			}
		})
	}
	if len(required) != 0 {
		t.Fatalf("missing cases: %v", required)
	}
}
