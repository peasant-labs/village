//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/collective-authority.yaml
var collectiveAuthorityYAML []byte

type collectiveAuthorityCase struct {
	Name            string   `yaml:"name"`
	Operation       string   `yaml:"operation"`
	Targets         []string `yaml:"targets"`
	Change          string   `yaml:"change"`
	WaitForPublish  bool     `yaml:"wait_for_publish"`
	StartVisibility string   `yaml:"start_visibility"`
	Status          int      `yaml:"status"`
	Attempts        int      `yaml:"attempts"`
	Visibility      string   `yaml:"visibility"`
	PreShare        bool     `yaml:"pre_share"`
}

func loadCollectiveAuthorityFixtures(t *testing.T) []collectiveAuthorityCase {
	t.Helper()
	cases, err := decodeFixtureRows[collectiveAuthorityCase](collectiveAuthorityYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"live-target-membership-revoked-no-audience-restore": false,
		"mixed-target-refusal-atomic":                        false,
		"private-no-target-no-flip":                          false,
		"policy-changed-before-share":                        false,
		"membership-revoked-during-share":                    false,
		"repeated-targets-one-attempt":                       false,
		"mixed-valid-invalid-target-no-writes":               false,
		"batch-policy-rechecked-after-publish-lock":          false,
		"batch-membership-rechecked-after-publish-lock":      false,
		"batch-curated-policy-change-needs-retry":            false,
		"approved-current-share-retains-public-visibility":   false,
		"reversed-intersecting-targets-no-deadlock":          false,
		"second-target-write-failure-rolls-back-first":       false,
		"owner-role-change-rechecked-before-policy-write":    false,
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("missing or repeated authority fixture name %q", c.Name)
		}
		seen[c.Name] = true
		if _, ok := required[c.Name]; ok {
			required[c.Name] = true
		}
		if c.Operation != "single" && c.Operation != "batch" && c.Operation != "concurrent" && c.Operation != "owner-update" {
			t.Fatalf("unknown operation %q", c.Operation)
		}
	}
	for name, present := range required {
		if !present {
			t.Fatalf("missing required authority case %q", name)
		}
	}
	return cases
}

