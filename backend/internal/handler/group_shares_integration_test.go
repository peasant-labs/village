//go:build integration

package handler

// Contributing a whole project to a collective, and the read behind the
// contribute surface, driven through the REAL HTTP handlers against a REAL
// PostgreSQL.
//
// It has to be a real database on three counts. The derived current-state share
// row is written by a trigger, so a Go test computing its own expected values
// would pass with no trigger installed at all. The whole-batch rollback is a
// property of one PostgreSQL transaction, and a mocked Querier cannot have one.
// And the mid-transaction conflict this route must survive is produced by a
// partial unique index, by a second connection, while the first is holding an
// advisory lock.

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

//go:embed testdata/groups-batch-share.yaml
var groupsBatchShareYAML []byte

//go:embed testdata/groups-contributable.yaml
var groupsContributableYAML []byte

// ---------------------------------------------------------------------------
// Fixture shapes
// ---------------------------------------------------------------------------

type batchShareTranscript struct {
	Key        string `yaml:"key"`
	Project    string `yaml:"project"`
	Visibility string `yaml:"visibility"`
	Owner      string `yaml:"owner"`
	Pre        string `yaml:"pre"`
}

type batchShareRequestSpec struct {
	Project    string   `yaml:"project"`
	ProjectRaw string   `yaml:"project_raw"`
	IDs        []string `yaml:"ids"`
	Confirm    bool     `yaml:"confirm"`
	RawBody    string   `yaml:"raw_body"`
}

type batchShareCase struct {
	Name                    string                 `yaml:"name"`
	Why                     string                 `yaml:"why"`
	Acceptance              string                 `yaml:"acceptance"`
	Member                  *bool                  `yaml:"member"`
	Mechanism               string                 `yaml:"mechanism"`
	Transcripts             []batchShareTranscript `yaml:"transcripts"`
	Request                 batchShareRequestSpec  `yaml:"request"`
	ExpectStatus            int                    `yaml:"expect_status"`
	ExpectShared            []string               `yaml:"expect_shared"`
	ExpectSharedStatus      string                 `yaml:"expect_shared_status"`
	ExpectAlreadyShared     []string               `yaml:"expect_already_shared"`
	ExpectMessageNames      []string               `yaml:"expect_message_names"`
	ExpectMessageContains   []string               `yaml:"expect_message_contains"`
	ExpectAttempts          map[string]int         `yaml:"expect_attempts"`
	ExpectDerived           map[string]string      `yaml:"expect_derived"`
	ExpectVisibility        map[string]string      `yaml:"expect_visibility"`
	ExpectRetryShared       []string               `yaml:"expect_retry_shared"`
	ExpectRetryAlreadyShare []string               `yaml:"expect_retry_already_shared"`
}

// requiredGroupsBatchShareCases names the cases that must exist. Each is here
// because losing it hides a specific failure: the two selection shapes, the
// three ways a selection can name something the person may not contribute, the
// consent answer in both directions, the two already-live shapes, the two
// acceptance modes that decide the opening state, the membership gate, the three
// malformed requests, and the mid-transaction conflict that is the only case
// able to fail if the single share path's continue-on-conflict pattern is copied
// into the batch transaction, and the event-ordering conflict that is the only
// row able to fail if the two conflict kinds are collapsed into one answer.
var requiredGroupsBatchShareCases = []string{
	"whole_project_share",
	"explicit_ids_share",
	"mixed_project_id_refused",
	"foreign_owner_id_refused",
	"private_without_confirm_refused",
	"private_with_confirm_flips_visibility",
	"partial_reshare_records_attempt_only",
	"all_already_shared_409",
	"curated_group_pending",
	"verified_only_without_org_refused",
	"non_member_403",
	"bad_project_hash_400",
	"unknown_json_field_400",
	"duplicate_ids_deduped",
	"mid_tx_conflict_rolls_back_then_retry_succeeds",
	"event_num_conflict_rolls_back_then_retry_succeeds",
}

func loadBatchShareCases(t *testing.T) []batchShareCase {
	t.Helper()
	cases, err := decodeFixtureRows[batchShareCase](groupsBatchShareYAML)
	if err != nil {
		t.Fatalf("load the whole-project contribution corpus: %v", err)
	}
	present := map[string]bool{}
	for _, testCase := range cases {
		if testCase.Name == "" {
			t.Fatalf("a contribution case has no name; every case is addressed by name")
		}
		if present[testCase.Name] {
			t.Fatalf("the contribution corpus repeats case %q", testCase.Name)
		}
		present[testCase.Name] = true
		if testCase.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", testCase.Name)
		}
		if testCase.ExpectStatus == 0 {
			t.Fatalf("case %q declares no expect_status", testCase.Name)
		}
		if len(testCase.Transcripts) == 0 {
			t.Fatalf("case %q builds no transcript, so it asks the route nothing", testCase.Name)
		}
		if len(testCase.ExpectAttempts) == 0 {
			t.Fatalf("case %q declares no expect_attempts; the ledger is what says whether anything was written, and a "+
				"case that does not look at it cannot tell a refusal from a silent partial write", testCase.Name)
		}
	}
	for _, required := range requiredGroupsBatchShareCases {
		if !present[required] {
			t.Fatalf("the whole-project contribution corpus no longer contains %q. That case exists because losing it "+
				"hides a real failure; restore it rather than deleting it from this manifest.", required)
		}
	}
	return cases
}

