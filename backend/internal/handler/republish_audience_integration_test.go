//go:build integration

package handler

// A republish keeps the audience it found: pre-image by outcome, driven through
// the REAL publish, share and owner-edit handlers against a REAL PostgreSQL.
//
// It has to be a real database. The narrowing and the restore are
// governance-axis writes that only the migration-026 trigger records, the known
// rollback is a real transaction abort, and the ambiguous outcome is PostgreSQL
// refusing a COMMIT the handler cannot classify. A mocked querier would see none
// of that. The object store is the in-memory recording store, because what is
// asserted here is who can read the row, not the ciphertext; the encrypted
// store's own lifecycle is covered by the authoritative publication suite.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/republish-audience.yaml
var republishAudienceYAML []byte

type republishAudienceEvent struct {
	Type       string `yaml:"type"`
	Visibility string `yaml:"visibility"`
}

type republishAudienceCase struct {
	Name             string                   `yaml:"name"`
	Why              string                   `yaml:"why"`
	PreImage         string                   `yaml:"pre_image"`
	Outcome          string                   `yaml:"outcome"`
	License          string                   `yaml:"license"`
	WantStatus       int                      `yaml:"want_status"`
	WantVisibility   string                   `yaml:"want_visibility"`
	WantEvents       []republishAudienceEvent `yaml:"want_events"`
	WantLiveShares   int                      `yaml:"want_live_shares"`
	WantMemberReads  bool                     `yaml:"want_member_reads"`
	ContentReplaced  bool                     `yaml:"content_replaced"`
	WantBodyContains []string                 `yaml:"want_body_contains"`
	WantBodyOmits    []string                 `yaml:"want_body_omits"`
}

// requiredRepublishAudienceCases names every case: each pre-image under each
// outcome, plus the one where the restore and a license move in one update.
// Losing any of them hides a specific way an audience is lost or leaked.
var requiredRepublishAudienceCases = []string{
	"private_success_changes_no_audience",
	"private_object_write_failure_changes_no_audience",
	"private_database_rollback_changes_no_audience",
	"private_ambiguous_commit_says_nothing_about_an_audience",
	"shared_success_restores_the_collective",
	"shared_success_with_a_new_license_restores_in_one_event",
	"shared_object_write_failure_restores_the_collective",
	"shared_database_rollback_restores_the_collective",
	"shared_ambiguous_commit_stays_private",
	"public_success_restores_public",
	"public_object_write_failure_restores_public",
	"public_database_rollback_restores_public",
	"public_ambiguous_commit_stays_private",
}

var republishAudienceOutcomes = []string{"success", "object_write_failure", "database_rollback", "ambiguous_commit"}

var republishAudienceEventTypes = []string{
	string(database.EventLicenseChanged),
	string(database.EventVisibilityChanged),
	string(database.EventGovernanceChanged),
}

func loadRepublishAudienceCases(t *testing.T) []republishAudienceCase {
	t.Helper()
	cases, err := decodeFixtureRows[republishAudienceCase](republishAudienceYAML)
	if err != nil {
		t.Fatalf("load the republish-audience fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if present[c.Name] {
			t.Fatalf("the republish-audience fixture repeats case %q", c.Name)
		}
		present[c.Name] = true
		if c.Why == "" {
			t.Fatalf("case %q states no reason it exists", c.Name)
		}
		if !slices.Contains(shareAttemptVisibilities, c.PreImage) || !slices.Contains(shareAttemptVisibilities, c.WantVisibility) {
			t.Fatalf("case %q uses pre_image %q and want_visibility %q; both come from %v", c.Name, c.PreImage, c.WantVisibility, shareAttemptVisibilities)
		}
		if !slices.Contains(republishAudienceOutcomes, c.Outcome) {
			t.Fatalf("case %q uses outcome %q; the outcomes are %v", c.Name, c.Outcome, republishAudienceOutcomes)
		}
		if c.WantStatus != http.StatusOK && c.WantStatus != http.StatusInternalServerError {
			t.Fatalf("case %q expects status %d; a republish of an existing session answers 200 or, on failure, 500", c.Name, c.WantStatus)
		}
		if (c.Outcome == "success") != (c.WantStatus == http.StatusOK) {
			t.Fatalf("case %q pairs outcome %q with status %d; only a successful republish answers 200", c.Name, c.Outcome, c.WantStatus)
		}
		if c.WantEvents == nil {
			t.Fatalf("case %q does not declare want_events; write [] when the republish must append none, so an "+
				"omitted expectation is never read as one", c.Name)
		}
		for _, event := range c.WantEvents {
			if !slices.Contains(republishAudienceEventTypes, event.Type) || !slices.Contains(shareAttemptVisibilities, event.Visibility) {
				t.Fatalf("case %q expects event %+v, outside the governance events a republish can append", c.Name, event)
			}
		}
		if c.License != "" && !slices.Contains(schema.AllLicenses, schema.License(c.License)) {
			t.Fatalf("case %q carries license %q, which is not on the contract's menu", c.Name, c.License)
		}
	}
	for _, required := range requiredRepublishAudienceCases {
		if !present[required] {
			t.Fatalf("the republish-audience fixture no longer contains %q. That case exists because losing it hides a "+
				"real way an audience is lost or leaked; restore it rather than removing it from this manifest.", required)
		}
	}
	return cases
}