// The subject is the actual transaction, SQL, audit triggers and mounted HTTP
// outcome. A unit mock cannot observe rollback or a policy change while a real
// publish lock is held. Existing disposable PostgreSQL is sufficient; no new
// service, copied authorization engine or timing-only race harness is involved.
func TestCollectiveFinalAuthority(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	h := New(&config.Config{}, pool, nil)
	for _, c := range loadCollectiveAuthorityFixtures(t) {
		t.Run(c.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			start := c.StartVisibility
			if start == "" {
				start = "private"
			}
			world := newShareWorldAt(t, ctx, pool, c.Name, "open", start)
			defer world.cleanup(t, context.Background())
			// Older ledger fixtures use short opaque project keys. This mounted
			// batch route requires the public 64-hex project-hash shape.
			if c.Operation == "batch" {
				project := strings.Repeat(fixtureProjectHash(world.localID), 6)[:64]
				if _, err := pool.Exec(ctx, "UPDATE transcripts SET project_hash=$2 WHERE id=$1", world.transcript, project); err != nil {
					t.Fatal(err)
				}
			}
			world.addSecondCollective(t, ctx, c.Name, "open")
			if c.PreShare {
				if got := world.submitTo(t, h, world.group); got != 200 {
					t.Fatalf("prepare live share: %d", got)
				}
				if err := h.inTxAs(ctx, world.owner, func(q Querier) error {
					private := dbVisibilityPrivate
					_, err := applyMetadataPatch(ctx, q, world.transcript, metadataPatch{Visibility: &private})
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if err := pool.QueryRow(ctx, "SELECT coalesce(max(seq),0) FROM transcript_governance_events_audit WHERE transcript_id=$1", world.transcript).Scan(&world.auditSeq); err != nil {
					t.Fatal(err)
				}
			}
			if c.Operation == "concurrent" {
				assertIntersectingCollectiveShares(t, ctx, h, world, c)
				return
			}
			if c.Operation == "owner-update" {
				assertOwnerAuthorityAfterRoleChange(t, ctx, h, world, c)
				return
			}
			owner := &AuthUser{ID: uuid.UUID(world.owner.Bytes), Username: world.ownerName}
			mod := &AuthUser{ID: uuid.UUID(world.moderator.Bytes), Username: "moderator"}
			router := chi.NewRouter()
			router.Post("/transcripts/{id}/share", h.ShareTranscript)
			router.Post("/groups/{id}/shares", h.BatchShareProject)
			router.Patch("/groups/{id}", h.UpdateGroup)
			router.Delete("/groups/{id}/members/{userID}", h.RemoveGroupMember)
			request := func(actor *AuthUser, method, path string, body []byte) *http.Request {
				r := httptest.NewRequest(method, path, bytes.NewReader(body))
				return r.WithContext(context.WithValue(ctx, UserContextKey, actor))
			}
			change := func() {
				var r *http.Request
				switch c.Change {
				case "":
					return
				case "reject-second-write":
					name := "authority_refusal_" + strings.ReplaceAll(uuid.UUID(world.transcript.Bytes).String(), "-", "")
					t.Cleanup(func() {
						if _, err := pool.Exec(context.Background(), "DROP TRIGGER IF EXISTS "+name+" ON transcript_share_attempts; DROP FUNCTION IF EXISTS "+name+"()"); err != nil {
							t.Error(err)
						}
					})
					if _, err := pool.Exec(ctx, "CREATE FUNCTION "+name+"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected second target refusal'; END $$"); err != nil {
						t.Fatal(err)
					}
					if _, err := pool.Exec(ctx, "CREATE TRIGGER "+name+" BEFORE INSERT ON transcript_share_attempts FOR EACH ROW WHEN (NEW.transcript_id='"+uuid.UUID(world.transcript.Bytes).String()+"'::uuid AND NEW.group_id='"+uuid.UUID(world.second.Bytes).String()+"'::uuid) EXECUTE FUNCTION "+name+"()"); err != nil {
						t.Fatal(err)
					}
					return
				case "verified-only", "curated":
					mode := "verified_only"
					if c.Change == "curated" {
						mode = "curated"
					}
					body, _ := json.Marshal(map[string]string{"acceptance_mode": mode})
					r = request(mod, http.MethodPatch, "/groups/"+uuid.UUID(world.group.Bytes).String(), body)
				case "remove-first-member", "remove-second-member":
					group := world.group
					if c.Change == "remove-second-member" {
						group = world.second
					}
					r = request(mod, http.MethodDelete, "/groups/"+uuid.UUID(group.Bytes).String()+"/members/"+owner.ID.String(), nil)
				default:
					t.Fatalf("unknown change %q", c.Change)
				}
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, r)
				if rec.Code != 200 {
					t.Fatalf("authority writer: %d %s", rec.Code, rec.Body.String())
				}
			}
			var body []byte
			path := "/transcripts/" + uuid.UUID(world.transcript.Bytes).String() + "/share"
			if c.Operation == "batch" {
				var project string
				if err := pool.QueryRow(ctx, "SELECT project_hash FROM transcripts WHERE id=$1", world.transcript).Scan(&project); err != nil {
					t.Fatal(err)
				}
				body, _ = json.Marshal(map[string]any{"project_hash": project, "visibility_confirmed": true})
				path = "/groups/" + uuid.UUID(world.group.Bytes).String() + "/shares"
			} else {
				targets := make([]string, 0, len(c.Targets))
				for _, target := range c.Targets {
					switch target {
					case "first":
						targets = append(targets, uuid.UUID(world.group.Bytes).String())
					case "second":
						targets = append(targets, uuid.UUID(world.second.Bytes).String())
					case "invalid":
						targets = append(targets, "not-a-uuid")
					default:
						t.Fatalf("unknown target %q", target)
					}
				}
				body, _ = json.Marshal(map[string][]string{"group_ids": targets})
			}
			rec := httptest.NewRecorder()
			if c.WaitForPublish {
				conn, err := pool.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				key := sessionPublishLockKey(world.owner, world.localID)
				if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1,0))", key); err != nil {
					conn.Release()
					t.Fatal(err)
				}
				done := make(chan struct{})
				go func() { defer close(done); router.ServeHTTP(rec, request(owner, http.MethodPost, path, body)) }()
				// Cleanup unlocks and joins the owned request even on assertion failure.
				defer func() {
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					if _, err := conn.Exec(cleanup, "SELECT pg_advisory_unlock(hashtextextended($1,0))", key); err != nil {
						t.Errorf("unlock: %v", err)
					}
					conn.Release()
					select {
					case <-done:
					case <-cleanup.Done():
						t.Error("share request did not terminate")
					}
				}()
				world.waitForLockWaiter(t, ctx, "advisory")
				change()
				if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1,0))", key); err != nil {
					t.Fatal(err)
				}
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			} else {
				change()
				router.ServeHTTP(rec, request(owner, http.MethodPost, path, body))
			}
			if rec.Code != c.Status {
				t.Fatalf("share status %d, want %d: %s", rec.Code, c.Status, rec.Body.String())
			}
			var attempts int
			var visibility string
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM transcript_share_attempts WHERE transcript_id=$1", world.transcript).Scan(&attempts); err != nil {
				t.Fatal(err)
			}
			if err := pool.QueryRow(ctx, "SELECT visibility FROM transcripts WHERE id=$1", world.transcript).Scan(&visibility); err != nil {
				t.Fatal(err)
			}
			if attempts != c.Attempts || visibility != c.Visibility {
				t.Fatalf("persisted attempts/visibility %d/%s, want %d/%s", attempts, visibility, c.Attempts, c.Visibility)
			}
			var actors []pgtype.UUID
			rows, err := pool.Query(ctx, "SELECT changed_by FROM transcript_governance_events_audit WHERE transcript_id=$1 AND seq>$2", world.transcript, world.auditSeq)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var actor pgtype.UUID
				if err := rows.Scan(&actor); err != nil {
					t.Fatal(err)
				}
				actors = append(actors, actor)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if c.Status != 200 && len(actors) != 0 {
				t.Fatalf("refused share appended governance events: %v", actors)
			}
			for _, actor := range actors {
				if actor != world.owner {
					t.Fatalf("share audit actor %v, want owner", actor)
				}
			}
			if c.Status >= 400 && strings.TrimSpace(rec.Body.String()) == "" {
				t.Fatal("refusal has no explanation")
			}
		})
	}
}

