//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	gh "github.com/peasant-labs/village/backend/internal/github"
	"golang.org/x/oauth2"
)

//go:embed testdata/authority-boundaries.yaml
var authorityBoundariesYAML []byte

type authorityBoundaryCase struct {
	Name      string   `yaml:"name"`
	Operation string   `yaml:"operation"`
	Newer     string   `yaml:"newer"`
	Key       string   `yaml:"key"`
	Fields    []string `yaml:"fields"`
}

func loadAuthorityBoundaryFixtures(t *testing.T) []authorityBoundaryCase {
	t.Helper()
	rows, err := decodeFixtureRows[authorityBoundaryCase](authorityBoundariesYAML)
	if err != nil {
		t.Fatal(err)
	}
	required := map[string]bool{
		"verified-only-share-waits-for-current-org-snapshot": false,
		"exhausted-org-generation-does-not-fetch":            false,
		"deleted-user-org-sync-does-not-fetch":               false,
		"missing-user-org-sync-does-not-fetch":               false,
		"batch-live-selection-revoked-before-final-write":    false, "newer-empty-org-snapshot-fences-old-login": false,
		"newer-failed-org-fetch-retains-complete-set": false, "org-second-write-failure-retains-complete-set": false,
		"org-pagination-installs-complete-set-preserving-visibility": false, "org-pagination-failed-prefix-retains-complete-set": false,
		"org-pagination-untrusted-next-retains-complete-set": false, "installation-binding-rechecks-current-org-control": false,
		"promote-member-serializes-current-owner-policy-write": false, "member-removal-retraction-failure-rolls-back-all-effects": false,
		"actor-lock-wait-is-bounded-and-releases-publish-lock": false, "collective-lock-wait-is-bounded-and-releases-publish-lock": false,
		"account-delete-cascade-waits-for-member-authority-row": false, "creator-delete-cascade-waits-for-collective-authority-row": false,
		"private-org-generation-never-enters-raw-user-endpoints": false,
	}
	for _, r := range rows {
		if _, ok := required[r.Name]; !ok || required[r.Name] {
			t.Fatalf("unknown/repeated authority case %q", r.Name)
		}
		required[r.Name] = true
	}
	for name, present := range required {
		if !present {
			t.Fatalf("missing required authority case %q", name)
		}
	}
	return rows
}

// These boundaries require actual transactions, FK cascades and trigger-written
// shares/audit. Reuse the disposable PostgreSQL harness and mock external
// GitHub only. Each owned request is joined before fixture teardown; every
// barrier observes its exact blocker PID, never an arbitrary cluster waiter.
func TestAuthorityWriterBoundaries(t *testing.T) {
	pool := govTestPool(t)
	defer pool.Close()
	for _, c := range loadAuthorityBoundaryFixtures(t) {
		t.Run(c.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			h := New(&config.Config{JWTSecret: installTestSecret, FrontendURL: "https://app.example.com"}, pool, nil)
			w := newShareWorldAt(t, ctx, pool, c.Name, "open", "private")
			t.Cleanup(func() { w.cleanup(t, context.Background()) })
			switch c.Operation {
			case "batch-revoked":
				boundaryBatchRevoked(t, ctx, h, w)
			case "org-overlap":
				boundaryOrgOverlap(t, ctx, h, w, c)
			case "org-write-failure":
				boundaryOrgFailure(t, ctx, h, w)
			case "org-share":
				boundaryOrgShare(t, ctx, h, w)
			case "generation-refusal":
				boundaryGenerationRefusal(t, ctx, h, w, c)
			case "org-pages":
				boundaryOrgPages(t, ctx, h, w, c)
			case "binding":
				boundaryBinding(t, ctx, h, w)
			case "promote":
				boundaryPromote(t, ctx, h, w)
			case "retract-failure":
				boundaryRetraction(t, ctx, h, w)
			case "timeout":
				boundaryTimeout(t, ctx, h, w, c)
			case "cascade":
				boundaryCascade(t, ctx, h, w, c)
			case "private-json":
				boundaryPrivateJSON(t, ctx, h, w, c)
			default:
				t.Fatalf("unimplemented boundary %q", c.Operation)
			}
		})
	}
}

