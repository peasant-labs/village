//go:build integration

package router

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	mathrand "math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/storage"
)

const routeBytesSecret = "route-bytes-test-only-secret"

// routeBytesWorld is the seeded state the database cases run against. Every
// identity is fresh per run, so concurrent runs against one database do not
// collide, and every row is removed in the order TESTING.md prescribes.
type routeBytesWorld struct {
	pool   *pgxpool.Pool
	values map[string]string
	// tokens are the sessions a case may carry, by its "as" name.
	tokens map[string]string
	// githubSignIn and gitlabSignIn are the provider identities the fake
	// providers report; installations are the App installations they know.
	githubSignIn, gitlabSignIn int64
	installations              map[int64]int64
	blobs                      *recordingBlobStore
}

// recordingBlobStore stands in for encrypted object storage on the deletion
// path: it records which objects deletion removed and refuses everything else,
// so a route that reached for content would fail loudly.
type recordingBlobStore struct {
	storage.TranscriptBlobStore
	mu      sync.Mutex
	deleted []string
}

func (b *recordingBlobStore) Delete(_ context.Context, d storage.BlobDescriptor) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deleted = append(b.deleted, string(d.ObjectKey()))
	return nil
}

func TestRouteBytesPostgres(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(pool); err != nil {
		t.Fatal(err)
	}
	w := seedRouteBytesWorld(t, pool)

	plain := New(&config.Config{JWTSecret: routeBytesSecret, FrontendURL: "https://example.test"}, pool, nil, nil)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	configured := New(&config.Config{
		JWTSecret: routeBytesSecret, FrontendURL: "https://example.test", BaseURL: "https://api.example.test",
		GitHubClientID: "client", GitHubClientSecret: "secret", GitLabClientID: "client", GitLabClientSecret: "secret",
		GitHubAppID: "1", GitHubAppSlug: "village-golden",
		GitHubAppPrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
	}, pool, w.blobs, nil)

	providers := fakeProviders{
		githubUser: func() (int64, string) { return w.githubSignIn, w.values["github_sign_in"] },
		gitlabUser: func() (int64, string) { return w.gitlabSignIn, w.values["gitlab_sign_in"] },
		installations: func(id int64) (string, int64, bool) {
			account, ok := w.installations[id]
			if account == w.githubAccountID(t) {
				return w.values["login"], account, ok
			}
			return w.values["login"] + "-org", account, ok
		},
	}
	// The GitHub App client sends through the default transport, which the
	// router offers no other way to replace; only the install callback cases
	// reach it. No test in this package runs in parallel with this one.
	defaultTransport := http.DefaultTransport
	http.DefaultTransport = providers
	t.Cleanup(func() { http.DefaultTransport = defaultTransport })

	for _, c := range routeByteCases(t) {
		if !c.Database {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			req := httptest.NewRequest(c.Method, w.replace(c.Path), strings.NewReader(w.replace(c.RequestBody)))
			switch c.As {
			case routeByteCallerAnonymous:
			case "":
				req.Header.Set("Authorization", "Bearer "+w.tokens["account"])
			default:
				token, ok := w.tokens[string(c.As)]
				if !ok {
					t.Fatalf("case %q signs in as unknown account %q", c.Name, c.As)
				}
				req.Header.Set("Authorization", "Bearer "+token)
			}
			for k, v := range c.Headers {
				req.Header.Set(k, w.replace(v))
			}
			for k, v := range c.Cookies {
				req.AddCookie(&http.Cookie{Name: k, Value: w.replace(v)})
			}
			if c.Providers {
				req = req.WithContext(context.WithValue(req.Context(), oauth2.HTTPClient, &http.Client{Transport: providers}))
			}
			routes := plain
			if c.Configured {
				routes = configured
			}
			rec := httptest.NewRecorder()
			routes.ServeHTTP(rec, req)
			for _, name := range c.Capture {
				capture, ok := routeByteCaptures[name]
				if !ok {
					t.Fatalf("case %q names unknown capture %q", c.Name, name)
				}
				capture(t, w, rec)
			}
			if rec.Code != c.Status {
				t.Fatalf("status = %d (body %q), want %d", rec.Code, rec.Body.String(), c.Status)
			}
			assertRouteBody(t, c, rec.Body.String(), w.replace(c.Body))
			for k, v := range c.Want {
				if got := strings.Join(rec.Header().Values(k), ", "); got != w.replace(v) {
					t.Errorf("%s = %q, want %q", k, got, w.replace(v))
				}
			}
			if c.Name == "list-api-keys" && (strings.Contains(rec.Body.String(), w.values["key"]) || strings.Contains(rec.Body.String(), "key_hash")) {
				t.Fatal("list exposed the plaintext key or key hash")
			}
		})
	}
}

