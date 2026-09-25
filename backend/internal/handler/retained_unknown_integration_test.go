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
	receiverCases := make([]retainedReceiverCase, 0, len(cases))
	for _, c := range cases {
		receiverCases = append(receiverCases, retainedReceiverCase{Name: c.Name, Content: c.Content, ExpectedWrites: 2, Status: 201})
	}
	receiverCases = append(receiverCases, loadRetainedReceiverCases(t)...)
	for _, c := range receiverCases {
		if c.Status != http.StatusCreated {
			continue
		} // Rejected size edge replaces a good publication below.
		t.Run(c.Name, func(t *testing.T) {
			logs := captureRetainedLogs(t)
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
			type state struct {
				Row             sqlc.Transcript
				Audit           string
				Writes, Deletes int
				Bytes           []byte
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
				d, err := descriptorFromTranscript(row)
				if err != nil {
					t.Fatal(err)
				}
				identity, err := identityFromTranscript(row)
				if err != nil {
					t.Fatal(err)
				}
				s.Bytes, _, err = blobs.Read(ctx, uuid.UUID(tid.Bytes), d, identity)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			request := func(method string, viewer *AuthUser) *http.Request {
				r := httptest.NewRequest(method, "/api/v1/transcripts/"+receipt.TranscriptID.String()+"/content", nil)
				r = withChiURLParam(r, "id", receipt.TranscriptID.String())
				if viewer != nil {
					r = r.WithContext(context.WithValue(r.Context(), UserContextKey, viewer))
				}
				return r
			}
			verifyReadOnly := c.ExpectedWrites == 1
			read := func(pull bool, viewer *AuthUser) *httptest.ResponseRecorder {
				var before state
				if verifyReadOnly {
					before = snapshot()
				}
				w := httptest.NewRecorder()
				if pull {
					h.GetPullTranscriptContent(w, request(http.MethodGet, viewer))
				} else {
					h.GetTranscriptContent(w, request(http.MethodGet, viewer))
				}
				if verifyReadOnly && !reflect.DeepEqual(before, snapshot()) {
					t.Fatal("no-write read changed row, audit, counters or authenticated original bytes")
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
				got := assertRetainedWire(t, content, w.Body.Bytes())
				expected := *original.SessionDetail
				expected.SchemaVersion = got.SchemaVersion
				if !equalPublicPayloads(&expected, got) {
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
			if blobs.writes != c.ExpectedWrites {
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
				auditSnapshot := func() string {
					t.Helper()
					var audit string
					if err := pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(e) ORDER BY seq),'[]'::jsonb)::text FROM transcript_governance_events_audit e WHERE transcript_id=$1`, tid).Scan(&audit); err != nil {
						t.Fatal(err)
					}
					return audit
				}
				boundaries := loadRetainedUnknownBoundaries(t)
				for _, edge := range receiverCases {
					if edge.Status == http.StatusConflict {
						boundaries = append(boundaries, retainedUnknownBoundary{Name: edge.Name, FinalBytes: edge.FinalBytes, Status: edge.Status, Error: "raw JSON validation failed"})
					}
				}
				for _, bad := range boundaries {
					beforeAudit := auditSnapshot()
					before, err := sqlc.New(pool).GetTranscriptByID(ctx, tid)
					if err != nil {
						t.Fatal(err)
					}
					writes, deletes := blobs.writes, blobs.deletes
					prior := read(true, user).Body.Bytes()
					raw, meta := bad.parts(t)
					if bad.ProofFailure {
						h.preservationEvaluator = fixedPreservationEvaluator{err: errors.New("preservation unavailable")}
					}
					refused := publishPiParts(t, h, user, meta, raw)
					h.preservationEvaluator = nil
					if refused.Code != bad.Status || !strings.Contains(refused.Body.String(), bad.Error) {
						t.Fatalf("%s: %d %s", bad.Name, refused.Code, refused.Body.String())
					}
					bad.assertPrivate(t, refused.Body.String(), logs.String())
					after, err := sqlc.New(pool).GetTranscriptByID(ctx, tid)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(before, after) || beforeAudit != auditSnapshot() || writes != blobs.writes || deletes != blobs.deletes || !bytes.Equal(prior, read(true, user).Body.Bytes()) {
						t.Fatalf("%s changed prior good row or object", bad.Name)
					}
					if bad.StoredReject {
						func() {
							d, identity, err := blobs.Write(ctx, uuid.UUID(tid.Bytes), raw)
							if err != nil {
								t.Fatal(err)
							}
							defer func() {
								if err := blobs.Delete(ctx, d); err != nil {
									t.Error(err)
								}
							}()
							invalid := after
							invalid.BlobKey = string(d.ObjectKey())
							invalid.WrappedDataKey = d.WrappedDEK()
							invalid.KeyVersion = int32(d.KeyVersion())
							invalid.EncryptionAlgorithm = string(d.Algorithm())
							invalid.ContentHash = pgtype.Text{String: string(identity.Hash()), Valid: true}
							invalid.BlobSizeBytes = pgtype.Int8{Int64: identity.PlaintextSize(), Valid: true}
							install := func(from, to sqlc.Transcript) {
								t.Helper()
								result := h.inEncryptedTx(ctx, owner, func(q Querier) error {
									_, err := q.CompareAndSwapTranscriptBlob(ctx, sqlc.CompareAndSwapTranscriptBlobParams{ID: tid, BlobKey: to.BlobKey, WrappedDataKey: to.WrappedDataKey, EncryptionAlgorithm: to.EncryptionAlgorithm, KeyVersion: to.KeyVersion, ContentHash: to.ContentHash, PlaintextSize: to.BlobSizeBytes, ExpectedBlobKey: from.BlobKey, ExpectedWrappedDataKey: from.WrappedDataKey, ExpectedEncryptionAlgorithm: from.EncryptionAlgorithm, ExpectedKeyVersion: from.KeyVersion})
									return err
								})
								if result.Err != nil {
									t.Fatal(result.Err)
								}
							}
							install(after, invalid)
							defer install(invalid, after)
							before := snapshot()
							if read(false, user).Code != http.StatusInternalServerError || read(true, user).Code != http.StatusInternalServerError {
								t.Fatalf("%s served invalid historical evidence", bad.Name)
							}
							if !reflect.DeepEqual(before, snapshot()) {
								t.Fatal("invalid historical read changed storage")
							}
						}()
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
			verifyReadOnly = false
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