func boundaryRequest(ctx context.Context, actor pgtype.UUID, method, path string, body any) *http.Request {
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			panic(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	return r.WithContext(context.WithValue(ctx, UserContextKey, &AuthUser{ID: uuid.UUID(actor.Bytes)}))
}
func boundaryRouter(h *Handler) chi.Router {
	r := chi.NewRouter()
	r.Post("/transcripts/{id}/share", h.ShareTranscript)
	r.Post("/groups/{id}/shares", h.BatchShareProject)
	r.Patch("/groups/{id}", h.UpdateGroup)
	r.Patch("/groups/{id}/members/{userID}/role", h.PromoteMember)
	r.Delete("/groups/{id}/members/{userID}", h.RemoveGroupMember)
	r.Delete("/groups/{id}/transcripts/{transcriptID}", h.RemoveGroupTranscript)
	r.Get("/auth/github/callback", h.GitHubCallback)
	r.Get("/integrations/github/callback", h.GitHubInstallCallback)
	r.Get("/auth/me", h.Me)
	r.Get("/users/{username}", h.GetUserPublicProfile)
	r.Delete("/auth/me", h.DeleteAccount)
	return r
}
func boundaryCall(t *testing.T, h *Handler, r *http.Request, want int) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	boundaryRouter(h).ServeHTTP(rec, r)
	if rec.Code != want {
		t.Fatalf("%s %s status %d want %d: %s", r.Method, r.URL.Path, rec.Code, want, rec.Body.String())
	}
	return rec
}

// Launch and register cleanup before returning. Cancellation releases blocked
// request queries; buffered result delivery cannot strand the goroutine.
func boundaryAsync(t *testing.T, h *Handler, r *http.Request) <-chan *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithCancel(r.Context())
	done := make(chan *httptest.ResponseRecorder, 1)
	joined := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(6 * time.Second):
			t.Error("owned request failed to terminate after cancellation")
		}
	})
	go func() {
		defer close(joined)
		rec := httptest.NewRecorder()
		boundaryRouter(h).ServeHTTP(rec, r.WithContext(ctx))
		done <- rec
	}()
	return done
}
func boundaryResult(t *testing.T, ctx context.Context, done <-chan *httptest.ResponseRecorder, want int) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case rec := <-done:
		if rec.Code != want {
			t.Fatalf("async status %d want %d: %s", rec.Code, want, rec.Body.String())
		}
		return rec
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return nil
	}
}
func boundaryGate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key string) (*pgxpool.Conn, func()) {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended($1,0))", key); err != nil {
		conn.Release()
		t.Fatal(err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := conn.Exec(cleanup, "SELECT pg_advisory_unlock(hashtextextended($1,0))", key); err != nil {
				t.Error(err)
			}
			conn.Release()
		})
	}
	t.Cleanup(release)
	return conn, release
}

func boundaryBatchRevoked(t *testing.T, ctx context.Context, h *Handler, w *shareWorld) {
	project := strings.Repeat("a", 64)
	if _, err := w.pool.Exec(ctx, "UPDATE transcripts SET project_hash=$2 WHERE id=$1", w.transcript, project); err != nil {
		t.Fatal(err)
	}
	other := shareInsertTranscript(t, ctx, w.pool, w.owner, w.localID+"-b", "private")
	if _, err := w.pool.Exec(ctx, "UPDATE transcripts SET project_hash=$2 WHERE id=$1", other, project); err != nil {
		t.Fatal(err)
	}
	if code := w.submitTo(t, h, w.group); code != 200 {
		t.Fatal(code)
	}
	gate, release := boundaryGate(t, ctx, w.pool, sessionPublishLockKey(w.owner, w.localID+"-b"))
	done := boundaryAsync(t, h, boundaryRequest(ctx, w.owner, "POST", "/groups/"+uuid.UUID(w.group.Bytes).String()+"/shares", map[string]any{"project_hash": project, "visibility_confirmed": true}))
	waitForSessionBlockedBy(t, ctx, w.pool, backendPID(t, ctx, gate))
	boundaryCall(t, h, boundaryRequest(ctx, w.moderator, "DELETE", "/groups/"+uuid.UUID(w.group.Bytes).String()+"/transcripts/"+uuid.UUID(w.transcript.Bytes).String(), nil), 200)
	release()
	boundaryResult(t, ctx, done, 409)
	var count int
	if err := w.pool.QueryRow(ctx, "SELECT count(*) FROM transcript_share_attempts WHERE transcript_id=$1", other).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 || readTranscriptVisibility(t, ctx, w.pool, other) != "private" {
		t.Fatal("changed complete selection committed remaining transcript")
	}
}

