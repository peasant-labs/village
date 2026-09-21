//go:build integration

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func TestRetainedUnknownEncryptedPublicationReadPull(t *testing.T) {
	ctx := context.Background()
	pool := publishLockPool(t, 8)
	owner := pullInsertUser(t, ctx, pool, 991164, "unknown-publication-fixture")
	defer cleanupOwners(t, ctx, pool, owner)
	user := &AuthUser{ID: uuid.UUID(owner.Bytes), Username: "unknown-publication-fixture"}
	cases, err := loadRetainedUnknownFixtures(retainedUnknownYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			blobs := &countedPiBlobStore{TranscriptBlobStore: authoritativeTestBlobStore(t)}
			h := newTestHandler(sqlc.New(pool), blobs)
			h.pool = pool
			content := []byte(c.Content)
			w := publishPiParts(t, h, user, retainedUnknownMetadata(t, content), content)
			if w.Code != http.StatusCreated {
				t.Fatalf("publish=%d %s", w.Code, w.Body.String())
			}
			var receipt schema.AuthoritativePublishResponse
			if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
			tid := toPgUUID(uuid.MustParse(receipt.TranscriptID.String()))
			defer purgeAuditRows(t, ctx, pool, []pgtype.UUID{tid})
			request := func(method string, viewer *AuthUser) *http.Request {
				r := httptest.NewRequest(method, "/api/v1/transcripts/"+receipt.TranscriptID.String()+"/content", nil)
				r = withChiURLParam(r, "id", receipt.TranscriptID.String())
				if viewer != nil {
					r = r.WithContext(context.WithValue(r.Context(), UserContextKey, viewer))
				}
				return r
			}
			read := func(pull bool, viewer *AuthUser) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				if pull {
					h.GetPullTranscriptContent(w, request(http.MethodGet, viewer))
				} else {
					h.GetTranscriptContent(w, request(http.MethodGet, viewer))
				}
				return w
			}
			original, err := schema.DecodeTranscriptContentRaw(content)
			if err != nil {
				t.Fatal(err)
			}
			assertPayload := func(w *httptest.ResponseRecorder) {
				t.Helper()
				if w.Code != http.StatusOK {
					t.Fatalf("read=%d %s", w.Code, w.Body.String())
				}
				got, err := schema.DecodeTranscriptContentRaw(w.Body.Bytes())
				if err != nil {
					t.Fatal(err)
				}
				expected := *original.SessionDetail
				expected.SchemaVersion = got.SessionDetail.SchemaVersion
				if !equalPublicPayloads(&expected, got.SessionDetail) {
					t.Fatal("known turns, complete lexical payloads, positions or partial signal changed")
				}
			}
			first := read(true, user)
			assertPayload(first)
			if !bytes.Equal(content, first.Body.Bytes()) {
				t.Fatal("initial pull changed exact uploaded bytes")
			}
			assertPayload(read(false, user))
			assertPayload(read(true, user))
			assertPayload(read(false, user))
			if blobs.writes != 2 {
				t.Fatalf("publish plus single canonical rewrite wrote %d objects", blobs.writes)
			}
			row, err := sqlc.New(pool).GetTranscriptByID(ctx, tid)
			if err != nil {
				t.Fatal(err)
			}
			if !row.DiagnosticsPartial.Valid || !row.DiagnosticsPartial.Bool {
				t.Fatal("metadata partial mirror was not persisted")
			}
			if read(false, nil).Code == http.StatusOK || read(true, &AuthUser{ID: uuid.New()}).Code == http.StatusOK {
				t.Fatal("private retained evidence leaked to unauthorized reader")
			}
			if c.Name == retainedUnknownCaseNames[0] {
				for _, bad := range loadRetainedUnknownBoundaries(t) {
					before, err := sqlc.New(pool).GetTranscriptByID(ctx, tid)
					if err != nil {
						t.Fatal(err)
					}
					writes, deletes := blobs.writes, blobs.deletes
					prior := read(true, user).Body.Bytes()
					raw, meta := bad.parts(t)
					refused := publishPiParts(t, h, user, meta, raw)
					if refused.Code != bad.Status || !strings.Contains(refused.Body.String(), bad.Error) {
						t.Fatalf("%s: %d %s", bad.Name, refused.Code, refused.Body.String())
					}
					after, err := sqlc.New(pool).GetTranscriptByID(ctx, tid)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(before, after) || writes != blobs.writes || deletes != blobs.deletes || !bytes.Equal(prior, read(true, user).Body.Bytes()) {
						t.Fatalf("%s changed prior good row or object", bad.Name)
					}
				}
			}
			descriptor, err := descriptorFromTranscript(row)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := identityFromTranscript(row)
			if err != nil {
				t.Fatal(err)
			}
			deleted := httptest.NewRecorder()
			h.DeleteTranscript(deleted, request(http.MethodDelete, user))
			if deleted.Code != http.StatusNoContent && deleted.Code != http.StatusOK {
				t.Fatalf("delete=%d %s", deleted.Code, deleted.Body.String())
			}
			if _, _, err := blobs.Read(ctx, uuid.UUID(tid.Bytes), descriptor, identity); err == nil {
				t.Fatal("deleted retained payload remained in object storage")
			}
			if read(true, user).Code != http.StatusNotFound {
				t.Fatal("deleted retained payload remained pullable")
			}
		})
	}
}