func (w *routeBytesWorld) replace(value string) string {
	for k, v := range w.values {
		value = strings.ReplaceAll(value, "{{"+k+"}}", v)
	}
	return value
}

func (w *routeBytesWorld) githubAccountID(t *testing.T) int64 {
	n, err := strconv.ParseInt(w.values["github_id"], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (w *routeBytesWorld) scan(t *testing.T, sql string, args []any, dest ...any) {
	t.Helper()
	if err := w.pool.QueryRow(context.Background(), sql, args...).Scan(dest...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// routeBytesTime renders a stored timestamp the way the pgx JSON encoder does.
func routeBytesTime(at time.Time) string { return at.Format(time.RFC3339Nano) }

// routeByteCaptures read back the values a response carries that the server
// generates: identifiers, timestamps, tokens and one-time codes. Each checks
// what it reads before the golden uses it.
var routeByteCaptures = map[string]func(*testing.T, *routeBytesWorld, *httptest.ResponseRecorder){
	"api-key": func(t *testing.T, w *routeBytesWorld, rec *httptest.ResponseRecorder) {
		var got struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^peasant_[0-9a-f]{64}$`).MatchString(got.Key) {
			t.Fatalf("bad key shape %q", got.Key)
		}
		var id uuid.UUID
		var created time.Time
		w.scan(t, `SELECT id,created_at FROM api_keys WHERE user_id=$1`, []any{w.values["account_id"]}, &id, &created)
		if got.ID != id.String() {
			t.Fatalf("returned key id %q does not match stored %s", got.ID, id)
		}
		w.values["id"], w.values["key"], w.values["created_at"] = got.ID, got.Key, routeBytesTime(created)
	},
	"revoked": func(t *testing.T, w *routeBytesWorld, _ *httptest.ResponseRecorder) {
		var revoked *time.Time
		w.scan(t, `SELECT revoked_at FROM api_keys WHERE id=$1`, []any{w.values["id"]}, &revoked)
		if revoked == nil {
			t.Fatal("revocation was not recorded")
		}
	},
	"account-times": func(t *testing.T, w *routeBytesWorld, _ *httptest.ResponseRecorder) {
		var created, updated time.Time
		w.scan(t, `SELECT created_at,updated_at FROM users WHERE id=$1`, []any{w.values["account_id"]}, &created, &updated)
		w.values["account_created_at"], w.values["account_updated_at"] = routeBytesTime(created), routeBytesTime(updated)
	},
	"account-deleted": func(t *testing.T, w *routeBytesWorld, _ *httptest.ResponseRecorder) {
		var n int
		w.scan(t, `SELECT count(*) FROM users WHERE id=$1`, []any{w.values["deletable_id"]}, &n)
		if n != 0 {
			t.Fatal("account deletion left the account row")
		}
	},
	"web-session": func(t *testing.T, w *routeBytesWorld, rec *httptest.ResponseRecorder) {
		w.values["session"] = sessionFromLocation(t, rec, w.values["github_sign_in"])
	},
	"provider-session": func(t *testing.T, w *routeBytesWorld, rec *httptest.ResponseRecorder) {
		w.values["session"] = sessionFromLocation(t, rec, w.values["gitlab_sign_in"])
	},
	"cli-code": func(t *testing.T, w *routeBytesWorld, _ *httptest.ResponseRecorder) {
		var code string
		w.scan(t, `SELECT exchange_code FROM cli_auth_sessions WHERE oauth_state=$1`, []any{w.values["cli_oauth_state"]}, &code)
		w.values["cli_code"] = code
	},
	"install-state": func(t *testing.T, w *routeBytesWorld, rec *httptest.ResponseRecorder) {
		location, err := url.Parse(rec.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		state := location.Query().Get("state")
		claims, err := auth.ValidateInstallState(routeBytesSecret, state)
		if err != nil || claims.UserID != w.values["account_id"] || claims.GroupID != w.values["group_id"] {
			t.Fatalf("install state %q does not bind the caller to the collective: %+v %v", state, claims, err)
		}
		w.values["install_state"] = state
	},
	"public-transcript-times": func(t *testing.T, w *routeBytesWorld, _ *httptest.ResponseRecorder) {
		var published, updated time.Time
		w.scan(t, `SELECT published_at,updated_at FROM transcripts WHERE id=$1`, []any{w.values["public_id"]}, &published, &updated)
		w.values["published_at"], w.values["transcript_updated_at"] = routeBytesTime(published), routeBytesTime(updated)
	},
	"attestation": func(t *testing.T, w *routeBytesWorld, _ *httptest.ResponseRecorder) {
		var id uuid.UUID
		var created time.Time
		w.scan(t, `SELECT id,created_at FROM attestations WHERE transcript_id=$1`, []any{w.values["public_id"]}, &id, &created)
		w.values["attestation_id"], w.values["attestation_created_at"] = id.String(), routeBytesTime(created)
	},
	"annotation": func(t *testing.T, w *routeBytesWorld, _ *httptest.ResponseRecorder) {
		var id uuid.UUID
		var hash string
		var created time.Time
		w.scan(t, `SELECT id,content_hash,created_at FROM annotations WHERE target_transcript_id=$1`, []any{w.values["public_id"]}, &id, &hash, &created)
		w.values["annotation_id"], w.values["annotation_hash"] = id.String(), hash
		w.values["annotation_created_at"] = strconv.FormatInt(created.UnixMilli(), 10)
	},
	"transcript-deleted": func(t *testing.T, w *routeBytesWorld, _ *httptest.ResponseRecorder) {
		var n int
		w.scan(t, `SELECT count(*) FROM transcripts WHERE id=$1`, []any{w.values["private_id"]}, &n)
		w.blobs.mu.Lock()
		deleted := strings.Join(w.blobs.deleted, ",")
		w.blobs.mu.Unlock()
		if n != 0 || deleted != "transcripts/"+w.values["private_id"]+".bin" {
			t.Fatalf("deletion left %d rows and removed objects %q", n, deleted)
		}
	},
}

// sessionFromLocation reads the session a web sign-in hands the frontend and
// checks that it names the account the provider signed in.
func sessionFromLocation(t *testing.T, rec *httptest.ResponseRecorder, handle string) string {
	t.Helper()
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	token := location.Query().Get("token")
	claims, err := auth.ValidateToken(routeBytesSecret, token)
	if err != nil || claims.Username != handle {
		t.Fatalf("sign-in session %q does not name %q: %+v %v", token, handle, claims, err)
	}
	return token
}

func seedRouteBytesWorld(t *testing.T, pool *pgxpool.Pool) *routeBytesWorld {
	t.Helper()
	ctx := context.Background()
	suffix := func() string {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		return hex.EncodeToString(b)
	}
	w := &routeBytesWorld{pool: pool, values: map[string]string{}, tokens: map[string]string{}, blobs: &recordingBlobStore{}}
	var users []uuid.UUID
	var transcripts []uuid.UUID
	t.Cleanup(func() {
		ctx := context.Background()
		rows, err := pool.Query(ctx, `SELECT id FROM transcripts WHERE owner_id = ANY($1::uuid[])`, users)
		if err == nil {
			for rows.Next() {
				var id uuid.UUID
				if rows.Scan(&id) == nil {
					transcripts = append(transcripts, id)
				}
			}
			rows.Close()
		}
		if len(transcripts) > 0 {
			execRouteBytesSystem(t, `DELETE FROM transcripts WHERE id = ANY($1::uuid[])`, pool, transcripts)
		}
		for sql, key := range map[string]string{
			`DELETE FROM tags WHERE name = $1`:                     w.values["tag"],
			`DELETE FROM cli_auth_sessions WHERE oauth_state = $1`: w.values["cli_oauth_state"],
		} {
			if _, err := pool.Exec(ctx, sql, key); err != nil {
				t.Errorf("%s: %v", sql, err)
			}
		}
		if _, err := pool.Exec(ctx, `DELETE FROM github_app_installations WHERE installation_id = ANY($1)`, keysOf(w.installations)); err != nil {
			t.Errorf("remove installations: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1::uuid[]) OR github_id = $2`,
			users, w.githubSignIn); err != nil {
			t.Errorf("remove test users: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE provider = 'gitlab' AND provider_user_id = $1`, strconv.FormatInt(w.gitlabSignIn, 10)); err != nil {
			t.Errorf("remove the gitlab sign-in: %v", err)
		}
		purgeRouteBytesAudit(t, pool, transcripts)
	})
	insertUser := func(name string) uuid.UUID {
		id := uuid.New()
		login := "golden-" + suffix()
		githubID := 7_000_000_000 + mathrand.Int63n(1_000_000_000)
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,github_id,github_username,provider_user_id)
			VALUES($1,$2,$3::text,$2::bigint::text)`, id, githubID, login); err != nil {
			t.Fatal(err)
		}
		users = append(users, id)
		token, err := auth.CreateToken(routeBytesSecret, id, login)
		if err != nil {
			t.Fatal(err)
		}
		w.tokens[name] = token
		if name == "account" {
			w.values["account_id"], w.values["login"], w.values["github_id"] = id.String(), login, strconv.FormatInt(githubID, 10)
		} else {
			w.values[name+"_id"], w.values[name+"_login"] = id.String(), login
		}
		return id
	}
	account := insertUser("account")
	insertUser("other")
	insertUser("deletable")

	login := w.values["login"]
	w.values["org_null"], w.values["org_avatar"] = login+"-null", login+"-avatar"
	if _, err := pool.Exec(ctx, `INSERT INTO user_github_orgs(user_id,org_login,org_id,visible,avatar_url)
		VALUES ($1,$2,1,true,NULL),($1,$3,2,true,'https://example.test/avatar.png')`, account, w.values["org_null"], w.values["org_avatar"]); err != nil {
		t.Fatal(err)
	}

	// One public transcript carrying a tag and two commits, and one private
	// transcript in a project of its own, which the deletion case removes.
	w.values["project"], w.values["private_project"] = strings.Repeat(suffix(), 6)[:64], strings.Repeat(suffix(), 6)[:64]
	// A project with no name evidence is labelled from its hash.
	w.values["project_label"], w.values["private_project_label"] = "project-"+w.values["project"][:12], "project-"+w.values["private_project"][:12]
	insertTranscript := func(name, visibility, project string) uuid.UUID {
		id := uuid.New()
		execRouteBytesSystem(t, `
			INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key, blob_size_bytes,
			                         schema_version, content_hash, wrapped_data_key, encryption_algorithm, key_version, project_hash)
			VALUES ($1, $2, $3, 'golden transcript', $4, 'claude-code', 'model', $5, 2, '0.1.0', $6, 'wrapped', 'aes-256-gcm-random-nonce-v1', 1, $7)`,
			pool, id, account, "golden-"+id.String(), visibility, "transcripts/"+id.String()+".bin", strings.Repeat("7", 64), project)
		transcripts = append(transcripts, id)
		w.values[name+"_id"] = id.String()
		return id
	}
	public := insertTranscript("public", "public", w.values["project"])
	insertTranscript("private", "private", w.values["private_project"])
	w.values["tag"] = "golden-" + suffix()
	var tagID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO tags(name) VALUES($1) RETURNING id`, w.values["tag"]).Scan(&tagID); err != nil {
		t.Fatal(err)
	}
	w.values["tag_id"] = tagID.String()
	if _, err := pool.Exec(ctx, `INSERT INTO transcript_tags(transcript_id,tag_id) VALUES($1,$2)`, public, tagID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO transcript_commits(transcript_id,commit_order,sha,message,author_name,author_email,authored_at,committed_at)
		VALUES ($1,0,$2,'first line','Golden Author','golden@example.test','2026-01-02T03:04:05.678Z','2026-01-02T03:05:06.789Z'),
		       ($1,1,$3,NULL,NULL,NULL,NULL,NULL)`, public, strings.Repeat("a", 40), strings.Repeat("b", 40)); err != nil {
		t.Fatal(err)
	}

	// A collective the account owns, for the App installation handshake.
	group := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO groups(id,name,created_by) VALUES($1,'golden collective',$2)`, group, account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_members(group_id,user_id,role,origin) VALUES($1,$2,'owner','legacy')`, group, account); err != nil {
		t.Fatal(err)
	}
	w.values["group_id"] = group.String()

	// Provider sign-ins: fresh identities the callbacks create accounts for,
	// a CLI session waiting on a sign-in, and two App installations, one on
	// the account's own GitHub identity and one on an account it does not
	// control.
	w.githubSignIn = 8_000_000_000 + mathrand.Int63n(1_000_000_000)
	w.gitlabSignIn = 9_000_000_000 + mathrand.Int63n(1_000_000_000)
	w.values["github_sign_in"], w.values["gitlab_sign_in"] = "golden-"+suffix(), "golden-"+suffix()
	w.values["oauth_state"], w.values["cli_oauth_state"] = suffix()+suffix(), suffix()+suffix()
	if _, err := pool.Exec(ctx, `INSERT INTO cli_auth_sessions(oauth_state,cli_port,cli_state) VALUES($1,43123,'cli-state')`, w.values["cli_oauth_state"]); err != nil {
		t.Fatal(err)
	}
	owned, foreign := 1_000_000_000+mathrand.Int63n(1_000_000_000), 2_000_000_000+mathrand.Int63n(1_000_000_000)
	w.installations = map[int64]int64{owned: w.githubAccountID(t), foreign: 3_000_000_000 + mathrand.Int63n(1_000_000_000)}
	w.values["owned_installation"], w.values["foreign_installation"] = strconv.FormatInt(owned, 10), strconv.FormatInt(foreign, 10)
	w.values["missing_installation"] = strconv.FormatInt(foreign+1, 10)
	return w
}

func keysOf(m map[int64]int64) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// execRouteBytesSystem runs one statement as the system actor under the
// transcript writer fence, as fixture writes and teardown must.
func execRouteBytesSystem(t *testing.T, sql string, pool *pgxpool.Pool, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.actor_id', $1, true), set_config('app.transcript_writer_version', '1', true)", database.SystemActorID); err != nil {
		t.Fatalf("declare the system actor: %v", err)
	}
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// purgeRouteBytesAudit removes the append-only governance rows the fixtures
// produced, last, through the sanctioned maintenance escape.
func purgeRouteBytesAudit(t *testing.T, pool *pgxpool.Pool, ids []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Errorf("begin audit purge: %v", err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL app.audit_maintenance = 'on'"); err != nil {
		t.Errorf("set maintenance escape: %v", err)
		return
	}
	if _, err := tx.Exec(ctx, `DELETE FROM transcript_governance_events_audit WHERE transcript_id = ANY($1::uuid[])`, ids); err != nil {
		t.Errorf("purge audit rows: %v", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		t.Errorf("commit audit purge: %v", err)
	}
}