type boundaryTransport func(*http.Request) (*http.Response, error)

func (f boundaryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func boundaryResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func boundaryLoginRequest(t *testing.T, ctx context.Context, w *shareWorld, orgs boundaryTransport) *http.Request {
	t.Helper()
	var githubID int64
	if err := w.pool.QueryRow(ctx, "SELECT github_id FROM users WHERE id=$1", w.owner).Scan(&githubID); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: boundaryTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			return boundaryResponse(200, `{"access_token":"disposable","token_type":"bearer"}`), nil
		case "/user":
			return boundaryResponse(200, fmt.Sprintf(`{"id":%d,"login":%q}`, githubID, w.ownerName)), nil
		case "/user/orgs":
			return orgs(r)
		default:
			return nil, fmt.Errorf("unexpected external OAuth endpoint")
		}
	})}
	r := httptest.NewRequest("GET", "/auth/github/callback?state=boundary&code=disposable", nil)
	r.AddCookie(&http.Cookie{Name: "oauth_state", Value: "boundary"})
	return r.WithContext(context.WithValue(ctx, oauth2.HTTPClient, client))
}
func boundarySeedOrg(t *testing.T, ctx context.Context, w *shareWorld) {
	t.Helper()
	if _, err := w.pool.Exec(ctx, "INSERT INTO user_github_orgs(user_id,org_login,org_id,visible) VALUES($1,'kept',101,true)", w.owner); err != nil {
		t.Fatal(err)
	}
}
func boundaryOrgs(t *testing.T, ctx context.Context, w *shareWorld) []sqlc.ListUserAllOrgsRow {
	t.Helper()
	rows, err := sqlc.New(w.pool).ListUserAllOrgs(ctx, w.owner)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
func boundaryOrgOverlap(t *testing.T, ctx context.Context, h *Handler, w *shareWorld, c authorityBoundaryCase) {
	boundarySeedOrg(t, ctx, w)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	old := boundaryAsync(t, h, boundaryLoginRequest(t, ctx, w, func(r *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-release:
			return boundaryResponse(200, `[{"id":202,"login":"resurrected"}]`), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}))
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	otherHandler := New(h.cfg, w.pool, nil)
	boundaryCall(t, otherHandler, boundaryLoginRequest(t, ctx, w, func(*http.Request) (*http.Response, error) {
		if c.Newer == "failed" {
			return boundaryResponse(503, `{}`), nil
		}
		return boundaryResponse(200, `[]`), nil
	}), 307)
	unblock()
	boundaryResult(t, ctx, old, 307)
	// Two mounted logins issued exactly two durable generations, even when
	// the second response is empty or fails. This count is this case's contract,
	// not a deletion guard for a fixture catalog.
	user, err := sqlc.New(w.pool).GetUserByID(ctx, w.owner)
	if err != nil || user.GithubOrgSyncGeneration != 2 {
		t.Fatalf("overlapping issuance fence %d, want 2: %v", user.GithubOrgSyncGeneration, err)
	}
	rows := boundaryOrgs(t, ctx, w)
	if c.Newer == "empty" && len(rows) != 0 {
		t.Fatalf("old login resurrected membership: %+v", rows)
	}
	if c.Newer == "failed" && (len(rows) != 1 || rows[0].OrgLogin != "kept" || !rows[0].Visible) {
		t.Fatalf("failed newer login lost previous complete set: %+v", rows)
	}
	if c.Newer == "empty" {
		if _, err := w.pool.Exec(ctx, "UPDATE groups SET acceptance_mode='verified_only' WHERE id=$1", w.group); err != nil {
			t.Fatal(err)
		}
		boundaryCall(t, h, boundaryRequest(ctx, w.owner, "POST", "/transcripts/"+uuid.UUID(w.transcript.Bytes).String()+"/share", map[string][]string{"group_ids": {uuid.UUID(w.group.Bytes).String()}}), 403)
	}
}

// Faults are narrow test-owned triggers and always removed, never migrations.
func boundaryFault(t *testing.T, ctx context.Context, w *shareWorld, table, condition, action string) {
	t.Helper()
	name := "boundary_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := w.pool.Exec(ctx, "CREATE FUNCTION "+name+"() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN "+action+"; RETURN NEW; END $$"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := w.pool.Exec(context.Background(), "DROP TRIGGER IF EXISTS "+name+" ON "+table+"; DROP FUNCTION IF EXISTS "+name+"()"); err != nil {
			t.Error(err)
		}
	})
	if _, err := w.pool.Exec(ctx, "CREATE TRIGGER "+name+" BEFORE INSERT OR UPDATE ON "+table+" FOR EACH ROW WHEN ("+condition+") EXECUTE FUNCTION "+name+"()"); err != nil {
		t.Fatal(err)
	}
}
func boundaryOrgFailure(t *testing.T, ctx context.Context, h *Handler, w *shareWorld) {
	boundarySeedOrg(t, ctx, w)
	boundaryFault(t, ctx, w, "user_github_orgs", "NEW.user_id='"+uuid.UUID(w.owner.Bytes).String()+"'::uuid AND NEW.org_id=303", "RAISE EXCEPTION 'injected org write refusal'")
	boundaryCall(t, h, boundaryLoginRequest(t, ctx, w, func(*http.Request) (*http.Response, error) {
		return boundaryResponse(200, `[{"id":202,"login":"new-one"},{"id":303,"login":"new-two"}]`), nil
	}), 307)
	rows := boundaryOrgs(t, ctx, w)
	if len(rows) != 1 || rows[0].OrgLogin != "kept" || !rows[0].Visible {
		t.Fatalf("org write failure partially installed: %+v", rows)
	}
}