type contributableTranscriptSpec struct {
	Key     string `yaml:"key"`
	Project string `yaml:"project"`
	Parent  string `yaml:"parent"`
	Pre     string `yaml:"pre"`
}

type contributableCase struct {
	Name                string                        `yaml:"name"`
	Why                 string                        `yaml:"why"`
	Member              *bool                         `yaml:"member"`
	Limit               int                           `yaml:"limit"`
	Transcripts         []contributableTranscriptSpec `yaml:"transcripts"`
	ExpectStatus        int                           `yaml:"expect_status"`
	ExpectRows          []string                      `yaml:"expect_rows"`
	ExpectAlreadyShared []string                      `yaml:"expect_already_shared"`
	ExpectParentOf      map[string]string             `yaml:"expect_parent_of"`
}

// requiredGroupsContributableCases names the cases that must exist: the
// server-computed already-contributed answer, the child rows a narrowed query
// would silently drop, the membership gate, and the bound on an un-paginated
// listing.
var requiredGroupsContributableCases = []string{
	"already_shared_flag_set",
	"child_rows_included",
	"non_member_403",
	"over_limit_413",
}

func loadContributableCases(t *testing.T) []contributableCase {
	t.Helper()
	cases, err := decodeFixtureRows[contributableCase](groupsContributableYAML)
	if err != nil {
		t.Fatalf("load the contribute-listing corpus: %v", err)
	}
	present := map[string]bool{}
	for _, testCase := range cases {
		if testCase.Name == "" {
			t.Fatalf("a contribute-listing case has no name; every case is addressed by name")
		}
		if present[testCase.Name] {
			t.Fatalf("the contribute-listing corpus repeats case %q", testCase.Name)
		}
		present[testCase.Name] = true
		if testCase.Why == "" {
			t.Fatalf("case %q states no reason it exists; a case nobody can justify cannot be maintained", testCase.Name)
		}
		if testCase.ExpectStatus == 0 {
			t.Fatalf("case %q declares no expect_status", testCase.Name)
		}
		if len(testCase.Transcripts) == 0 {
			t.Fatalf("case %q builds no transcript, so it asks the route nothing", testCase.Name)
		}
	}
	for _, required := range requiredGroupsContributableCases {
		if !present[required] {
			t.Fatalf("the contribute-listing corpus no longer contains %q. That case exists because losing it hides a "+
				"real failure; restore it rather than deleting it from this manifest.", required)
		}
	}
	return cases
}

// ---------------------------------------------------------------------------
// The world
// ---------------------------------------------------------------------------

// contributeWorld is one member, one stranger who owns transcripts of their own,
// one collective, and the transcripts named by a case.
type contributeWorld struct {
	pool        *pgxpool.Pool
	caseName    string
	suffix      string
	member      pgtype.UUID
	memberName  string
	moderator   pgtype.UUID
	other       pgtype.UUID
	group       pgtype.UUID
	transcripts map[string]pgtype.UUID
	localIDs    map[string]string
	hashes      map[string]string
}

