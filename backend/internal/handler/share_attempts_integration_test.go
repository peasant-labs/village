//go:build integration

package handler

// The share-attempt lattice, driven through the REAL HTTP handlers against a
// REAL PostgreSQL.
//
// It has to be a real database. The current-state share row is written by a
// trigger, so a Go test computing its own expected values would pass with no
// trigger installed at all; and because moderation decisions and withdrawals
// are UPDATEs, a trigger narrowed to INSERT would keep every mock-backed
// assertion green while silently propagating nothing. Each case therefore reads
// the attempt sequence and the derived row back out of the database after every
// transition.

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
)

//go:embed testdata/share-attempts.yaml
var shareAttemptsYAML []byte

type shareAttemptStep struct {
	Do     string `yaml:"do"`
	Status string `yaml:"status"`
	Expect string `yaml:"expect"`
}

type shareAttemptCase struct {
	Name                string             `yaml:"name"`
	Why                 string             `yaml:"why"`
	Acceptance          string             `yaml:"acceptance"`
	Steps               []shareAttemptStep `yaml:"steps"`
	Attempts            []string           `yaml:"attempts"`
	Derived             string             `yaml:"derived"`
	ApprovedTranscripts int                `yaml:"approved_transcripts"`
	PendingTranscripts  int                `yaml:"pending_transcripts"`
	ApprovedAttempts    int                `yaml:"approved_attempts"`
	RejectedAttempts    int                `yaml:"rejected_attempts"`
}

// requiredShareAttemptCases names the cases that must exist. Each one is here
// because losing it would hide a specific failure: the index wedge that blocks
// re-submission forever, the in-place edit of decided history, the duplicate
// refusals, the one case where counting transcripts and counting attempts
// disagree, and the ordering conflict that used to reach the person as an
// unexplained failure.
var requiredShareAttemptCases = []string{
	"first_submission_to_open_collective",
	"first_submission_to_curated_collective",
	"rejected_then_resubmitted_then_rejected_again",
	"rejected_then_resubmitted_then_approved",
	"duplicate_submission_while_pending_refused",
	"submission_while_approved_refused",
	"owner_unshare_of_approved_contribution",
	"owner_unshare_of_pending_submission",
	"unshare_while_pending_then_reshare",
	"collective_removal_of_approved_contribution",
	"reshare_after_revocation",
	"leaving_the_collective_retracts_the_live_attempt",
	"terminal_attempt_update_refused",
	"reshare_after_retraction_counts_one_transcript",
	"competing_event_ordinal_answers_409_not_an_unexplained_failure",
}

func loadShareAttemptCases(t *testing.T) []shareAttemptCase {
	t.Helper()
	cases, err := decodeFixtureRows[shareAttemptCase](shareAttemptsYAML)
	if err != nil {
		t.Fatalf("load the share-attempt fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range cases {
		if present[c.Name] {
			t.Fatalf("the share-attempt fixture repeats case %q", c.Name)
		}
		present[c.Name] = true
		if c.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", c.Name)
		}
		if c.Acceptance != "open" && c.Acceptance != "curated" {
			t.Fatalf("case %q uses acceptance %q; the fixture drives open and curated collectives", c.Name, c.Acceptance)
		}
		if len(c.Steps) == 0 {
			t.Fatalf("case %q performs no steps", c.Name)
		}
	}
	for _, required := range requiredShareAttemptCases {
		if !present[required] {
			t.Fatalf("the share-attempt fixture no longer contains %q. That case exists because losing it hides a real "+
				"failure; restore it rather than removing it from this manifest.", required)
		}
	}
	return cases
}

func TestShareAttemptLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	h := New(&config.Config{}, pool, nil)

	for _, testCase := range loadShareAttemptCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			world := newShareWorld(t, ctx, pool, testCase.Name, testCase.Acceptance)
			defer world.cleanup(t, ctx)

			for i, step := range testCase.Steps {
				world.run(t, ctx, h, i, step)
			}

			world.assertAttempts(t, ctx, testCase)
			world.assertDerivedRow(t, ctx, testCase)
			world.assertCounts(t, ctx, testCase)
			// The per-case assertions above check this pair. This one checks the
			// WHOLE projection against a latest-event fold over the whole ledger,
			// so a transition that corrupted some OTHER pair cannot pass unseen.
			assertProjectionMatchesLedger(t, ctx, world.pool, testCase.Name)
		})
	}
}

