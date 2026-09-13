//go:build integration

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func TestLegacyDispatchEncryptedPublicationAndReads(t *testing.T) {
	ctx := context.Background()
	pool := publishLockPool(t, 8)
	store := authoritativeTestBlobStore(t)
	for i, c := range loadLegacyDispatch(t) {
		t.Run(c.Name, func(t *testing.T) {
			owner := pullInsertUser(t, ctx, pool, int64(992000+i), "dispatch-"+c.Name)
			defer cleanupOwners(t, ctx, pool, owner)
			blobs := &countedPiBlobStore{TranscriptBlobStore: store}
			h := newTestHandler(sqlc.New(pool), blobs)
			h.pool = pool
			user := &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "dispatch-fixture"}
			metadata := func(raw []byte) []byte {
				meta := piMetadata(t, raw)
				if c.Context == "" {
					meta = bytes.Replace(meta, []byte(`"harness":"pi"`), []byte(`"harness":"claude-code"`), 1)
				}
				return meta
			}
			seed := currentEnvelopeJSON(t, "claude-code")
			if c.Context == "pi" {
				seed = piContent(t)
			}
			created := publishPiParts(t, h, user, metadata(seed), seed)
			if created.Code != http.StatusCreated {
				t.Fatalf("seed=%d %s", created.Code, created.Body.String())
			}
			var receipt schema.AuthoritativePublishResponse
			if err := json.Unmarshal(created.Body.Bytes(), &receipt); err != nil {
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
			type state struct {
				Row                          sqlc.Transcript
				Audit, Commits, Associations string
				Raw                          []byte
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
				s.Raw, _, err = blobs.Read(ctx, uuid.UUID(tid.Bytes), d, loaded)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			raw := []byte(c.Content)
			before := snapshot()
			published := publishPiParts(t, h, user, metadata(raw), raw)
			want := c.PublishStatus
			if want == http.StatusCreated {
				want = http.StatusOK
			}
			if published.Code != want {
				t.Fatalf("publish=%d want=%d %s", published.Code, want, published.Body.String())
			}
			if c.PublishStatus >= 400 && !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("refused publication changed database, audit, relationships, encrypted object or counters")
			}
			// The private encrypted writer installs a historic generation as test
			// setup, including data that the public publish gate correctly refuses.
			if c.PublishStatus >= 400 {
				row, err := sqlc.New(pool).GetTranscriptByID(ctx, tid)
				if err != nil {
					t.Fatal(err)
				}
				if err := h.rewriteCanonicalTranscript(ctx, row, raw); err != nil {
					t.Fatal(err)
				}
			}
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
			before = snapshot()
			pulled := read(true)
			if pulled.Code != c.ReadStatus {
				t.Fatalf("pull=%d want=%d %s", pulled.Code, c.ReadStatus, pulled.Body.String())
			}
			if pulled.Code == http.StatusOK {
				if !bytes.Equal(pulled.Body.Bytes(), raw) {
					t.Fatal("raw pull changed stored bytes")
				}
				if pulled.Header().Get("ETag") != `"`+string(schema.ComputeTranscriptContentHash(raw))+`"` {
					t.Fatal("pull ETag differs from stored plaintext hash")
				}
			}
			if !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("raw pull changed stored state")
			}
			served := read(false)
			if c.ReadStatus >= 400 {
				if served.Code != http.StatusInternalServerError || !reflect.DeepEqual(before, snapshot()) {
					t.Fatal("invalid historic generation was served or mutated")
				}
			} else if c.Name != "opaque_plaintext" {
				if served.Code != http.StatusOK {
					t.Fatalf("display=%d %s", served.Code, served.Body.String())
				}
				after := snapshot()
				second := read(false)
				if second.Code != http.StatusOK || !bytes.Equal(second.Body.Bytes(), served.Body.Bytes()) || !reflect.DeepEqual(after, snapshot()) {
					t.Fatal("second legacy display read was not an identical no-op")
				}
			} else if served.Code != http.StatusInternalServerError {
				t.Fatal("opaque text unexpectedly became renderable")
			}
		})
	}
}