func newContributeWorld(t *testing.T, ctx context.Context, pool *pgxpool.Pool, caseName, acceptance string, member bool) *contributeWorld {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	w := &contributeWorld{
		pool:        pool,
		caseName:    caseName,
		suffix:      suffix,
		memberName:  "contrib-owner-" + suffix,
		transcripts: map[string]pgtype.UUID{},
		localIDs:    map[string]string{},
		hashes:      map[string]string{},
	}
	w.member = shareInsertUser(t, ctx, pool, w.memberName)
	w.moderator = shareInsertUser(t, ctx, pool, "contrib-mod-"+suffix)
	w.other = shareInsertUser(t, ctx, pool, "contrib-other-"+suffix)
	if acceptance == "" {
		acceptance = "open"
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO groups (name, created_by, acceptance_mode) VALUES ($1, $2, $3) RETURNING id
	`, "contribute-"+suffix, w.moderator, acceptance).Scan(&w.group); err != nil {
		t.Fatalf("create the collective: %v", err)
	}
	shareAddMember(t, ctx, pool, w.group, w.moderator, "owner")
	if member {
		shareAddMember(t, ctx, pool, w.group, w.member, "member")
	}
	return w
}

func (w *contributeWorld) cleanup(t *testing.T, ctx context.Context) {
	t.Helper()
	if _, err := w.pool.Exec(ctx, "DELETE FROM groups WHERE id = $1", w.group); err != nil {
		t.Errorf("cleanup collective: %v", err)
	}
	cleanupOwners(t, ctx, w.pool, w.member, w.moderator, w.other)
}

func (w *contributeWorld) groupID() string { return uuid.UUID(w.group.Bytes).String() }

// projectHash gives one 64-hex project identity per (case, project key). It is
// derived from the case name so two cases running against the same database
// never share a project, and from the project key so two transcripts of one
// case's project do share one.
func (w *contributeWorld) projectHash(projectKey string) string {
	if hash, ok := w.hashes[projectKey]; ok {
		return hash
	}
	sum := sha256.Sum256([]byte("contribute:" + w.suffix + ":" + projectKey))
	hash := hex.EncodeToString(sum[:])
	w.hashes[projectKey] = hash
	return hash
}

// insertTranscript writes one transcript as the system actor, which is what the
// fail-closed publish trigger requires of a fixture.
func (w *contributeWorld) insertTranscript(t *testing.T, ctx context.Context, ordinal int, key, projectKey, visibility, ownerKey, parentKey string) {
	t.Helper()
	if visibility == "" {
		visibility = "shared"
	}
	owner := w.member
	if ownerKey == "other" {
		owner = w.other
	}
	var parent pgtype.Text
	if parentKey != "" {
		parentLocal, ok := w.localIDs[parentKey]
		if !ok {
			t.Fatalf("case %s: transcript %q names parent %q, which is not declared before it", w.caseName, key, parentKey)
		}
		parent = pgtype.Text{String: parentLocal, Valid: true}
	}
	localID := "contrib-" + w.suffix + "-" + key

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transcript fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatalf("declare the system actor for the transcript fixture: %v", err)
	}
	id := toPgUUID(uuid.New())
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key,
		                         blob_size_bytes, schema_version, content_hash, wrapped_data_key, encryption_algorithm,
		                         key_version, project_hash, parent_session_id, git_branch, session_origin,
		                         published_at)
		VALUES ($1,$2,$3,$4,$5,'claude-code',$6,$7,$8,'0.1.0',$9,$10,'aes-256-gcm-random-nonce-v1',1,$11,$12,'main','user',
		        now() - ($13 * interval '1 minute'))
	`, id, owner, localID, "t-"+key, visibility, "m-"+key, "blob/"+localID, int64(len(localID)),
		hex.EncodeToString(sha256.New().Sum([]byte(localID))[:16]), []byte("fixture-wrapped-data-key"),
		w.projectHash(projectKey), parent, ordinal); err != nil {
		t.Fatalf("insert transcript %s: %v", key, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit transcript fixture %s: %v", key, err)
	}
	w.transcripts[key] = id
	w.localIDs[key] = localID
}

// submitSingle offers one transcript through the SHIPPED single-share handler,
// so a case's pre-existing submission is built by the real write path rather
// than written behind it.
func (w *contributeWorld) submitSingle(t *testing.T, h *Handler, key string) {
	t.Helper()
	body, err := json.Marshal(map[string][]string{"group_ids": {w.groupID()}})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ShareTranscript(rec, w.request(http.MethodPost, "/api/v1/transcripts/share", body,
		map[string]string{"id": uuid.UUID(w.transcripts[key].Bytes).String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("preparing case %s: the single share of %q answered %d, want 200 (body: %s)",
			w.caseName, key, rec.Code, rec.Body.String())
	}
}

func (w *contributeWorld) request(method, target string, body []byte, params map[string]string) *http.Request {
	return shareRequestAs(w.member, w.memberName, method, target, body, params)
}

func shareRequestAs(actor pgtype.UUID, username, method, target string, body []byte, params map[string]string) *http.Request {
	route := chi.NewRouteContext()
	for key, value := range params {
		route.URLParams.Add(key, value)
	}
	reqCtx := context.WithValue(context.Background(), UserContextKey, &AuthUser{ID: uuid.UUID(actor.Bytes), Username: username})
	reqCtx = context.WithValue(reqCtx, chi.RouteCtxKey, route)
	return httptest.NewRequest(method, target, bytes.NewReader(body)).WithContext(reqCtx)
}

func (w *contributeWorld) attemptCount(t *testing.T, ctx context.Context, key string) int {
	t.Helper()
	var count int
	if err := w.pool.QueryRow(ctx,
		"SELECT count(*)::int FROM transcript_share_attempts WHERE transcript_id = $1 AND group_id = $2",
		w.transcripts[key], w.group).Scan(&count); err != nil {
		t.Fatalf("count the attempts of %q: %v", key, err)
	}
	return count
}

func (w *contributeWorld) derivedStatus(t *testing.T, ctx context.Context, key string) string {
	t.Helper()
	var status string
	err := w.pool.QueryRow(ctx,
		"SELECT status FROM transcript_shares WHERE transcript_id = $1 AND group_id = $2",
		w.transcripts[key], w.group).Scan(&status)
	if err != nil {
		return "<none>"
	}
	return status
}

func (w *contributeWorld) visibility(t *testing.T, ctx context.Context, key string) string {
	t.Helper()
	var visibility string
	if err := w.pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id = $1", w.transcripts[key]).Scan(&visibility); err != nil {
		t.Fatalf("read the visibility of %q: %v", key, err)
	}
	return visibility
}

// ---------------------------------------------------------------------------
// The batch route
// ---------------------------------------------------------------------------

func TestBatchShareProject(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()
	h := New(&config.Config{}, pool, nil)

	for _, testCase := range loadBatchShareCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			member := testCase.Member == nil || *testCase.Member
			world := newContributeWorld(t, ctx, pool, testCase.Name, testCase.Acceptance, member)
			defer world.cleanup(t, ctx)

			// published_at descends with the declaration order, and the
			// candidate query orders by published_at DESC, so a case's
			// declaration order IS the order the batch writes in. Without that
			// the ordering is decided by microsecond clock ties and a case that
			// depends on which transcript is written first would pass or fail by
			// luck.
			for ordinal, transcript := range testCase.Transcripts {
				world.insertTranscript(t, ctx, ordinal, transcript.Key, transcript.Project, transcript.Visibility, transcript.Owner, "")
			}
			for _, transcript := range testCase.Transcripts {
				if transcript.Pre == "live" {
					world.submitSingle(t, h, transcript.Key)
				}
			}

			switch testCase.Mechanism {
			case "mid_tx_conflict":
				runMidTransactionConflictCase(t, ctx, h, world, testCase)
				return
			case "event_num_conflict":
				runEventOrderingConflictCase(t, ctx, h, world, testCase)
				return
			}

			rec := world.postBatch(h, world.batchBody(t, testCase))
			world.assertBatchOutcome(t, ctx, rec, testCase, testCase.ExpectStatus,
				testCase.ExpectShared, testCase.ExpectAlreadyShared)
			assertProjectionMatchesLedger(t, ctx, pool, "case "+testCase.Name)
		})
	}
}