// shareWorld is one owner, one moderator, one collective and one transcript.
type shareWorld struct {
	owner      pgtype.UUID
	ownerName  string
	moderator  pgtype.UUID
	group      pgtype.UUID
	transcript pgtype.UUID
	localID    string
	pool       *pgxpool.Pool
}

func newShareWorld(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name, acceptance string) *shareWorld {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	w := &shareWorld{pool: pool, ownerName: "share-owner-" + suffix}
	w.owner = shareInsertUser(t, ctx, pool, w.ownerName)
	w.moderator = shareInsertUser(t, ctx, pool, "share-mod-"+suffix)

	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, acceptance_mode) VALUES ($1, $2, $3) RETURNING id
	`, name+"-"+suffix, w.moderator, acceptance).Scan(&w.group); err != nil {
		t.Fatalf("create the collective: %v", err)
	}
	shareAddMember(t, ctx, pool, w.group, w.moderator, "owner")
	shareAddMember(t, ctx, pool, w.group, w.owner, "member")
	w.localID = "share-" + suffix
	w.transcript = shareInsertTranscript(t, ctx, pool, w.owner, w.localID)
	return w
}

func (w *shareWorld) cleanup(t *testing.T, ctx context.Context) {
	t.Helper()
	if _, err := w.pool.Exec(ctx, "DELETE FROM groups WHERE id = $1", w.group); err != nil {
		t.Errorf("cleanup collective: %v", err)
	}
	cleanupOwners(t, ctx, w.pool, w.owner, w.moderator)
}

func (w *shareWorld) run(t *testing.T, ctx context.Context, h *Handler, index int, step shareAttemptStep) {
	t.Helper()
	switch step.Do {
	case "submit":
		w.expectStatus(t, index, step, w.submit(t, h))
	case "decide":
		w.expectStatus(t, index, step, w.decide(t, h, step.Status))
	case "unshare":
		w.expectStatus(t, index, step, w.unshare(t, h))
	case "remove":
		w.expectStatus(t, index, step, w.remove(t, h))
	case "leave_collective":
		w.expectStatus(t, index, step, w.leaveCollective(t, h))
	case "competing_event_ordinal":
		w.expectStatus(t, index, step, w.submitAgainstACompetingEventOrdinal(t, ctx, h))
	case "rewrite_terminal_attempt":
		w.rewriteTerminalAttempt(t, ctx, index, step)
	default:
		t.Fatalf("step %d asks for %q, which this fixture interpreter does not perform", index, step.Do)
	}
}

func (w *shareWorld) expectStatus(t *testing.T, index int, step shareAttemptStep, status int) {
	t.Helper()
	switch step.Expect {
	case "ok":
		if status != http.StatusOK {
			t.Fatalf("step %d (%s) returned %d, want 200", index, step.Do, status)
		}
	case "refused":
		if status != http.StatusConflict {
			t.Fatalf("step %d (%s) returned %d, want 409: a duplicate submission must be refused with an answer the "+
				"person can act on, not silently discarded", index, step.Do, status)
		}
	default:
		t.Fatalf("step %d declares expect %q; the outcomes are ok and refused", index, step.Expect)
	}
}

func (w *shareWorld) request(actor pgtype.UUID, username, method, target string, body []byte, params map[string]string) *http.Request {
	route := chi.NewRouteContext()
	for key, value := range params {
		route.URLParams.Add(key, value)
	}
	reqCtx := context.WithValue(context.Background(), UserContextKey, &AuthUser{ID: uuid.UUID(actor.Bytes), Username: username})
	reqCtx = context.WithValue(reqCtx, chi.RouteCtxKey, route)
	return httptest.NewRequest(method, target, bytes.NewReader(body)).WithContext(reqCtx)
}

func (w *shareWorld) submit(t *testing.T, h *Handler) int {
	t.Helper()
	body, err := json.Marshal(map[string][]string{"group_ids": {uuid.UUID(w.group.Bytes).String()}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ShareTranscript(rec, w.request(w.owner, w.ownerName, http.MethodPost, "/api/v1/transcripts/share", body,
		map[string]string{"id": uuid.UUID(w.transcript.Bytes).String()}))
	return rec.Result().StatusCode
}

// submitAgainstACompetingEventOrdinal offers the transcript while ANOTHER writer
// - a whole-project contribution, or any other appender - is recording an event
// for the same pair that this submission cannot yet see.
//
// The single share holds the transcript's publish advisory lock for the whole of
// its work, so two single shares of one transcript serialize and can never race
// here; the competing writer is therefore simulated with a transaction of its
// own, which is exactly the shape a concurrent batch presents. This test holds
// the lock first so the share is gated, appends an ACCEPTED event at ordinal 1
// without committing it, releases the lock, waits until the share is genuinely
// blocked inside the unique index, and only then commits. The share's own insert
// then violates UNIQUE (transcript_id, group_id, event_num).
//
// It must reach the person as a 409 that names the transcript and says asking
// again will work. Before the shared conflict classifier it fell through to a
// 500 that named nothing - the same gap the whole-project path had.
func (w *shareWorld) submitAgainstACompetingEventOrdinal(t *testing.T, ctx context.Context, h *Handler) int {
	t.Helper()
	lockKey := sessionPublishLockKey(w.owner, w.localID)
	lockConn, err := w.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the connection that holds the publish lock: %v", err)
	}
	lockReleased := false
	defer func() {
		if !lockReleased {
			_, _ = lockConn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockKey)
		}
		lockConn.Release()
	}()
	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", lockKey); err != nil {
		t.Fatalf("hold the publish lock for the transcript: %v", err)
	}

	body, err := json.Marshal(map[string][]string{"group_ids": {uuid.UUID(w.group.Bytes).String()}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	rec := httptest.NewRecorder()
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ShareTranscript(rec, w.request(w.owner, w.ownerName, http.MethodPost, "/api/v1/transcripts/share", body,
			map[string]string{"id": uuid.UUID(w.transcript.Bytes).String()}))
	}()
	w.waitForLockWaiter(t, ctx, "advisory")

	competing, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the competing writer's transaction: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = competing.Rollback(context.Background())
		}
	}()
	if _, err := competing.Exec(ctx, `
		INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
		VALUES ($1, $2, 1, 'approved')`, w.transcript, w.group); err != nil {
		t.Fatalf("append the competing event: %v", err)
	}

	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockKey); err != nil {
		t.Fatalf("release the publish lock: %v", err)
	}
	lockReleased = true
	w.waitForLockWaiter(t, ctx, "transactionid")
	if err := competing.Commit(ctx); err != nil {
		t.Fatalf("commit the competing event: %v", err)
	}
	committed = true
	wg.Wait()

	var refusal struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refusal); err != nil {
		t.Fatalf("decode the refusal: %v (body: %s)", err, rec.Body.String())
	}
	if !strings.Contains(refusal.Error, uuid.UUID(w.transcript.Bytes).String()) {
		t.Errorf("the refusal does not name the transcript. A conflict answer that names nothing cannot be acted on. "+
			"Message: %s", refusal.Error)
	}
	if !strings.Contains(refusal.Error, "lost the race") {
		t.Errorf("the refusal does not say the submission lost a race. An ordering conflict is cleared by asking "+
			"again, unlike a duplicate submission, so answering both the same way tells half the callers the wrong "+
			"thing. Message: %s", refusal.Error)
	}
	return rec.Result().StatusCode
}

// waitForLockWaiter blocks until PostgreSQL reports a session waiting on a lock
// of the given kind, so each stage of the race is OBSERVED rather than assumed
// after a sleep.
func (w *shareWorld) waitForLockWaiter(t *testing.T, ctx context.Context, lockType string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := w.pool.QueryRow(ctx,
			"SELECT count(*)::int FROM pg_locks WHERE locktype = $1 AND NOT granted", lockType).Scan(&waiting); err != nil {
			t.Fatalf("look for a session waiting on a %s lock: %v", lockType, err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no session ever waited on a %s lock, so the race this case describes never happened and it would prove "+
		"nothing", lockType)
}

func (w *shareWorld) decide(t *testing.T, h *Handler, status string) int {
	t.Helper()
	body, err := json.Marshal(map[string]string{"status": status})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ReviewShare(rec, w.request(w.moderator, "moderator", http.MethodPatch, "/api/v1/groups/shares", body,
		map[string]string{"id": uuid.UUID(w.group.Bytes).String(), "transcriptID": uuid.UUID(w.transcript.Bytes).String()}))
	return rec.Result().StatusCode
}

func (w *shareWorld) unshare(t *testing.T, h *Handler) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.UnshareTranscript(rec, w.request(w.owner, w.ownerName, http.MethodDelete, "/api/v1/transcripts/share", nil,
		map[string]string{"id": uuid.UUID(w.transcript.Bytes).String(), "groupID": uuid.UUID(w.group.Bytes).String()}))
	return rec.Result().StatusCode
}

func (w *shareWorld) remove(t *testing.T, h *Handler) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.RemoveGroupTranscript(rec, w.request(w.moderator, "moderator", http.MethodDelete, "/api/v1/groups/transcripts", nil,
		map[string]string{"id": uuid.UUID(w.group.Bytes).String(), "transcriptID": uuid.UUID(w.transcript.Bytes).String()}))
	return rec.Result().StatusCode
}

func (w *shareWorld) leaveCollective(t *testing.T, h *Handler) int {
	t.Helper()
	rec := httptest.NewRecorder()
	h.RemoveGroupMember(rec, w.request(w.owner, w.ownerName, http.MethodDelete, "/api/v1/groups/members?retract=true", nil,
		map[string]string{"id": uuid.UUID(w.group.Bytes).String(), "userID": uuid.UUID(w.owner.Bytes).String()}))
	return rec.Result().StatusCode
}

// rewriteTerminalAttempt edits decided history in place, which the database
// must refuse whatever route reaches it.
func (w *shareWorld) rewriteTerminalAttempt(t *testing.T, ctx context.Context, index int, step shareAttemptStep) {
	t.Helper()
	if step.Expect != "refused" {
		t.Fatalf("step %d rewrites decided history and can only be expected to be refused", index)
	}
	_, err := w.pool.Exec(ctx, `
		UPDATE transcript_share_attempts SET status = 'rejected'
		WHERE transcript_id = $1 AND group_id = $2
	`, w.transcript, w.group)
	if err == nil {
		t.Fatalf("step %d edited a decided attempt in place; decided history must be immutable, and a changed decision "+
			"must be a new attempt", index)
	}
	if !strings.Contains(err.Error(), "cannot be modified") {
		t.Fatalf("step %d was refused by %v, which is not the immutability guard; the refusal must say what to do instead", index, err)
	}
}

func (w *shareWorld) assertAttempts(t *testing.T, ctx context.Context, testCase shareAttemptCase) {
	t.Helper()
	rows, err := w.pool.Query(ctx, `
		SELECT status FROM transcript_share_attempts
		WHERE transcript_id = $1 AND group_id = $2 ORDER BY event_num
	`, w.transcript, w.group)
	if err != nil {
		t.Fatalf("read the attempt sequence: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatal(err)
		}
		got = append(got, status)
	}
	if strings.Join(got, ",") != strings.Join(testCase.Attempts, ",") {
		t.Fatalf("attempt sequence = %v, want %v (%s)", got, testCase.Attempts, testCase.Why)
	}
}

func (w *shareWorld) assertDerivedRow(t *testing.T, ctx context.Context, testCase shareAttemptCase) {
	t.Helper()
	var derived string
	err := w.pool.QueryRow(ctx, `
		SELECT status FROM transcript_shares WHERE transcript_id = $1 AND group_id = $2
	`, w.transcript, w.group).Scan(&derived)
	if testCase.Derived == "absent" {
		if err == nil {
			t.Fatalf("the current-state row survives as %q after the contribution was withdrawn or removed; it must be "+
				"gone, because the shipped row carries only pending, approved and rejected and a withdrawn contribution "+
				"is not a share any more", derived)
		}
		return
	}
	if err != nil {
		t.Fatalf("read the derived current-state row: %v", err)
	}
	if derived != testCase.Derived {
		t.Fatalf("current-state row = %q, want %q; the derivation must follow the latest attempt", derived, testCase.Derived)
	}
}

// assertCounts checks the four counts the fixture declares. They are asserted
// one by one rather than from a table: these are the DEFINITIONS of the four
// counters, not a corpus of cases - the cases live in share-attempts.yaml, and
// each row there supplies the expected values used here.
func (w *shareWorld) assertCounts(t *testing.T, ctx context.Context, testCase shareAttemptCase) {
	t.Helper()
	// A transcript is either in a collective or not, so the two live counters
	// count DISTINCT TRANSCRIPTS.
	w.assertCount(t, ctx, "approved transcripts",
		`SELECT count(DISTINCT transcript_id) FROM transcript_shares WHERE group_id=$1 AND status='approved'`,
		testCase.ApprovedTranscripts, testCase.Why)
	w.assertCount(t, ctx, "pending transcripts",
		`SELECT count(DISTINCT transcript_id) FROM transcript_shares WHERE group_id=$1 AND status='pending'`,
		testCase.PendingTranscripts, testCase.Why)
	// Attempts are counted as ATTEMPTS: three rejections of one transcript are
	// three rejections, and two accepted attempts of one transcript are still
	// one contribution. Carrying both makes that divergence visible.
	w.assertCount(t, ctx, "approved attempts",
		`SELECT count(*) FROM transcript_share_attempts WHERE group_id=$1 AND status='approved'`,
		testCase.ApprovedAttempts, testCase.Why)
	w.assertCount(t, ctx, "rejected attempts",
		`SELECT count(*) FROM transcript_share_attempts WHERE group_id=$1 AND status='rejected'`,
		testCase.RejectedAttempts, testCase.Why)
}

func (w *shareWorld) assertCount(t *testing.T, ctx context.Context, label, query string, want int, why string) {
	t.Helper()
	var got int
	if err := w.pool.QueryRow(ctx, query, w.group).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", label, err)
	}
	if got != want {
		t.Errorf("%s = %d, want %d (%s)", label, got, want, why)
	}
}

func shareInsertUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, username string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id) VALUES ($1, $2, $3) RETURNING id
	`, int64(uuid.New().ID()), username, username).Scan(&id); err != nil {
		t.Fatalf("insert %s: %v", username, err)
	}
	return id
}