func TestRepublishKeepsTheAudience(t *testing.T) {
	cases := loadRepublishAudienceCases(t)
	ctx := context.Background()
	pool := publishLockPool(t, 8)
	t.Cleanup(pool.Close)
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct title pipeline: %v", err)
	}

	for _, testCase := range cases {
		t.Run(testCase.Name, func(t *testing.T) {
			blobs := &failableTranscriptBlobStore{recordingTranscriptBlobStore: newRecordingTranscriptBlobStore()}
			h := &Handler{pool: pool, queries: sqlc.New(pool), blobs: blobs, titles: titles, cfg: &config.Config{FrontendURL: "https://village.example"}}
			world := newRepublishWorld(t, ctx, pool, h, testCase.PreImage)
			defer world.cleanup(t, ctx)

			var seqBefore int64
			var hashBefore string
			if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(seq), 0) FROM transcript_governance_events_audit WHERE transcript_id = $1`,
				world.transcript).Scan(&seqBefore); err != nil {
				t.Fatalf("read the audit position before the republish: %v", err)
			}
			if err := pool.QueryRow(ctx, `SELECT content_hash FROM transcripts WHERE id = $1`, world.transcript).Scan(&hashBefore); err != nil {
				t.Fatalf("read the content hash before the republish: %v", err)
			}

			switch testCase.Outcome {
			case "object_write_failure":
				blobs.failNextWrite.Store(true)
			case "database_rollback":
				installRepublishRollback(t, ctx, pool, world.transcript)
			case "ambiguous_commit":
				installRepublishCommitRefusal(t, ctx, pool, world.transcript)
			}

			code, body := world.publish(t, republishAudienceRevisedContent(), testCase.License)

			if code != testCase.WantStatus {
				t.Fatalf("republish status = %d, want %d (%s); body: %s", code, testCase.WantStatus, testCase.Why, body)
			}
			for _, needle := range testCase.WantBodyContains {
				if !strings.Contains(body, needle) {
					t.Errorf("the answer does not say %q (%s); body: %s", needle, testCase.Why, body)
				}
			}
			for _, needle := range testCase.WantBodyOmits {
				if strings.Contains(body, needle) {
					t.Errorf("the answer says %q, which is not true of this outcome (%s); body: %s", needle, testCase.Why, body)
				}
			}
			if code == http.StatusOK {
				// The receipt reports the visibility the transcript now has, which
				// is what a client renders; it must be the restored one.
				var receipt struct {
					Transcript struct {
						Visibility string `json:"visibility"`
					} `json:"transcript"`
				}
				if err := json.Unmarshal([]byte(body), &receipt); err != nil {
					t.Fatalf("decode the republish receipt: %v (body: %s)", err, body)
				}
				if receipt.Transcript.Visibility != testCase.WantVisibility {
					t.Errorf("the receipt reports visibility %q, want %q (%s)", receipt.Transcript.Visibility, testCase.WantVisibility, testCase.Why)
				}
			}

			world.assertVisibility(t, ctx, testCase)
			world.assertEvents(t, ctx, seqBefore, testCase)
			world.assertLiveShares(t, ctx, testCase)
			world.assertMemberReads(t, testCase)

			var hashAfter string
			if err := pool.QueryRow(ctx, `SELECT content_hash FROM transcripts WHERE id = $1`, world.transcript).Scan(&hashAfter); err != nil {
				t.Fatalf("read the content hash after the republish: %v", err)
			}
			if replaced := hashAfter != hashBefore; replaced != testCase.ContentReplaced {
				t.Errorf("content replaced = %v, want %v (%s): the outcome this case names did not happen", replaced, testCase.ContentReplaced, testCase.Why)
			}
			assertProjectionMatchesLedger(t, ctx, pool, testCase.Name)
		})
	}
}

// republishWorld is one owner with one published session, one open collective
// with a moderator, and one member of that collective who is not the owner.
type republishWorld struct {
	h          *Handler
	pool       *pgxpool.Pool
	owner      *AuthUser
	moderator  pgtype.UUID
	member     *AuthUser
	group      pgtype.UUID
	sessionID  string
	transcript pgtype.UUID
}

func newRepublishWorld(t *testing.T, ctx context.Context, pool *pgxpool.Pool, h *Handler, preImage string) *republishWorld {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	w := &republishWorld{h: h, pool: pool, sessionID: uuid.NewString()}
	ownerName, memberName := "republish-owner-"+suffix, "republish-member-"+suffix
	w.owner = &AuthUser{ID: uuid.UUID(shareInsertUser(t, ctx, pool, ownerName).Bytes), Username: ownerName}
	w.member = &AuthUser{ID: uuid.UUID(shareInsertUser(t, ctx, pool, memberName).Bytes), Username: memberName}
	w.moderator = shareInsertUser(t, ctx, pool, "republish-mod-"+suffix)
	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, acceptance_mode) VALUES ($1, $2, 'open') RETURNING id
	`, "republish-"+suffix, w.moderator).Scan(&w.group); err != nil {
		t.Fatalf("create the collective: %v", err)
	}
	shareAddMember(t, ctx, pool, w.group, w.moderator, "owner")
	shareAddMember(t, ctx, pool, w.group, w.owner.PgID(), "member")
	shareAddMember(t, ctx, pool, w.group, w.member.PgID(), "member")

	if code, body := w.publish(t, attachmentPublicationContent(), ""); code != http.StatusCreated {
		t.Fatalf("first publish status = %d (%s), want 201", code, body)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM transcripts WHERE owner_id = $1 AND local_id = $2`, w.owner.PgID(), w.sessionID).Scan(&w.transcript); err != nil {
		t.Fatalf("read the published transcript: %v", err)
	}

	// The pre-image is reached the way a person reaches it.
	switch preImage {
	case dbVisibilityShared:
		body, err := json.Marshal(map[string][]string{"group_ids": {uuid.UUID(w.group.Bytes).String()}})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/"+uuid.UUID(w.transcript.Bytes).String()+"/share", bytes.NewReader(body))
		r = withChiURLParam(r, "id", uuid.UUID(w.transcript.Bytes).String())
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, w.owner))
		rec := httptest.NewRecorder()
		h.ShareTranscript(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("share status = %d (%s), want 200", rec.Code, rec.Body.String())
		}
	case dbVisibilityPublic:
		if rec := transcriptVisibilityPatch(t, h, w.owner, w.transcript, dbVisibilityPublic); rec.Code != http.StatusOK {
			t.Fatalf("widen status = %d (%s), want 200", rec.Code, rec.Body.String())
		}
	}
	var visibility string
	if err := pool.QueryRow(ctx, `SELECT visibility FROM transcripts WHERE id = $1`, w.transcript).Scan(&visibility); err != nil {
		t.Fatalf("read the pre-image: %v", err)
	}
	if visibility != preImage {
		t.Fatalf("the setup reached visibility %q, want the pre-image %q; the case would prove nothing", visibility, preImage)
	}
	return w
}

func (w *republishWorld) cleanup(t *testing.T, ctx context.Context) {
	t.Helper()
	if _, err := w.pool.Exec(ctx, "DELETE FROM groups WHERE id = $1", w.group); err != nil {
		t.Errorf("cleanup collective: %v", err)
	}
	cleanupOwners(t, ctx, w.pool, w.owner.PgID(), w.member.PgID(), w.moderator)
}

// publish sends the owner's session through the mounted publish handler.
func (w *republishWorld) publish(t *testing.T, content []byte, license string) (int, string) {
	t.Helper()
	metadata := schema.PublishRequest{
		Identity:    schema.SessionIdentity{SessionID: schema.SessionID(w.sessionID), SchemaVersion: 2},
		Model:       schema.ModelInfo{Harness: schema.HarnessClaudeCode, Model: "republish-audience"},
		Timestamp:   schema.TimestampInfo{Start: 1700000000000, End: 1700000060000},
		Source:      schema.SourceInfo{FilePath: "/fixtures/republish.jsonl", Format: "jsonl"},
		Project:     schema.ProjectContext{Hash: testProjectHash, Name: "republish-fixture"},
		Stats:       schema.SessionStats{TurnCount: 2, DurationMs: 1000, TokensIn: 60, TokensOut: 40},
		Diagnostics: schema.DiagnosticsInfo{Warnings: []schema.DiagnosticEntry{}},
		License:     schema.License(license),
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal republish metadata: %v", err)
	}
	body, boundary := multipartBody(t, map[string]string{"metadata": string(metadataJSON)}, string(content))
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transcripts/publish", body)
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r = r.WithContext(context.WithValue(r.Context(), UserContextKey, w.owner))
	rec := httptest.NewRecorder()
	w.h.PublishTranscript(rec, r)
	return rec.Code, rec.Body.String()
}

func (w *republishWorld) assertVisibility(t *testing.T, ctx context.Context, testCase republishAudienceCase) {
	t.Helper()
	var visibility string
	if err := w.pool.QueryRow(ctx, `SELECT visibility FROM transcripts WHERE id = $1`, w.transcript).Scan(&visibility); err != nil {
		t.Fatalf("read the visibility after the republish: %v", err)
	}
	if visibility != testCase.WantVisibility {
		t.Errorf("visibility = %q after the republish, want %q (%s)", visibility, testCase.WantVisibility, testCase.Why)
	}
}

// assertEvents compares the governance events the republish appended with the
// case, and requires every one to be attributed to the owner: the narrowing and
// the restore are the publisher's own actions, never the system's.
func (w *republishWorld) assertEvents(t *testing.T, ctx context.Context, seqBefore int64, testCase republishAudienceCase) {
	t.Helper()
	rows, err := w.pool.Query(ctx, `
		SELECT event_type, visibility, changed_by FROM transcript_governance_events_audit
		WHERE transcript_id = $1 AND seq > $2 ORDER BY seq
	`, w.transcript, seqBefore)
	if err != nil {
		t.Fatalf("read the audit trail: %v", err)
	}
	defer rows.Close()
	got := []republishAudienceEvent{}
	for rows.Next() {
		var event republishAudienceEvent
		var actor pgtype.UUID
		if err := rows.Scan(&event.Type, &event.Visibility, &actor); err != nil {
			t.Fatal(err)
		}
		if actor != w.owner.PgID() {
			t.Errorf("event %+v is attributed to %s, want the owner: a republish acts as its publisher", event, uuid.UUID(actor.Bytes))
		}
		got = append(got, event)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the audit trail: %v", err)
	}
	if !slices.Equal(got, testCase.WantEvents) {
		t.Errorf("the republish appended %+v, want %+v (%s)", got, testCase.WantEvents, testCase.Why)
	}
}

// assertLiveShares counts the collectives whose latest attempt is pending or
// approved. The count is written here from the ledger, independently of the
// production query that decides the same question for an unshare.
func (w *republishWorld) assertLiveShares(t *testing.T, ctx context.Context, testCase republishAudienceCase) {
	t.Helper()
	var live int
	if err := w.pool.QueryRow(ctx, `
		SELECT count(*)::int FROM (
			SELECT DISTINCT ON (group_id) status FROM transcript_share_attempts
			WHERE transcript_id = $1 ORDER BY group_id, event_num DESC
		) latest WHERE status IN ('pending', 'approved')
	`, w.transcript).Scan(&live); err != nil {
		t.Fatalf("count the live shares: %v", err)
	}
	if live != testCase.WantLiveShares {
		t.Errorf("live shares = %d after the republish, want %d (%s): a republish never touches the ledger", live, testCase.WantLiveShares, testCase.Why)
	}
}

// assertMemberReads opens the transcript as a member of the collective who is
// not its owner. This is the audience itself, asked through the mounted read.
func (w *republishWorld) assertMemberReads(t *testing.T, testCase republishAudienceCase) {
	t.Helper()
	rec := transcriptViewAs(t, w.h, w.member, w.transcript)
	want := http.StatusNotFound
	if testCase.WantMemberReads {
		want = http.StatusOK
	}
	if rec.Code != want {
		t.Errorf("a collective member's read answered %d, want %d (%s)", rec.Code, want, testCase.Why)
	}
}

// republishAudienceRevisedContent is the republished session: the first
// publish's content with a changed prompt, so the stored hash has to move.
func republishAudienceRevisedContent() []byte {
	return bytes.Replace(attachmentPublicationContent(),
		[]byte("please attach my prompts"), []byte("please attach my prompts, revised"), 1)
}

// failableTranscriptBlobStore fails the next object write on request, before
// anything is staged.
type failableTranscriptBlobStore struct {
	*recordingTranscriptBlobStore
	failNextWrite atomic.Bool
}

func (s *failableTranscriptBlobStore) Write(ctx context.Context, id uuid.UUID, contents []byte) (storage.BlobDescriptor, storage.ContentIdentity, error) {
	if s.failNextWrite.CompareAndSwap(true, false) {
		return storage.BlobDescriptor{}, storage.ContentIdentity{}, errors.New("injected object write failure")
	}
	return s.recordingTranscriptBlobStore.Write(ctx, id, contents)
}

// installRepublishRollback makes the replacement UPDATE of one transcript raise,
// so the replacement transaction is known to have rolled back.
func installRepublishRollback(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID) {
	t.Helper()
	name := "republish_rollback_" + strings.ReplaceAll(uuid.UUID(id.Bytes).String(), "-", "")
	installRepublishFailureTrigger(t, ctx, pool, name, `
		CREATE TRIGGER `+name+` BEFORE UPDATE OF blob_key ON transcripts FOR EACH ROW
		WHEN (NEW.id = '`+uuid.UUID(id.Bytes).String()+`'::uuid AND NEW.blob_key IS DISTINCT FROM OLD.blob_key)
		EXECUTE FUNCTION `+name+`()`)
}

// installRepublishCommitRefusal defers the same refusal to COMMIT. PostgreSQL
// then rolls the transaction back while answering the COMMIT with an error
// rather than with a rollback, which is exactly the answer the handler cannot
// tell from a lost acknowledgement of a commit that happened: the outcome it
// must treat as ambiguous.
func installRepublishCommitRefusal(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID) {
	t.Helper()
	name := "republish_commit_" + strings.ReplaceAll(uuid.UUID(id.Bytes).String(), "-", "")
	installRepublishFailureTrigger(t, ctx, pool, name, `
		CREATE CONSTRAINT TRIGGER `+name+` AFTER UPDATE OF blob_key ON transcripts
		DEFERRABLE INITIALLY DEFERRED FOR EACH ROW
		WHEN (NEW.id = '`+uuid.UUID(id.Bytes).String()+`'::uuid AND NEW.blob_key IS DISTINCT FROM OLD.blob_key)
		EXECUTE FUNCTION `+name+`()`)
}

func installRepublishFailureTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, createTrigger string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS `+name+` ON transcripts; DROP FUNCTION IF EXISTS `+name+`()`); err != nil {
			t.Errorf("remove the injected failure %s: %v", name, err)
		}
	})
	if _, err := pool.Exec(ctx, `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected republish failure'; END $$`); err != nil {
		t.Fatalf("install the injected failure function: %v", err)
	}
	if _, err := pool.Exec(ctx, createTrigger); err != nil {
		t.Fatalf("install the injected failure trigger: %v", err)
	}
}