func assertIntersectingCollectiveShares(t *testing.T, ctx context.Context, h *Handler, world *shareWorld, c collectiveAuthorityCase) {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	otherTranscript := shareInsertTranscript(t, ctx, world.pool, world.moderator, world.localID+"-other", "private")
	ids := []string{uuid.UUID(world.group.Bytes).String(), uuid.UUID(world.second.Bytes).String()}
	sort.Strings(ids)
	gate, err := world.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackTxBestEffort("test_authority_gate", gate)
	if _, err := gate.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "village:collective-authority:"+ids[0]); err != nil {
		t.Fatal(err)
	}
	var gatePID int
	if err := gate.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&gatePID); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Post("/transcripts/{id}/share", h.ShareTranscript)
	request := func(actor pgtype.UUID, transcript pgtype.UUID, targets []string) <-chan *httptest.ResponseRecorder {
		body, err := json.Marshal(map[string][]string{"group_ids": targets})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan *httptest.ResponseRecorder, 1)
		workers.Add(1)
		go func() {
			defer workers.Done()
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/transcripts/"+uuid.UUID(transcript.Bytes).String()+"/share", bytes.NewReader(body))
			r = r.WithContext(context.WithValue(ctx, UserContextKey, &AuthUser{ID: uuid.UUID(actor.Bytes)}))
			router.ServeHTTP(rec, r)
			done <- rec
		}()
		return done
	}
	first := request(world.owner, world.transcript, ids)
	second := request(world.moderator, otherTranscript, []string{ids[1], ids[0]})
	defer func() { cancel(); rollbackTxBestEffort("test_authority_gate", gate); workers.Wait() }()
	// Both callers must really be waiting on the held minimum key. Without
	// sorted acquisition the reverse caller would hold the other key, forming
	// a cycle as soon as this gate releases. No timing-only start assumption.
	for {
		var waiters int
		if err := world.pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks w JOIN pg_locks g
		 ON w.locktype=g.locktype AND w.classid=g.classid AND w.objid=g.objid AND w.objsubid=g.objsubid
		 WHERE g.pid=$1 AND g.locktype='advisory' AND g.granted AND NOT w.granted`, gatePID).Scan(&waiters); err != nil {
			t.Fatal(err)
		}
		if waiters == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("two intersecting share waiters were not observed: ", ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, done := range []<-chan *httptest.ResponseRecorder{first, second} {
		select {
		case rec := <-done:
			if rec.Code != c.Status {
				t.Fatalf("intersecting share %d, want %d: %s", rec.Code, c.Status, rec.Body.String())
			}
		case <-ctx.Done():
			t.Fatal("intersecting share did not terminate: ", ctx.Err())
		}
	}
	var firstCount, otherCount int
	if err := world.pool.QueryRow(ctx, "SELECT count(*) FILTER (WHERE transcript_id=$1), count(*) FILTER (WHERE transcript_id=$2) FROM transcript_share_attempts WHERE transcript_id=ANY($3::uuid[])", world.transcript, otherTranscript, []pgtype.UUID{world.transcript, otherTranscript}).Scan(&firstCount, &otherCount); err != nil {
		t.Fatal(err)
	}
	if firstCount != c.Attempts || otherCount != c.Attempts {
		t.Fatalf("intersecting effects %d/%d, want %d each", firstCount, otherCount, c.Attempts)
	}
}

func assertOwnerAuthorityAfterRoleChange(t *testing.T, ctx context.Context, h *Handler, world *shareWorld, c collectiveAuthorityCase) {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := sqlc.New(world.pool).UpdateMemberRole(ctx, sqlc.UpdateMemberRoleParams{GroupID: world.group, UserID: world.owner, Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	var originalName string
	if err := world.pool.QueryRow(ctx, "SELECT name FROM groups WHERE id=$1", world.group).Scan(&originalName); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	writerDone := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		writerDone <- h.inCollectiveAuthorityTx(ctx, nil, world.owner, []pgtype.UUID{world.group}, func(q Querier) error {
			if err := q.UpdateMemberRole(ctx, sqlc.UpdateMemberRoleParams{GroupID: world.group, UserID: world.moderator, Role: "member"}); err != nil {
				return err
			}
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer func() { cancel(); unblock(); workers.Wait() }()
	select {
	case <-started:
	case err := <-writerDone:
		t.Fatal("role writer failed before barrier: ", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	router := chi.NewRouter()
	router.Patch("/groups/{id}", h.UpdateGroup)
	body, _ := json.Marshal(map[string]string{"name": "must-not-be-written"})
	request := httptest.NewRequest(http.MethodPatch, "/groups/"+uuid.UUID(world.group.Bytes).String(), bytes.NewReader(body))
	request = request.WithContext(context.WithValue(ctx, UserContextKey, &AuthUser{ID: uuid.UUID(world.moderator.Bytes)}))
	result := make(chan *httptest.ResponseRecorder, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, request)
		result <- rec
	}()
	world.waitForLockWaiter(t, ctx, "advisory")
	unblock()
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case rec := <-result:
		if rec.Code != c.Status {
			t.Fatalf("stale owner policy write %d, want %d: %s", rec.Code, c.Status, rec.Body.String())
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var currentName string
	if err := world.pool.QueryRow(ctx, "SELECT name FROM groups WHERE id=$1", world.group).Scan(&currentName); err != nil {
		t.Fatal(err)
	}
	if currentName != originalName {
		t.Fatalf("former owner's refused request changed collective name to %q", currentName)
	}
}