func shareAddMember(t *testing.T, ctx context.Context, pool *pgxpool.Pool, group, user pgtype.UUID, role string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, $3)
	`, group, user, role); err != nil {
		t.Fatalf("add %s: %v", role, err)
	}
}

func shareInsertTranscript(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner pgtype.UUID, localID string) pgtype.UUID {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatal(err)
	}
	id := toPgUUID(uuid.New())
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key,
		                         blob_size_bytes, schema_version, content_hash, wrapped_data_key, encryption_algorithm,
		                         key_version, project_hash)
		VALUES ($1,$2,$3,$4,'shared','claude-code',$5,$6,$7,'0.1.0',$8,$9,'aes-256-gcm-random-nonce-v1',1,$10)
		RETURNING id
	`, id, owner, localID, "t-"+localID, "m-"+localID, "blob/"+localID, int64(len(localID)),
		hex.EncodeToString(sha256.New().Sum([]byte(localID))[:16]), []byte("fixture-wrapped-data-key"),
		fixtureProjectHash(localID)).Scan(&id); err != nil {
		t.Fatalf("insert transcript %s: %v", localID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

// fixtureProjectHash produces the project identity every transcript is required
// to carry. It is derived from the fixture's own seed so two fixtures of the
// same project share one identity and different fixtures do not collide.
func fixtureProjectHash(seed string) string {
	sum := sha256.Sum256([]byte("project:" + seed))
	return hex.EncodeToString(sum[:6])
}

// assertProjectionMatchesLedger asserts that transcript_shares is exactly a
// latest-event fold over the whole of transcript_share_attempts. The comparison
// runs in the database, against the same definition the maintenance check and
// the rebuild use, so a green result here means those agree too.
func assertProjectionMatchesLedger(t *testing.T, ctx context.Context, pool *pgxpool.Pool, after string) {
	t.Helper()
	var drift int64
	if err := pool.QueryRow(ctx, `SELECT check_transcript_shares_drift()`).Scan(&drift); err != nil {
		t.Fatalf("evaluate the projection-versus-ledger check after %s: %v", after, err)
	}
	if drift == 0 {
		return
	}
	var problem, stored, expected string
	_ = pool.QueryRow(ctx, `SELECT problem, COALESCE(stored_status,'<none>'), COALESCE(expected_status,'<none>')
	                        FROM transcript_share_drift LIMIT 1`).Scan(&problem, &stored, &expected)
	t.Fatalf("after %s the derived projection disagrees with the ledger in %d row(s); first is %s (stored %s, expected %s). "+
		"Every transition must leave the projection equal to a latest-event fold over the ledger.", after, drift, problem, stored, expected)
}
