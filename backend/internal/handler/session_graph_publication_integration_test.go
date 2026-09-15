//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

// Observe the actual encrypted store, never replace its encryption or I/O.
type graphPublicationBlobObserver struct {
	storage.TranscriptBlobStore
	writes atomic.Int64
}

func (b *graphPublicationBlobObserver) Write(ctx context.Context, id uuid.UUID, content []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	b.writes.Add(1)
	return b.TranscriptBlobStore.Write(ctx, id, content)
}

func TestSessionGraphEncryptedPublicationAndPull(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	owner := pullInsertUser(t, ctx, pool, 99887767, "graph-publication-owner")
	defer cleanupOwners(t, ctx, pool, owner)
	blobs := &graphPublicationBlobObserver{TranscriptBlobStore: authoritativeTestBlobStore(t)}
	h := New(&config.Config{FrontendURL: "https://example.test"}, pool, blobs)
	user := &AuthUser{ID: uuidFromPg(owner), Username: "graph-publication-owner"}
	routes := chi.NewRouter()
	routes.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, user)))
		})
	})
	routes.Post("/api/v1/transcripts/publish", h.PublishTranscript)
	routes.Get("/api/v1/transcripts/{id}", h.GetTranscript)
	routes.Get("/api/v1/transcripts/{id}/content", h.GetTranscriptContent)
	routes.Get("/api/v1/pull/transcripts/{id}/content", h.GetPullTranscriptContent)
	f := loadGraphPublicationFixtures(t)
	queries := sqlc.New(pool)
	var transcriptID uuid.UUID
	defer func() {
		if transcriptID == uuid.Nil {
			return
		}
		row, err := queries.GetTranscriptByID(ctx, toPgUUID(transcriptID))
		if err != nil {
			t.Errorf("load final descriptor for cleanup: %v", err)
			return
		}
		descriptor, err := descriptorFromTranscript(row)
		if err != nil {
			t.Errorf("decode cleanup descriptor: %v", err)
			return
		}
		if err := blobs.Delete(ctx, descriptor); err != nil {
			t.Errorf("remove test ciphertext: %v", err)
		}
	}()
	// Cases deliberately reuse one owner/local identity. Accepted replacements
	// and their exact retries must never create another transcript or merge by text.
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			content := graphPublicationContent(t, f, c)
			metadata := graphPublicationMetadata(t, content, f)
			beforeWrites := blobs.writes.Load()
			var beforeState string
			stateSQL := `SELECT jsonb_build_object('transcripts', (SELECT jsonb_agg(to_jsonb(t) ORDER BY t.id) FROM transcripts t WHERE owner_id=$1), 'audit', (SELECT jsonb_agg(to_jsonb(a) ORDER BY a.seq) FROM transcript_governance_events_audit a WHERE changed_by=$1), 'shares', (SELECT jsonb_agg(to_jsonb(s) ORDER BY s.id) FROM transcript_share_attempts s JOIN transcripts t ON t.id=s.transcript_id WHERE t.owner_id=$1))::text`
			if err := pool.QueryRow(ctx, stateSQL, owner).Scan(&beforeState); err != nil {
				t.Fatal(err)
			}
			publish := func() *httptest.ResponseRecorder {
				body, boundary := multipartBody(t, map[string]string{"metadata": string(metadata)}, string(content))
				r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
				r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
				w := httptest.NewRecorder()
				routes.ServeHTTP(w, r)
				return w
			}
			w := publish()
			if !c.Accepted {
				// The content boundary refuses un-preservable content with 409
				// before the durable graph decode; the message is its canonical one.
				if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), c.Error) {
					t.Fatalf("refusal status=%d body=%s", w.Code, w.Body.String())
				}
				var afterState string
				if err := pool.QueryRow(ctx, stateSQL, owner).Scan(&afterState); err != nil {
					t.Fatal(err)
				}
				if beforeState != afterState || blobs.writes.Load() != beforeWrites {
					t.Fatal("raw refusal mutated database, audit, shares, or encrypted objects")
				}
				return
			}
			if w.Code != http.StatusCreated && w.Code != http.StatusOK {
				t.Fatalf("publish status=%d body=%s", w.Code, w.Body.String())
			}
			var response schema.AuthoritativePublishResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			id, err := uuid.Parse(string(response.TranscriptID))
			if err != nil {
				t.Fatal(err)
			}
			if transcriptID != uuid.Nil && transcriptID != id {
				t.Fatal("republish changed owner/local transcript identity")
			}
			transcriptID = id
			expected, err := decodePublicationDetail(content)
			if err != nil {
				t.Fatal(err)
			}
			row, err := queries.GetTranscriptByID(ctx, toPgUUID(id))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(pgInt8ToInt64Ptr(row.InputSubmissionCount), expected.InputSubmissionCount) || int(row.TurnCount.Int32) != expected.TurnCount {
				t.Fatal("stored input presence/value or independent turn total changed")
			}
			if !reflect.DeepEqual(pgTextToSessionIDPtr(row.RootSessionID), expected.RootSessionID) || row.SessionPurpose.String != string(expected.Purpose) {
				t.Fatal("stored graph identity changed")
			}
			var relationships []schema.SessionRelationship
			if err := json.Unmarshal(row.SessionRelationships, &relationships); err != nil {
				t.Fatal(err)
			}
			if len(relationships) != len(expected.Relationships) || len(relationships) > 0 && !reflect.DeepEqual(relationships, expected.Relationships) {
				t.Fatal("stored relationship/anchor evidence changed")
			}
			writesAfterPublish := blobs.writes.Load()
			readMetadata := httptest.NewRecorder()
			routes.ServeHTTP(readMetadata, httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id.String(), nil))
			if readMetadata.Code != http.StatusOK {
				t.Fatalf("metadata read: %d %s", readMetadata.Code, readMetadata.Body.String())
			}
			var metadataResponse struct {
				Transcript transcriptResponse `json:"transcript"`
			}
			if err := json.Unmarshal(readMetadata.Body.Bytes(), &metadataResponse); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(metadataResponse.Transcript.InputSubmissionCount, expected.InputSubmissionCount) || !reflect.DeepEqual(metadataResponse.Transcript.RootSessionID, expected.RootSessionID) || metadataResponse.Transcript.Purpose != expected.Purpose {
				t.Fatal("mounted metadata read lost count presence or durable graph projections")
			}
			retry := publish()
			if retry.Code != http.StatusOK || blobs.writes.Load() != writesAfterPublish {
				t.Fatalf("exact retry rewrote ciphertext or failed: %d %s", retry.Code, retry.Body.String())
			}
			web := httptest.NewRecorder()
			routes.ServeHTTP(web, httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id.String()+"/content", nil))
			if web.Code != http.StatusOK {
				t.Fatalf("content read: %d %s", web.Code, web.Body.String())
			}
			webDetail, err := decodePublicationDetail(web.Body.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			expected.SchemaVersion = webDetail.SchemaVersion
			if !reflect.DeepEqual(expected, webDetail) {
				t.Fatal("encrypted read/typed migration/canonical rewrite lost exact evidence or full body")
			}
			// Exercise the real immutable encrypted rewrite, even when the accepted
			// envelope is patch-compatible and the web read correctly avoids churn.
			canonical, err := encodeCanonicalTranscript(webDetail)
			if err != nil {
				t.Fatal(err)
			}
			row, err = queries.GetTranscriptByID(ctx, toPgUUID(id))
			if err != nil {
				t.Fatal(err)
			}
			if err := h.rewriteCanonicalTranscript(ctx, row, canonical); err != nil {
				t.Fatal(err)
			}
			pull := httptest.NewRecorder()
			routes.ServeHTTP(pull, httptest.NewRequest(http.MethodGet, "/api/v1/pull/transcripts/"+id.String()+"/content", nil))
			if pull.Code != http.StatusOK {
				t.Fatalf("pull read: %d %s", pull.Code, pull.Body.String())
			}
			pulled, err := decodePublicationDetail(pull.Body.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(webDetail, pulled) {
				t.Fatal("pull changed durable content or emitted read navigation")
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcripts WHERE owner_id=$1 AND local_id=$2`, owner, expected.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("owner/local identity cardinality=%d want exactly one", count)
			}
		})
	}
}