func boundaryOrgShare(t *testing.T, ctx context.Context, h *Handler, w *shareWorld) {
	boundarySeedOrg(t, ctx, w)
	if _, err := w.pool.Exec(ctx, "UPDATE groups SET acceptance_mode='verified_only',linked_github_org='kept' WHERE id=$1", w.group); err != nil {
		t.Fatal(err)
	}
	key := "test:org-install:" + uuid.NewString()
	gate, release := boundaryGate(t, ctx, w.pool, key)
	defer release()
	boundaryFault(t, ctx, w, "user_github_orgs", "NEW.user_id='"+uuid.UUID(w.owner.Bytes).String()+"'::uuid AND NEW.org_id=202", "PERFORM pg_advisory_xact_lock(hashtextextended('"+key+"',0))")
	login := boundaryAsync(t, h, boundaryLoginRequest(t, ctx, w, func(*http.Request) (*http.Response, error) {
		return boundaryResponse(200, `[{"id":202,"login":"other"}]`), nil
	}))
	writerPID := boundaryBlockedPID(t, ctx, w.pool, backendPID(t, ctx, gate))
	share := boundaryAsync(t, h, boundaryRequest(ctx, w.owner, "POST", "/transcripts/"+uuid.UUID(w.transcript.Bytes).String()+"/share", map[string][]string{"group_ids": {uuid.UUID(w.group.Bytes).String()}}))
	waitForSessionBlockedBy(t, ctx, w.pool, writerPID)
	release()
	boundaryResult(t, ctx, login, 307)
	boundaryResult(t, ctx, share, 403)
	rows := boundaryOrgs(t, ctx, w)
	if len(rows) != 1 || rows[0].OrgLogin != "other" {
		t.Fatalf("snapshot did not replace old authority: %+v", rows)
	}
}
func boundaryGenerationRefusal(t *testing.T, ctx context.Context, h *Handler, w *shareWorld, c authorityBoundaryCase) {
	id := w.owner
	switch c.Key {
	case "exhausted":
		if _, err := w.pool.Exec(ctx, "UPDATE users SET github_org_sync_generation=9223372036854775807 WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
	case "deleted":
		boundaryCall(t, h, boundaryRequest(ctx, w.owner, "DELETE", "/auth/me", nil), 200)
		t.Cleanup(func() { purgeAuditRows(t, context.Background(), w.pool, []pgtype.UUID{w.transcript}) })
	case "missing":
		id = toPgUUID(uuid.New())
	default:
		t.Fatalf("unknown generation refusal %q", c.Key)
	}
	called := false
	client := &http.Client{Transport: boundaryTransport(func(*http.Request) (*http.Response, error) { called = true; return boundaryResponse(200, `[]`), nil })}
	if err := h.syncGitHubOrgSnapshot(ctx, client, id); err == nil {
		t.Fatal("invalid generation/user did not refuse organization fetch")
	}
	if called {
		t.Fatal("generation refusal still fetched organizations")
	}
}
func boundaryOrgPages(t *testing.T, ctx context.Context, h *Handler, w *shareWorld, c authorityBoundaryCase) {
	boundarySeedOrg(t, ctx, w)
	boundaryCall(t, h, boundaryLoginRequest(t, ctx, w, func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("page") == "2" {
			if c.Newer == "failed" {
				return boundaryResponse(503, `{}`), nil
			}
			return boundaryResponse(200, `[{"id":202,"login":"second"}]`), nil
		}
		resp := boundaryResponse(200, `[{"id":101,"login":"kept"}]`)
		next := "https://api.github.com/user/orgs?per_page=100&page=2"
		if c.Newer == "untrusted" {
			next = "https://not-github.example/user/orgs?per_page=100&page=2"
		}
		resp.Header.Set("Link", "<"+next+">; rel=\"next\"")
		return resp, nil
	}), 307)
	rows := boundaryOrgs(t, ctx, w)
	want := 1
	if c.Newer == "complete" {
		want = 2
	}
	if len(rows) != want || rows[0].OrgLogin != "kept" || !rows[0].Visible {
		t.Fatalf("incomplete page replaced membership or lost visibility: %+v", rows)
	}
}