func (w *contributeWorld) batchBody(t *testing.T, testCase batchShareCase) []byte {
	t.Helper()
	if testCase.Request.RawBody != "" {
		return []byte(strings.ReplaceAll(testCase.Request.RawBody, "PROJECT_HASH_P", w.projectHash("p")))
	}
	payload := map[string]any{"visibility_confirmed": testCase.Request.Confirm}
	if testCase.Request.ProjectRaw != "" {
		payload["project_hash"] = testCase.Request.ProjectRaw
	} else {
		payload["project_hash"] = w.projectHash(testCase.Request.Project)
	}
	if len(testCase.Request.IDs) > 0 {
		ids := make([]string, 0, len(testCase.Request.IDs))
		for _, key := range testCase.Request.IDs {
			ids = append(ids, uuid.UUID(w.transcripts[key].Bytes).String())
		}
		payload["transcript_ids"] = ids
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func (w *contributeWorld) postBatch(h *Handler, body []byte) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.BatchShareProject(rec, w.request(http.MethodPost, "/api/v1/groups/"+w.groupID()+"/shares",
		body, map[string]string{"id": w.groupID()}))
	return rec
}

// assertBatchOutcome checks the answer and then the database: what the ledger
// holds, what the derivation produced, and what each transcript's visibility is.
// The ledger assertion runs for every case, including the refusals, because
// "nothing was written" is the promise and only the ledger can show it.
func (w *contributeWorld) assertBatchOutcome(t *testing.T, ctx context.Context, rec *httptest.ResponseRecorder,
	testCase batchShareCase, wantStatus int, wantShared, wantAlreadyShared []string) {
	t.Helper()

	if rec.Code != wantStatus {
		t.Fatalf("the contribution answered %d, want %d (%s) (body: %s)", rec.Code, wantStatus, testCase.Why, rec.Body.String())
	}

	if wantStatus == http.StatusOK {
		var got batchShareResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode the contribution receipt: %v (body: %s)", err, rec.Body.String())
		}
		if got.ProjectHash != w.projectHash(testCase.Request.Project) {
			t.Errorf("the receipt names project %q, want %q; the person routes and groups by the project hash",
				got.ProjectHash, w.projectHash(testCase.Request.Project))
		}
		gotShared := make([]string, 0, len(got.Shared))
		for _, entry := range got.Shared {
			gotShared = append(gotShared, entry.TranscriptID)
			if testCase.ExpectSharedStatus != "" && string(entry.Status) != testCase.ExpectSharedStatus {
				t.Errorf("transcript %s opened in state %q, want %q; the collective's acceptance mode decides the "+
					"opening state, and a batch must not bypass moderation the single share honours (%s)",
					entry.TranscriptID, entry.Status, testCase.ExpectSharedStatus, testCase.Why)
			}
		}
		w.assertIDSet(t, "shared", gotShared, wantShared, testCase.Why)
		w.assertIDSet(t, "already_shared", got.AlreadyShared, wantAlreadyShared, testCase.Why)
	} else {
		var body struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode the refusal: %v (body: %s)", err, rec.Body.String())
		}
		if strings.TrimSpace(body.Error) == "" {
			t.Fatalf("the refusal carries no message; a person cannot act on a bare status code (%s)", testCase.Why)
		}
		for _, phrase := range testCase.ExpectMessageContains {
			if !strings.Contains(body.Error, phrase) {
				t.Errorf("the refusal does not say %q (%s). The two conflict kinds need OPPOSITE advice - one is "+
					"cleared by withdrawing the live submission, the other by simply asking again - so answering both "+
					"with one message tells half the callers the wrong thing. Message: %s", phrase, testCase.Why, body.Error)
			}
		}
		for _, key := range testCase.ExpectMessageNames {
			needle := key
			if id, ok := w.transcripts[key]; ok {
				needle = uuid.UUID(id.Bytes).String()
			}
			if !strings.Contains(body.Error, needle) {
				t.Errorf("the refusal does not name %q (%s). A refusal that does not say WHICH thing was wrong cannot "+
					"be acted on. Message: %s", needle, testCase.Why, body.Error)
			}
		}
	}

	for key, want := range testCase.ExpectAttempts {
		if got := w.attemptCount(t, ctx, key); got != want {
			t.Errorf("transcript %q has %d attempt row(s) in this collective, want %d (%s). The ledger is what says "+
				"whether anything was written; a refusal that left a row behind broke the promise that nothing was.",
				key, got, want, testCase.Why)
		}
	}
	for key, want := range testCase.ExpectDerived {
		if got := w.derivedStatus(t, ctx, key); got != want {
			t.Errorf("the derived share row for %q is %q, want %q (%s). It is written by a trigger from the ledger, so "+
				"a disagreement here means the ledger and the projection have parted.", key, got, want, testCase.Why)
		}
	}
	for key, want := range testCase.ExpectVisibility {
		if got := w.visibility(t, ctx, key); got != want {
			t.Errorf("transcript %q is %q, want %q (%s). Contributing a private transcript makes it visible to the "+
				"collective, so the submission and the visibility must move together or not at all.", key, got, want, testCase.Why)
		}
	}
}

