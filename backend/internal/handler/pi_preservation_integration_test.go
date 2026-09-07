//go:build integration

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

type countedPiBlobStore struct {
	storage.TranscriptBlobStore
	writes, deletes int
}

var _ storage.TranscriptBlobStore = (*countedPiBlobStore)(nil)

func (s *countedPiBlobStore) Write(ctx context.Context, id uuid.UUID, raw []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	s.writes++
	return s.TranscriptBlobStore.Write(ctx, id, raw)
}
func (s *countedPiBlobStore) Delete(ctx context.Context, d storage.BlobDescriptor) error {
	s.deletes++
	return s.TranscriptBlobStore.Delete(ctx, d)
}

func TestPiEncryptedPublishRewriteReadPull(t *testing.T) {
	ctx := context.Background()
	pool := publishLockPool(t, 8)
	owner := pullInsertUser(t, ctx, pool, 991163, "pi-publication-fixture")
	defer cleanupOwners(t, ctx, pool, owner)
	blobs := &countedPiBlobStore{TranscriptBlobStore: authoritativeTestBlobStore(t)}
	h := newTestHandler(sqlc.New(pool), blobs)
	h.pool = pool
	user := &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "pi-publication-fixture"}
	content := piContent(t)
	w := publishPiParts(t, h, user, piMetadata(t, content), content)
	if w.Code != http.StatusCreated {
		t.Fatalf("publish=%d %s", w.Code, w.Body.String())
	}
	var receipt schema.AuthoritativePublishResponse
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	tid := toPgUUID(uuid.MustParse(receipt.TranscriptID.String()))
	defer func() {
		row, err := sqlc.New(pool).GetTranscriptByID(ctx, tid)
		if err != nil {
			t.Error(err)
			return
		}
		d, err := descriptorFromTranscript(row)
		if err != nil {
			t.Error(err)
			return
		}
		if err := blobs.Delete(ctx, d); err != nil {
			t.Error(err)
		}
	}()
	read := func(pull bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+receipt.TranscriptID.String()+"/content", nil)
		r = withChiURLParam(r, "id", receipt.TranscriptID.String())
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, user))
		w := httptest.NewRecorder()
		if pull {
			h.GetPullTranscriptContent(w, r)
		} else {
			h.GetTranscriptContent(w, r)
		}
		return w
	}
	assertPayload := func(raw []byte, envelope bool) {
		t.Helper()
		want, err := schema.DecodeTranscriptContentRaw(content)
		if err != nil {
			t.Fatal(err)
		}
		var got schema.SessionDetailPayload
		if envelope {
			v, err := schema.DecodeTranscriptContentRaw(raw)
			if err != nil {
				t.Fatal(err)
			}
			got = *v.SessionDetail
		} else {
			got, err = schema.DecodeSessionDetailPayloadRaw(raw)
			if err != nil {
				t.Fatal(err)
			}
		}
		want.SessionDetail.SchemaVersion = got.SchemaVersion
		if !equalPublicPayloads(want.SessionDetail, &got) {
			t.Fatal("public evidence changed across encrypted publication")
		}
	}
	pulled := read(true)
	if pulled.Code != http.StatusOK {
		t.Fatalf("initial pull=%d %s", pulled.Code, pulled.Body.String())
	}
	if !bytes.Equal(pulled.Body.Bytes(), content) {
		t.Fatal("pull changed uploaded raw content before rewrite")
	}
	assertPayload(pulled.Body.Bytes(), true)
	served := read(false)
	if served.Code != http.StatusOK {
		t.Fatalf("read=%d %s", served.Code, served.Body.String())
	}
	assertPayload(served.Body.Bytes(), false)
	if blobs.writes != 2 {
		t.Fatalf("publish plus canonical rewrite writes=%d", blobs.writes)
	}
	pulled = read(true)
	if pulled.Code != http.StatusOK {
		t.Fatalf("rewritten pull=%d %s", pulled.Code, pulled.Body.String())
	}
	assertPayload(pulled.Body.Bytes(), true)
	if read(false).Code != http.StatusOK || blobs.writes != 2 {
		t.Fatal("second display read rewrote canonical data")
	}

	type state struct {
		Row                          sqlc.Transcript
		Audit, Commits, Associations string
		Bytes                        []byte
		Writes, Deletes              int
	}
	snapshot := func() state {
		t.Helper()
		row, err := sqlc.New(pool).GetTranscriptByID(ctx, tid)
		if err != nil {
			t.Fatal(err)
		}
		s := state{Row: row, Writes: blobs.writes, Deletes: blobs.deletes}
		if err := pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(e) ORDER BY seq),'[]'::jsonb)::text FROM transcript_governance_events_audit e WHERE transcript_id=$1`, tid).Scan(&s.Audit); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(e)),'[]'::jsonb)::text FROM transcript_commits e WHERE transcript_id=$1`, tid).Scan(&s.Commits); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(e)),'[]'::jsonb)::text FROM transcript_associations e WHERE transcript_id=$1`, tid).Scan(&s.Associations); err != nil {
			t.Fatal(err)
		}
		d, err := descriptorFromTranscript(row)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := identityFromTranscript(row)
		if err != nil {
			t.Fatal(err)
		}
		s.Bytes, _, err = blobs.Read(ctx, uuid.UUID(tid.Bytes), d, loaded)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, c := range loadPiBoundaries(t) {
		t.Run(c.Name, func(t *testing.T) {
			before := snapshot()
			meta, raw := c.parts(t)
			if c.Surface == "capability" {
				h.preservationEvaluator = fixedPreservationEvaluator{err: errors.New("preservation unavailable")}
			}
			w := publishPiParts(t, h, user, meta, raw)
			h.preservationEvaluator = nil
			want := c.Status
			if want == http.StatusCreated {
				want = http.StatusOK
			}
			if w.Code != want || !strings.Contains(w.Body.String(), c.Error) {
				t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
			}
			if c.Status != http.StatusCreated && !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("refused request changed row, encrypted bytes, ledger, audit or object counters")
			}
		})
	}
	// Install an authenticated but contract-invalid historic generation through
	// the real writer fence. Reads must refuse it without mutating PostgreSQL/S3.
	for _, c := range loadPiBoundaries(t) {
		if c.Surface != "content" || c.Status == http.StatusCreated {
			continue
		}
		t.Run("stored_"+c.Name, func(t *testing.T) {
			_, raw := c.parts(t)
			d, identity, err := blobs.Write(ctx, uuid.UUID(tid.Bytes), raw)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := blobs.Delete(ctx, d); err != nil {
					t.Error(err)
				}
			}()
			row, err := sqlc.New(pool).GetTranscriptByID(ctx, tid)
			if err != nil {
				t.Fatal(err)
			}
			invalid := row
			invalid.BlobKey = string(d.ObjectKey())
			invalid.WrappedDataKey = d.WrappedDEK()
			invalid.KeyVersion = int32(d.KeyVersion())
			invalid.EncryptionAlgorithm = string(d.Algorithm())
			invalid.ContentHash = pgtype.Text{String: string(identity.Hash()), Valid: true}
			invalid.BlobSizeBytes = pgtype.Int8{Int64: identity.PlaintextSize(), Valid: true}
			install := func(from, to sqlc.Transcript) {
				t.Helper()
				result := h.inEncryptedTx(ctx, owner, func(q Querier) error {
					_, err := q.CompareAndSwapTranscriptBlob(ctx, sqlc.CompareAndSwapTranscriptBlobParams{
						ID: tid, BlobKey: to.BlobKey, WrappedDataKey: to.WrappedDataKey, EncryptionAlgorithm: to.EncryptionAlgorithm, KeyVersion: to.KeyVersion, ContentHash: to.ContentHash, PlaintextSize: to.BlobSizeBytes,
						ExpectedBlobKey: from.BlobKey, ExpectedWrappedDataKey: from.WrappedDataKey, ExpectedEncryptionAlgorithm: from.EncryptionAlgorithm, ExpectedKeyVersion: from.KeyVersion,
					})
					return err
				})
				if result.Err != nil {
					t.Fatal(result.Err)
				}
			}
			install(row, invalid)
			defer install(invalid, row)
			before := snapshot()
			if read(false).Code != http.StatusInternalServerError || read(true).Code != http.StatusInternalServerError {
				t.Fatal("invalid stored content was served")
			}
			if !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("invalid stored content caused mutation")
			}
		})
	}
}