func boundaryRetraction(t *testing.T, ctx context.Context, h *Handler, w *shareWorld) {
	if code := w.submitTo(t, h, w.group); code != 200 {
		t.Fatal(code)
	}
	if _, err := w.pool.Exec(ctx, "UPDATE groups SET transcript_deletion_policy='mandatory' WHERE id=$1", w.group); err != nil {
		t.Fatal(err)
	}
	before := boundaryShareState(t, ctx, w)
	boundaryFault(t, ctx, w, "transcript_share_attempts", "NEW.transcript_id='"+uuid.UUID(w.transcript.Bytes).String()+"'::uuid", "RAISE EXCEPTION 'injected required retraction refusal'")
	boundaryCall(t, h, boundaryRequest(ctx, w.moderator, "DELETE", "/groups/"+uuid.UUID(w.group.Bytes).String()+"/members/"+uuid.UUID(w.owner.Bytes).String(), nil), 500)
	member, err := sqlc.New(w.pool).GetGroupMember(ctx, sqlc.GetGroupMemberParams{GroupID: w.group, UserID: w.owner})
	if err != nil || member.Role != "member" {
		t.Fatalf("membership deletion escaped rollback: %+v %v", member, err)
	}
	if after := boundaryShareState(t, ctx, w); !bytes.Equal(before, after) {
		t.Fatalf("retraction failure changed ledger/projection/visibility/audit: before=%s after=%s", before, after)
	}
}
func boundaryShareState(t *testing.T, ctx context.Context, w *shareWorld) []byte {
	t.Helper()
	var raw []byte
	err := w.pool.QueryRow(ctx, `SELECT jsonb_build_object('attempts',(SELECT coalesce(jsonb_agg(to_jsonb(a) ORDER BY event_num),'[]') FROM transcript_share_attempts a WHERE transcript_id=$1), 'shares',(SELECT coalesce(jsonb_agg(to_jsonb(s)),'[]') FROM transcript_shares s WHERE transcript_id=$1), 'visibility',(SELECT visibility FROM transcripts WHERE id=$1), 'audit',(SELECT coalesce(jsonb_agg(to_jsonb(e) ORDER BY seq),'[]') FROM transcript_governance_events_audit e WHERE transcript_id=$1))`, w.transcript).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func boundaryTimeout(t *testing.T, ctx context.Context, h *Handler, w *shareWorld, c authorityBoundaryCase) {
	h.collectiveAuthorityTimeout = 750 * time.Millisecond
	key := "village:collective-actor:" + uuid.UUID(w.owner.Bytes).String()
	if c.Key == "collective" {
		key = "village:collective-authority:" + uuid.UUID(w.group.Bytes).String()
	}
	gate, release := boundaryGate(t, ctx, w.pool, key)
	defer release()
	done := boundaryAsync(t, h, boundaryRequest(ctx, w.owner, "POST", "/transcripts/"+uuid.UUID(w.transcript.Bytes).String()+"/share", map[string][]string{"group_ids": {uuid.UUID(w.group.Bytes).String()}}))
	waitForSessionBlockedBy(t, ctx, w.pool, backendPID(t, ctx, gate))
	short, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	boundaryResult(t, short, done, 500)
	var available bool
	check, err := w.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Release()
	if err := check.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1,0))", sessionPublishLockKey(w.owner, w.localID)).Scan(&available); err != nil {
		t.Fatal(err)
	}
	// The check must hold and release on one pinned physical connection.
	if !available {
		t.Fatal("timed-out authority operation retained its publish lock")
	}
	if _, err := check.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1,0))", sessionPublishLockKey(w.owner, w.localID)); err != nil {
		t.Fatal(err)
	}
	if readTranscriptVisibility(t, ctx, w.pool, w.transcript) != "private" {
		t.Fatal("timeout widened visibility")
	}
	var count int
	if err := w.pool.QueryRow(ctx, "SELECT count(*) FROM transcript_share_attempts WHERE transcript_id=$1", w.transcript).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("timeout committed share")
	}
}

