//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/conversation-turn-count-publication.yaml
var conversationTurnCountPublicationYAML []byte

// requiredConversationTurnCountPublicationSteps is the name manifest for
// testdata/conversation-turn-count-publication.yaml. Exact membership, never a
// count.
var requiredConversationTurnCountPublicationSteps = []string{
	"first-publish-without-the-count-stores-null",
	"republish-with-the-count-stores-it",
	"null-is-refused",
	"fraction-is-refused",
	"negative-is-refused",
	"above-the-safe-maximum-is-refused",
	"republish-without-the-count-clears-it",
	"measured-zero-is-stored-as-zero",
	"safe-maximum-is-stored",
	"legacy-publish-stores-no-count",
}

type conversationTurnCountPublicationFixture struct {
	TurnCount int `yaml:"turn_count"`
	Steps     []struct {
		Name      string `yaml:"name"`
		Legacy    bool   `yaml:"legacy"`
		CountJSON string `yaml:"count_json"`
		Accepted  bool   `yaml:"accepted"`
		Error     string `yaml:"error"`
		Stored    *int64 `yaml:"stored"`
		Displayed *int64 `yaml:"displayed"`
	} `yaml:"steps"`
}

// TestConversationTurnCountPublication_RealPostgres drives the mounted publish
// handler through a first publish and its republishes. It proves the pinned
// contract refuses a malformed count before any side effect, that an accepted
// count is stored as published, and that a republish without it clears it.
func TestConversationTurnCountPublication_RealPostgres(t *testing.T) {
	fixture, err := decodeFixtureDocument[conversationTurnCountPublicationFixture](conversationTurnCountPublicationYAML)
	if err != nil {
		t.Fatalf("load testdata/conversation-turn-count-publication.yaml: %v", err)
	}
	present := map[string]struct{}{}
	for _, step := range fixture.Steps {
		if _, repeated := present[step.Name]; repeated {
			t.Fatalf("testdata/conversation-turn-count-publication.yaml repeats %q", step.Name)
		}
		present[step.Name] = struct{}{}
		if step.Accepted == (step.Error != "") || step.Accepted != (step.Displayed != nil) || !step.Accepted && step.Stored != nil {
			t.Fatalf("step %q must be either accepted with its displayed value or refused with its error", step.Name)
		}
		if step.Stored != nil && *step.Stored == int64(fixture.TurnCount) {
			t.Fatalf("step %q stores the same number as turn_count; the two counts must differ", step.Name)
		}
	}
	assertExactTitleFixtureNames(t, "conversation-turn-count-publication", present, requiredConversationTurnCountPublicationSteps)

	ctx := context.Background()
	pool := govTestPool(t)
	t.Cleanup(pool.Close)
	owner := pullInsertUser(t, ctx, pool, 7_100_000_000+int64(uuid.New().ID()), "conversation-count-"+uuid.NewString()[:8])
	t.Cleanup(func() { cleanupOwners(t, context.Background(), pool, owner) })
	blobs := &graphPublicationBlobObserver{TranscriptBlobStore: authoritativeTestBlobStore(t)}
	h := New(&config.Config{FrontendURL: "https://example.test"}, pool, blobs)
	user := &AuthUser{ID: uuidFromPg(owner), Username: "conversation-count-owner"}
	routes := chi.NewRouter()
	routes.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), UserContextKey, user)))
		})
	})
	routes.Post("/api/v1/transcripts/publish", h.PublishTranscript)
	routes.Get("/api/v1/transcripts/{id}", h.GetTranscript)
	queries := sqlc.New(pool)
	sessionID := uuid.NewString()

	var transcriptID uuid.UUID
	t.Cleanup(func() {
		if transcriptID == uuid.Nil {
			return
		}
		row, err := queries.GetTranscriptByID(context.Background(), toPgUUID(transcriptID))
		if err != nil {
			t.Errorf("load final descriptor for cleanup: %v", err)
			return
		}
		descriptor, err := descriptorFromTranscript(row)
		if err != nil {
			t.Errorf("decode cleanup descriptor: %v", err)
			return
		}
		if err := blobs.Delete(context.Background(), descriptor); err != nil {
			t.Errorf("remove test ciphertext: %v", err)
		}
	})

	stateSQL := `SELECT jsonb_build_object('transcripts', (SELECT jsonb_agg(to_jsonb(t) ORDER BY t.id) FROM transcripts t WHERE owner_id=$1), 'audit', (SELECT jsonb_agg(to_jsonb(a) ORDER BY a.seq) FROM transcript_governance_events_audit a WHERE changed_by=$1), 'shares', (SELECT jsonb_agg(to_jsonb(s) ORDER BY s.id) FROM transcript_share_attempts s JOIN transcripts t ON t.id=s.transcript_id WHERE t.owner_id=$1))::text`
	// The steps share one transcript on purpose, so they run in order and a
	// failed step stops the sequence instead of asserting against stale state.
	for _, step := range fixture.Steps {
		ok := t.Run(step.Name, func(t *testing.T) {
			// Each step replaces the content, as a real republish does.
			content := bytes.Replace(attachmentPublicationContent(), []byte("please attach my prompts"), []byte("please attach my prompts, "+step.Name), 1)
			content = bytes.Replace(content, []byte("attachment-session"), []byte(sessionID), 1)
			// The authoritative request is what a current client sends, and the
			// only one whose decoder enforces the count. A legacy step sends the
			// frozen compatibility shape instead.
			var request any = schema.AuthoritativePublishRequest{
				Identity:    schema.AuthoritativeSessionIdentity{SessionID: schema.SessionID(sessionID), SchemaVersion: 11},
				Model:       schema.AuthoritativeModelInfo{Harness: schema.HarnessClaudeCode, Model: "conversation-count"},
				Timestamp:   schema.AuthoritativeTimestampInfo{Start: 1700000000000, End: 1700000060000},
				Source:      schema.AuthoritativeSourceInfo{Format: schema.SourceFormatJSON},
				Project:     schema.AuthoritativeProjectContext{Hash: testProjectHash, Name: "conversation-count-fixture"},
				Stats:       schema.AuthoritativeSessionStats{TurnCount: fixture.TurnCount},
				ContentHash: schema.ComputeTranscriptContentHash(content),
			}
			if step.Legacy {
				request = schema.PublishRequest{
					Identity:    schema.SessionIdentity{SessionID: schema.SessionID(sessionID), SchemaVersion: 2},
					Model:       schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "conversation-count"},
					Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
					Source:      schema.SourceInfo{FilePath: "/fixtures/conversation-count.jsonl", Format: "jsonl"},
					Project:     schema.ProjectContext{Hash: testProjectHash, Name: "conversation-count-fixture"},
					Stats:       schema.SessionStats{TurnCount: fixture.TurnCount},
					Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
				}
			}
			metadata, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			// The count is written as raw JSON so null and a fraction reach the
			// handler exactly as a client would send them. The typed stats
			// struct holds a whole number or nothing, so it cannot carry those
			// refused values; the two object keys are read from the contract
			// type instead of being restated here.
			statsKey := wireKeyOf(schema.AuthoritativePublishRequest{}, schema.AuthoritativePublishRequest{Stats: schema.AuthoritativeSessionStats{TurnCount: 1}})
			countKey := wireKeyOf(schema.AuthoritativeSessionStats{}, schema.AuthoritativeSessionStats{ConversationTurnCount: new(int64)})
			var document map[string]json.RawMessage
			if err := json.Unmarshal(metadata, &document); err != nil {
				t.Fatal(err)
			}
			var stats map[string]json.RawMessage
			if err := json.Unmarshal(document[statsKey], &stats); err != nil {
				t.Fatal(err)
			}
			if _, already := stats[countKey]; already {
				t.Fatalf("the base metadata already carries %s", countKey)
			}
			if step.CountJSON != "" {
				stats[countKey] = json.RawMessage(step.CountJSON)
			}
			if document[statsKey], err = json.Marshal(stats); err != nil {
				t.Fatal(err)
			}
			if metadata, err = json.Marshal(document); err != nil {
				t.Fatal(err)
			}

			var beforeState string
			if err := pool.QueryRow(ctx, stateSQL, owner).Scan(&beforeState); err != nil {
				t.Fatal(err)
			}
			beforeWrites := blobs.writes.Load()
			body, boundary := multipartBody(t, map[string]string{"metadata": string(metadata)}, string(content))
			r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
			r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
			w := httptest.NewRecorder()
			routes.ServeHTTP(w, r)

			if !step.Accepted {
				// The contract 422, told apart from the secret-scan 422 by its body.
				if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "metadata failed schema validation") || !strings.Contains(w.Body.String(), step.Error) {
					t.Fatalf("refusal status=%d body=%s, want the contract 422 naming %q", w.Code, w.Body.String(), step.Error)
				}
				var afterState string
				if err := pool.QueryRow(ctx, stateSQL, owner).Scan(&afterState); err != nil {
					t.Fatal(err)
				}
				if beforeState != afterState || blobs.writes.Load() != beforeWrites {
					t.Fatal("a refused count changed a row, an audit entry, a share attempt or a stored object")
				}
				return
			}
			if w.Code != http.StatusCreated && w.Code != http.StatusOK {
				t.Fatalf("publish status=%d body=%s", w.Code, w.Body.String())
			}
			if blobs.writes.Load() == beforeWrites {
				t.Fatal("the publish wrote no object; the step did not replace the content")
			}
			var id uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT id FROM transcripts WHERE owner_id=$1 AND local_id=$2`, owner, sessionID).Scan(&id); err != nil {
				t.Fatalf("the accepted publish stored no transcript for the session: %v", err)
			}
			if transcriptID != uuid.Nil && transcriptID != id {
				t.Fatal("the republish created another transcript")
			}
			transcriptID = id

			row, err := queries.GetTranscriptByID(ctx, toPgUUID(id))
			if err != nil {
				t.Fatal(err)
			}
			if got := pgInt8ToInt64Ptr(row.ConversationTurnCount); !reflect.DeepEqual(got, step.Stored) {
				t.Fatalf("stored conversation_turn_count = %v, want %v", derefCount(got), derefCount(step.Stored))
			}
			if got := pgInt8ToInt64Ptr(row.DisplayedTurnCount); !reflect.DeepEqual(got, step.Displayed) {
				t.Fatalf("stored displayed_turn_count = %v, want %v", derefCount(got), derefCount(step.Displayed))
			}
			if !row.TurnCount.Valid || int(row.TurnCount.Int32) != fixture.TurnCount {
				t.Fatalf("stored turn_count = %v, want %d unchanged", row.TurnCount, fixture.TurnCount)
			}

			read := httptest.NewRecorder()
			routes.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+id.String(), nil))
			if read.Code != http.StatusOK {
				t.Fatalf("metadata read: %d %s", read.Code, read.Body.String())
			}
			var served struct {
				Transcript transcriptResponse `json:"transcript"`
			}
			if err := json.Unmarshal(read.Body.Bytes(), &served); err != nil {
				t.Fatal(err)
			}
			if response := served.Transcript; !reflect.DeepEqual(response.ConversationTurnCount, step.Stored) || !reflect.DeepEqual(response.DisplayedTurnCount, step.Displayed) {
				t.Fatalf("the metadata read serves conversation_turn_count=%v displayed_turn_count=%v, want %v and %v",
					derefCount(response.ConversationTurnCount), derefCount(response.DisplayedTurnCount), derefCount(step.Stored), derefCount(step.Displayed))
			}
		})
		if !ok {
			t.Fatalf("step %q failed; later steps republish the same transcript and are not run", step.Name)
		}
	}
}

func derefCount(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
