package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func loadLegacyDispatch(t *testing.T) []legacyDispatchCase {
	t.Helper()
	cases, err := loadLegacyDispatchFixtures()
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func TestLegacyDispatchMountedPublishAndPull(t *testing.T) {
	for _, c := range loadLegacyDispatch(t) {
		t.Run(c.Name, func(t *testing.T) {
			user := GetUser(withTestUser(context.Background()))
			var creates, scans, probes int
			q := &mockQuerier{getTranscriptIDByOwnerAndLocalID: func(context.Context, sqlc.GetTranscriptIDByOwnerAndLocalIDParams) (pgtype.UUID, error) {
				probes++
				return pgtype.UUID{}, errFakeNotFound
			}, createTranscript: func(_ context.Context, p sqlc.CreateTranscriptParams) (sqlc.Transcript, error) {
				creates++
				return sqlc.Transcript{ID: p.ID}, nil
			}}
			blobs := newFakeBlobStore()
			h := newTestHandler(q, blobs)
			h.scanContent = func([]byte) []string { scans++; return nil }
			raw := []byte(c.Content)
			meta := piMetadata(t, raw)
			if c.Context == "" {
				meta = bytes.Replace(meta, []byte(`"harness":"pi"`), []byte(`"harness":"claude-code"`), 1)
				var legacy map[string]json.RawMessage
				if err := json.Unmarshal(meta, &legacy); err != nil {
					t.Fatal(err)
				}
				delete(legacy, "contentHash")
				delete(legacy, "visibilityIntent")
				var err error
				meta, err = json.Marshal(legacy)
				if err != nil {
					t.Fatal(err)
				}
			}
			w := publishPiParts(t, h, user, meta, raw)
			if w.Code != c.PublishStatus {
				t.Fatalf("publish=%d want=%d body=%s", w.Code, c.PublishStatus, w.Body.String())
			}
			if c.PublishStatus != http.StatusCreated && (creates != 0 || scans != 0 || probes != 0 || blobs.uploadCount() != 0) {
				t.Fatal("refusal caused publication effects")
			}
			// Stored context is independent of the raw claim. The real pull handler
			// must use that context, not erase it by inspecting the content alone.
			const key = "transcripts/10000000-0000-4000-8000-000000000001.bin"
			blobs.put(key, raw)
			readQueries := publicTranscriptQuerier(key)
			original := readQueries.getTranscriptByID
			readQueries.getTranscriptByID = func(ctx context.Context, id pgtype.UUID) (sqlc.Transcript, error) {
				row, err := original(ctx, id)
				row.ModelProvider = c.Context
				row.OwnerID = user.PgID()
				return row, err
			}
			h = newTestHandler(readQueries, blobs)
			r := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+mustFixtureUUID(t).String()+"/content", nil)
			r = withChiURLParam(r, "id", mustFixtureUUID(t).String())
			r = r.WithContext(context.WithValue(r.Context(), UserContextKey, user))
			before := blobs.uploadCount()
			pulled := httptest.NewRecorder()
			h.GetPullTranscriptContent(pulled, r)
			if pulled.Code != c.ReadStatus {
				t.Fatalf("pull=%d want=%d body=%s", pulled.Code, c.ReadStatus, pulled.Body.String())
			}
			if pulled.Code == http.StatusOK && !bytes.Equal(pulled.Body.Bytes(), raw) {
				t.Fatal("legacy raw pull changed bytes")
			}
			if blobs.uploadCount() != before {
				t.Fatal("raw pull rewrote storage")
			}
			if c.ReadStatus != http.StatusOK {
				var body map[string]string
				if json.Unmarshal(pulled.Body.Bytes(), &body) != nil || body["error"] == "" {
					t.Fatal("refusal is not actionable JSON")
				}
			}
		})
	}
}