func boundaryPrivateJSON(t *testing.T, ctx context.Context, h *Handler, w *shareWorld, c authorityBoundaryCase) {
	if _, err := w.pool.Exec(ctx, "UPDATE users SET is_discoverable=true, github_org_sync_generation=73 WHERE id=$1", w.owner); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/auth/me", "/users/" + w.ownerName} {
		rec := boundaryCall(t, h, boundaryRequest(ctx, w.owner, "GET", path, nil), 200)
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if _, exists := raw["github_org_sync_generation"]; exists {
			t.Fatalf("private generation leaked on %s", path)
		}
		if len(c.Fields) == 0 {
			t.Fatal("raw user fixture lacks independent field inventory")
		}
		expected := map[string]bool{}
		for _, field := range c.Fields {
			expected[field] = true
			if _, ok := raw[field]; !ok {
				t.Fatalf("user response lost field %s", field)
			}
		}
		for field := range raw {
			if !expected[field] {
				t.Fatalf("unexpected user field %s", field)
			}
		}
	}
}

func boundaryBinding(t *testing.T, ctx context.Context, h *Handler, w *shareWorld) {
	if err := sqlc.New(w.pool).UpdateMemberRole(ctx, sqlc.UpdateMemberRoleParams{GroupID: w.group, UserID: w.owner, Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(ctx, "INSERT INTO user_github_orgs(user_id,org_login,org_id) VALUES($1,'binding-org',1)", w.owner); err != nil {
		t.Fatal(err)
	}
	f := &fakeGitHub{installationID: 777123, installationAccount: "binding-org"}
	newFakeGitHub(t, f)
	client, err := gh.NewClient(gh.Config{AppID: "123", PrivateKeyPEM: testAppPEM(t)}, gh.WithBaseURL(f.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	h.gh = client
	t.Cleanup(func() {
		if _, err := w.pool.Exec(context.Background(), "DELETE FROM github_app_installations WHERE installation_id=777123"); err != nil {
			t.Error(err)
		}
	})
	started, allow := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(allow) }) }
	defer unblock()
	login := boundaryAsync(t, h, boundaryLoginRequest(t, ctx, w, func(r *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-allow:
			return boundaryResponse(200, `[]`), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}))
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	gate, release := boundaryGate(t, ctx, w.pool, "village:collective-actor:"+uuid.UUID(w.owner.Bytes).String())
	defer release()
	pid := backendPID(t, ctx, gate)
	unblock()
	waitForSessionBlockedBy(t, ctx, w.pool, pid) // the empty snapshot install queues FIRST
	state, err := auth.CreateInstallState(installTestSecret, uuid.UUID(w.owner.Bytes).String(), uuid.UUID(w.group.Bytes).String())
	if err != nil {
		t.Fatal(err)
	}
	callback := boundaryAsync(t, h, boundaryRequest(ctx, w.owner, "GET", "/integrations/github/callback?installation_id=777123&state="+url.QueryEscape(state), nil))
	waitBoundaryBlockers(t, ctx, w.pool, pid, 2)
	release()
	boundaryResult(t, ctx, login, 307)
	boundaryResult(t, ctx, callback, 500)
	group, err := sqlc.New(w.pool).GetGroupByID(ctx, w.group)
	if err != nil {
		t.Fatal(err)
	}
	if group.LinkedGithubOrg.Valid {
		t.Fatalf("stale identity bound organization: %+v", group.LinkedGithubOrg)
	}
}

func waitBoundaryBlockers(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pid int32, want int) {
	t.Helper()
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		var n int
		if err := pool.QueryRow(deadline, "SELECT count(*) FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))", pid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n >= want {
			return
		}
		select {
		case <-deadline.Done():
			t.Fatalf("owned backend %d did not block %d requests", pid, want)
		case <-time.After(5 * time.Millisecond):
		}
	}
}
func boundaryBlockedPID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blocker int32) int32 {
	t.Helper()
	waitForSessionBlockedBy(t, ctx, pool, blocker)
	var pid int32
	if err := pool.QueryRow(ctx, "SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) ORDER BY pid LIMIT 1", blocker).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	return pid
}
func boundaryPromote(t *testing.T, ctx context.Context, h *Handler, w *shareWorld) {
	if err := sqlc.New(w.pool).UpdateMemberRole(ctx, sqlc.UpdateMemberRoleParams{GroupID: w.group, UserID: w.owner, Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	key := "test:role:" + uuid.NewString()
	gate, release := boundaryGate(t, ctx, w.pool, key)
	defer release()
	boundaryFault(t, ctx, w, "group_members", "NEW.group_id='"+uuid.UUID(w.group.Bytes).String()+"'::uuid AND NEW.user_id='"+uuid.UUID(w.moderator.Bytes).String()+"'::uuid AND NEW.role='member'", "PERFORM pg_advisory_xact_lock(hashtextextended('"+key+"',0))")
	promote := boundaryAsync(t, h, boundaryRequest(ctx, w.owner, "PATCH", "/groups/"+uuid.UUID(w.group.Bytes).String()+"/members/"+uuid.UUID(w.moderator.Bytes).String()+"/role", map[string]string{"role": "member"}))
	writerPID := boundaryBlockedPID(t, ctx, w.pool, backendPID(t, ctx, gate))
	edit := boundaryAsync(t, h, boundaryRequest(ctx, w.moderator, "PATCH", "/groups/"+uuid.UUID(w.group.Bytes).String(), map[string]string{"name": "must-not-write"}))
	waitForSessionBlockedBy(t, ctx, w.pool, writerPID)
	release()
	boundaryResult(t, ctx, promote, 200)
	boundaryResult(t, ctx, edit, 403)
	group, err := sqlc.New(w.pool).GetGroupByID(ctx, w.group)
	if err != nil {
		t.Fatal(err)
	}
	if group.Name == "must-not-write" {
		t.Fatal("demoted owner changed policy")
	}
}
func boundaryCascade(t *testing.T, ctx context.Context, h *Handler, w *shareWorld, c authorityBoundaryCase) {
	key := "test:cascade:" + uuid.NewString()
	gate, release := boundaryGate(t, ctx, w.pool, key)
	defer release()
	boundaryFault(t, ctx, w, "transcript_share_attempts", "NEW.transcript_id='"+uuid.UUID(w.transcript.Bytes).String()+"'::uuid", "PERFORM pg_advisory_xact_lock(hashtextextended('"+key+"',0))")
	share := boundaryAsync(t, h, boundaryRequest(ctx, w.owner, "POST", "/transcripts/"+uuid.UUID(w.transcript.Bytes).String()+"/share", map[string][]string{"group_ids": {uuid.UUID(w.group.Bytes).String()}}))
	writerPID := boundaryBlockedPID(t, ctx, w.pool, backendPID(t, ctx, gate))
	actor := w.owner
	if c.Key == "group" {
		actor = w.moderator
	}
	// Account deletion bypasses collective advisory locks. Capture the audit
	// identity now: its deletion-surviving events must be purged after teardown.
	t.Cleanup(func() { purgeAuditRows(t, context.Background(), w.pool, []pgtype.UUID{w.transcript}) })
	deletion := boundaryAsync(t, h, boundaryRequest(ctx, actor, "DELETE", "/auth/me", nil))
	waitForSessionBlockedBy(t, ctx, w.pool, writerPID)
	release()
	boundaryResult(t, ctx, share, 200)
	boundaryResult(t, ctx, deletion, 200)
	var exists bool
	query := "SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id=$1 AND user_id=$2)"
	if c.Key == "group" {
		query = "SELECT EXISTS(SELECT 1 FROM groups WHERE id=$1 AND created_by=$2)"
	}
	if err := w.pool.QueryRow(ctx, query, w.group, actor).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("account cascade did not complete after authority released")
	}
}