func (w *contributeWorld) assertIDSet(t *testing.T, label string, got []string, wantKeys []string, why string) {
	t.Helper()
	want := make([]string, 0, len(wantKeys))
	for _, key := range wantKeys {
		want = append(want, uuid.UUID(w.transcripts[key].Bytes).String())
	}
	gotSet := map[string]bool{}
	for _, id := range got {
		if gotSet[id] {
			t.Errorf("%s names %s twice; one transcript is contributed once (%s)", label, id, why)
		}
		gotSet[id] = true
	}
	if len(got) != len(want) {
		t.Fatalf("%s holds %d transcript(s), want %d (%s); got %v, want %v", label, len(got), len(want), why, got, want)
	}
	for i, id := range want {
		if !gotSet[id] {
			t.Errorf("%s omits transcript %q (%s); got %v", label, wantKeys[i], why, got)
		}
	}
}

// runMidTransactionConflictCase reproduces the one race this route must survive.
//
// The batch is started in a goroutine while this test holds the publish advisory
// lock for one of its transcripts on a connection of its own. The batch
// therefore completes every check - it sees no live attempt for that transcript,
// because there is none yet - and then blocks waiting for the lock. The test
// then opens a live attempt for that transcript through its own connection,
// exactly as a concurrent single share would, and releases the lock. The batch
// resumes, the partial unique index refuses its second open attempt - which is
// why the collective is curated: the index covers submissions awaiting review,
// so 'pending' is the one state in which the database itself can refuse - and the
// WHOLE batch must roll back: the other transcript must have no row at all.
// A retry then reports the conflicting transcript as already contributed and
// submits the rest.
func runMidTransactionConflictCase(t *testing.T, ctx context.Context, h *Handler, world *contributeWorld, testCase batchShareCase) {
	lockCtx, cancelLock := context.WithTimeout(ctx, 30*time.Second)
	defer cancelLock()
	lockConn, err := world.pool.Acquire(lockCtx)
	if err != nil {
		t.Fatalf("acquire the connection that holds the publish lock: %v", err)
	}
	lockReleased := false
	defer func() {
		if !lockReleased {
			_, _ = lockConn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1, 0))", world.blockedLockKey())
		}
		lockConn.Release()
	}()
	if _, err := lockConn.Exec(lockCtx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", world.blockedLockKey()); err != nil {
		t.Fatalf("hold the publish lock for the conflicting transcript: %v", err)
	}

	body := world.batchBody(t, testCase)
	var wg sync.WaitGroup
	var rec *httptest.ResponseRecorder
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec = world.postBatch(h, body)
	}()

	// Give the batch time to pass its checks and block on the lock. If it has
	// not blocked yet the case still holds: the conflicting attempt is inserted
	// before the batch's own insert either way.
	waitForPublishLockWaiter(t, ctx, world)

	if _, err := world.pool.Exec(ctx, `
		INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
		VALUES ($1, $2, 1, 'pending')`, world.transcripts["b"], world.group); err != nil {
		t.Fatalf("open the competing submission for the conflicting transcript: %v", err)
	}

	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", world.blockedLockKey()); err != nil {
		t.Fatalf("release the publish lock: %v", err)
	}
	lockReleased = true
	wg.Wait()

	world.assertBatchOutcome(t, ctx, rec, testCase, testCase.ExpectStatus, nil, nil)
	assertProjectionMatchesLedger(t, ctx, world.pool, "the refused batch of case "+testCase.Name)

	retry := world.postBatch(h, body)
	retryCase := testCase
	retryCase.ExpectAttempts = map[string]int{"a": 1, "b": 1}
	retryCase.ExpectDerived = map[string]string{"a": testCase.ExpectSharedStatus, "b": testCase.ExpectSharedStatus}
	retryCase.ExpectMessageNames = nil
	world.assertBatchOutcome(t, ctx, retry, retryCase, http.StatusOK,
		testCase.ExpectRetryShared, testCase.ExpectRetryAlreadyShare)
	assertProjectionMatchesLedger(t, ctx, world.pool, "the retried batch of case "+testCase.Name)
}

// runEventOrderingConflictCase reproduces the OTHER race, the one that used to
// answer an unexplained 500.
//
// The batch is gated on the publish advisory lock exactly as in the case above.
// While it waits, this test opens a transaction of its own and appends an
// ACCEPTED event for the same transcript at ordinal 1 - and does NOT commit it.
// The batch then resumes, reads the pair's highest ordinal (it cannot see the
// uncommitted event, so it computes ordinal 1 too), and blocks inside the unique
// index waiting on that transaction. Committing it turns the batch's insert into
// a UNIQUE (transcript_id, group_id, event_num) violation - deterministically,
// because the block is observed before the commit rather than assumed.
//
// The competing event is ACCEPTED rather than awaiting review on purpose: the
// open-attempt index covers only submissions awaiting review, so it cannot fire
// here, and the answer this case sees can only have come from the ordering
// branch.
func runEventOrderingConflictCase(t *testing.T, ctx context.Context, h *Handler, world *contributeWorld, testCase batchShareCase) {
	lockCtx, cancelLock := context.WithTimeout(ctx, 30*time.Second)
	defer cancelLock()
	lockConn, err := world.pool.Acquire(lockCtx)
	if err != nil {
		t.Fatalf("acquire the connection that holds the publish lock: %v", err)
	}
	lockReleased := false
	defer func() {
		if !lockReleased {
			_, _ = lockConn.Exec(context.Background(), "SELECT pg_advisory_unlock(hashtextextended($1, 0))", world.blockedLockKey())
		}
		lockConn.Release()
	}()
	if _, err := lockConn.Exec(lockCtx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", world.blockedLockKey()); err != nil {
		t.Fatalf("hold the publish lock for the conflicting transcript: %v", err)
	}

	body := world.batchBody(t, testCase)
	var wg sync.WaitGroup
	var rec *httptest.ResponseRecorder
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec = world.postBatch(h, body)
	}()
	waitForPublishLockWaiter(t, ctx, world)

	competing, err := world.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the competing writer's transaction: %v", err)
	}
	competingCommitted := false
	defer func() {
		if !competingCommitted {
			_ = competing.Rollback(context.Background())
		}
	}()
	if _, err := competing.Exec(ctx, `
		INSERT INTO transcript_share_attempts (transcript_id, group_id, event_num, status)
		VALUES ($1, $2, 1, 'approved')`, world.transcripts["b"], world.group); err != nil {
		t.Fatalf("append the competing event: %v", err)
	}

	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", world.blockedLockKey()); err != nil {
		t.Fatalf("release the publish lock: %v", err)
	}
	lockReleased = true

	waitForBlockedWriter(t, ctx, world)
	if err := competing.Commit(ctx); err != nil {
		t.Fatalf("commit the competing event: %v", err)
	}
	competingCommitted = true
	wg.Wait()

	world.assertBatchOutcome(t, ctx, rec, testCase, testCase.ExpectStatus, nil, nil)
	assertProjectionMatchesLedger(t, ctx, world.pool, "the refused batch of case "+testCase.Name)

	retry := world.postBatch(h, body)
	retryCase := testCase
	retryCase.ExpectAttempts = map[string]int{"a": 1, "b": 1}
	retryCase.ExpectDerived = map[string]string{"a": testCase.ExpectSharedStatus, "b": testCase.ExpectSharedStatus}
	retryCase.ExpectMessageNames = nil
	retryCase.ExpectMessageContains = nil
	world.assertBatchOutcome(t, ctx, retry, retryCase, http.StatusOK,
		testCase.ExpectRetryShared, testCase.ExpectRetryAlreadyShare)
	assertProjectionMatchesLedger(t, ctx, world.pool, "the retried batch of case "+testCase.Name)
}

// waitForBlockedWriter blocks until PostgreSQL reports a session waiting on
// another transaction. That is the batch parked inside the unique index on the
// competing writer's uncommitted row, and observing it is what makes the
// conflict deterministic instead of a sleep that usually works.
func waitForBlockedWriter(t *testing.T, ctx context.Context, world *contributeWorld) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := world.pool.QueryRow(ctx, `
			SELECT count(*)::int FROM pg_locks
			WHERE locktype = 'transactionid' AND NOT granted`).Scan(&waiting); err != nil {
			t.Fatalf("look for a session blocked on the competing writer: %v", err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no session ever blocked on the competing writer, so the batch never contended for the event ordinal and "+
		"this case would prove nothing about an ordering conflict (case %s)", world.caseName)
}

// TestShareAttemptConstraintNamesMatchTheCatalog pins the two constraint names
// the conflict classifier keys on against the LIVE catalog. The driver's error
// carries the name and nothing else, so a rename - or a migration that recreates
// one of them under a different name - would silently turn every conflict back
// into an unexplained failure while every other test stayed green.
func TestShareAttemptConstraintNamesMatchTheCatalog(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	for _, name := range []string{openShareAttemptIndex, shareAttemptEventNumConstraint} {
		var present bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_indexes
				WHERE tablename = 'transcript_share_attempts' AND indexname = $1
			)`, name).Scan(&present); err != nil {
			t.Fatalf("look up %q in the catalog: %v", name, err)
		}
		if !present {
			t.Fatalf("the conflict classifier keys on the constraint name %q, and no such index exists on "+
				"transcript_share_attempts. PostgreSQL reports a unique violation by NAME, so a classifier pointed at a "+
				"name nothing carries answers every conflict as an unexplained failure.", name)
		}
	}
}

// blockedLockKey is the advisory-lock key of the transcript the conflict case
// blocks on. It is built by the SAME helper production uses, so a change to the
// key shape cannot leave this test holding a lock nothing contends for.
func (w *contributeWorld) blockedLockKey() string {
	return sessionPublishLockKey(w.member, w.localIDs["b"])
}

// waitForPublishLockWaiter blocks until PostgreSQL reports a session waiting on
// an advisory lock, so the competing submission is inserted while the batch is
// genuinely between its checks and its writes rather than before it started.
func waitForPublishLockWaiter(t *testing.T, ctx context.Context, world *contributeWorld) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := world.pool.QueryRow(ctx, `
			SELECT count(*)::int FROM pg_locks
			WHERE locktype = 'advisory' AND NOT granted`).Scan(&waiting); err != nil {
			t.Fatalf("look for a session waiting on the publish lock: %v", err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no session ever waited on the publish lock, so the batch never reached its write phase and this case "+
		"would prove nothing about a mid-transaction conflict (case %s)", world.caseName)
}

// ---------------------------------------------------------------------------
// The contribute listing
// ---------------------------------------------------------------------------

func TestListContributable(t *testing.T) {
	ctx := context.Background()
	pool := govTestPool(t)
	defer pool.Close()

	for _, testCase := range loadContributableCases(t) {
		t.Run(testCase.Name, func(t *testing.T) {
			h := New(&config.Config{}, pool, nil)
			if testCase.Limit > 0 {
				h.contributableRowLimit = testCase.Limit
			}
			member := testCase.Member == nil || *testCase.Member
			world := newContributeWorld(t, ctx, pool, testCase.Name, "open", member)
			defer world.cleanup(t, ctx)

			for ordinal, transcript := range testCase.Transcripts {
				world.insertTranscript(t, ctx, ordinal, transcript.Key, transcript.Project, "shared", "", transcript.Parent)
			}
			for _, transcript := range testCase.Transcripts {
				if transcript.Pre == "live" {
					world.submitSingle(t, h, transcript.Key)
				}
			}

			rec := httptest.NewRecorder()
			h.ListContributable(rec, world.request(http.MethodGet,
				"/api/v1/groups/"+world.groupID()+"/contributable", nil, map[string]string{"id": world.groupID()}))

			if rec.Code != testCase.ExpectStatus {
				t.Fatalf("the contribute listing answered %d, want %d (%s) (body: %s)",
					rec.Code, testCase.ExpectStatus, testCase.Why, rec.Body.String())
			}
			if testCase.ExpectStatus != http.StatusOK {
				var body struct {
					Error string `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || strings.TrimSpace(body.Error) == "" {
					t.Fatalf("the refusal carries no message; a person cannot act on a bare status code (%s)", testCase.Why)
				}
				return
			}

			var got contributableResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode the contribute listing: %v (body: %s)", err, rec.Body.String())
			}
			if got.GroupID != world.groupID() {
				t.Errorf("the listing names collective %q, want %q", got.GroupID, world.groupID())
			}
			world.assertContributableRows(t, got, testCase)
		})
	}
}

func (w *contributeWorld) assertContributableRows(t *testing.T, got contributableResponse, testCase contributableCase) {
	t.Helper()
	byID := map[string]contributableTranscript{}
	for _, row := range got.Transcripts {
		byID[row.ID] = row
	}
	if len(got.Transcripts) != len(testCase.ExpectRows) {
		t.Fatalf("the listing holds %d row(s), want %d (%s). A query narrowed to root sessions loses exactly the child "+
			"rows, which shortens this answer rather than emptying it.",
			len(got.Transcripts), len(testCase.ExpectRows), testCase.Why)
	}
	alreadyShared := map[string]bool{}
	for _, key := range testCase.ExpectAlreadyShared {
		alreadyShared[key] = true
	}
	for _, key := range testCase.ExpectRows {
		id := uuid.UUID(w.transcripts[key].Bytes).String()
		row, ok := byID[id]
		if !ok {
			t.Fatalf("the listing omits transcript %q (%s); a transcript the person owns and has not yet contributed "+
				"must be offerable", key, testCase.Why)
		}
		if row.AlreadyShared != alreadyShared[key] {
			t.Errorf("transcript %q reports already_shared=%v, want %v (%s). The answer comes from the attempt ledger; "+
				"a listing that read the derived row would disagree exactly for a withdrawn contribution.",
				key, row.AlreadyShared, alreadyShared[key], testCase.Why)
		}
		if row.LocalID != w.localIDs[key] {
			t.Errorf("transcript %q carries local_id %q, want %q; the surface groups child sessions by their parent's "+
				"local id", key, row.LocalID, w.localIDs[key])
		}
		if row.ProjectHash != w.projectHash("p") {
			t.Errorf("transcript %q carries project_hash %q, want %q; the project is identified by its hash",
				key, row.ProjectHash, w.projectHash("p"))
		}
		// These fixtures publish no project name and no git remote, so the one
		// name every surface renders is the privacy-safe label the resolver
		// synthesises. Asserting the tier proves the listing went through the
		// shared resolver rather than inventing a name of its own.
		if row.ProjectNameSource != "privacy" {
			t.Errorf("transcript %q resolved its project name from tier %q, want \"privacy\"; these fixtures carry no "+
				"name and no remote, so any other tier means the listing resolved something it was not given",
				key, row.ProjectNameSource)
		}
		if !strings.HasPrefix(row.ProjectDisplayName, "project-") {
			t.Errorf("transcript %q renders project name %q, want the privacy-safe label; every surface renders the "+
				"same one answer for a project", key, row.ProjectDisplayName)
		}
	}
	for childKey, parentKey := range testCase.ExpectParentOf {
		row := byID[uuid.UUID(w.transcripts[childKey].Bytes).String()]
		if row.ParentSessionID == nil || *row.ParentSessionID != w.localIDs[parentKey] {
			t.Errorf("transcript %q reports parent %v, want %q (%s). Without it the surface cannot place the child "+
				"under its parent and would show it as a root session.",
				childKey, row.ParentSessionID, w.localIDs[parentKey], testCase.Why)
		}
	}
}
