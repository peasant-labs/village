//go:build integration

package router

import (
	"context"
	_ "embed"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/redact"
	schemaroute "github.com/peasant-labs/schema/route"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/storage"
)

//go:embed testdata/session_account_lifecycle.yaml
var sessionAccountLifecycleYAML []byte

// sessionLifecycleContentPath is the existing transcript body the fixtures store.
const sessionLifecycleContentPath = "../handler/testdata/contract/current/valid/content.json"

// requiredSessionAccountLifecycleNames is the fixture's name manifest.
var requiredSessionAccountLifecycleNames = []string{
	"api-key-refused-after-account-deletion",
	"api-key-unchanged-after-change",
	"session-bearer-refused-after-account-deletion",
	"session-cookie-refused-after-account-deletion",
	"session-cookie-valid-for-existing-account",
	"session-valid-for-existing-account",
}

type sessionCredential string

const (
	sessionCredentialCookie sessionCredential = "cookie"
	sessionCredentialBearer sessionCredential = "bearer"
	sessionCredentialAPIKey sessionCredential = "api_key"
)

type sessionAccountLifecycleCase struct {
	Name          string            `yaml:"name"`
	Credential    sessionCredential `yaml:"credential"`
	DeleteAccount bool              `yaml:"delete_account"`
	SignedIn      bool              `yaml:"signed_in"`
}

type sessionAccountLifecycleFile struct {
	Cases []sessionAccountLifecycleCase `yaml:"cases"`
}

// sessionLifecycleWorld is the PostgreSQL and object-store state every case
// shares: the production router, and another owner's private and public
// transcripts that outlive every deleted account.
type sessionLifecycleWorld struct {
	pool          *pgxpool.Pool
	blobs         storage.TranscriptBlobStore
	routes        http.Handler
	secret        string
	otherPrivate  uuid.UUID
	otherPublic   uuid.UUID
	missingAnswer *httptest.ResponseRecorder
}

// TestSessionEndsWithItsAccount drives the production router and the real
// DELETE /api/v1/auth/me: a cookie, bearer session or API key issued before the
// account is deleted is refused on auth-required routes and is anonymous on
// optional-auth reads, while the same credentials keep working for an account
// that still exists.
func TestSessionEndsWithItsAccount(t *testing.T) {
	file, err := decodeSingleYAMLDocument[sessionAccountLifecycleFile](sessionAccountLifecycleYAML)
	if err != nil {
		t.Fatalf("load the session account lifecycle fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range file.Cases {
		if present[c.Name] {
			t.Fatalf("the session account lifecycle fixture repeats %q", c.Name)
		}
		present[c.Name] = true
	}
	assertExactNames(t, "session_account_lifecycle", present, requiredSessionAccountLifecycleNames)

	w := newSessionLifecycleWorld(t)
	for _, c := range file.Cases {
		t.Run(c.Name, func(t *testing.T) { runSessionAccountLifecycleCase(t, w, c) })
	}
}

func runSessionAccountLifecycleCase(t *testing.T, w *sessionLifecycleWorld, c sessionAccountLifecycleCase) {
	ctx := context.Background()
	account, login := w.insertUser(t, "session-life")
	own := w.insertTranscript(t, account, "private")
	deleted := false
	t.Cleanup(func() {
		if !deleted {
			w.deleteTranscriptsAsSystem(t, own)
			if _, err := w.pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, account); err != nil {
				t.Errorf("delete the account: %v", err)
			}
		}
		w.purgeAudit(t, own)
	})

	session, err := auth.CreateToken(w.secret, account, login)
	if err != nil {
		t.Fatalf("mint the account's session: %v", err)
	}
	sign := func(r *http.Request) {}
	switch c.Credential {
	case sessionCredentialCookie:
		sign = func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session}) }
	case sessionCredentialBearer:
		sign = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+session) }
	case sessionCredentialAPIKey:
		created := w.serve(t, http.MethodPost, schemaroute.VillageCreateMyAPIKey.Pattern(), `{"label":"session lifecycle"}`, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+session)
		})
		var key struct {
			Key string `json:"key"`
		}
		if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &key) != nil || key.Key == "" {
			t.Fatalf("create the account's API key = %d %s, want 201 with a key", created.Code, created.Body.String())
		}
		sign = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+key.Key) }
	default:
		t.Fatalf("unknown credential %q", c.Credential)
	}

	if c.DeleteAccount {
		gone := w.serve(t, http.MethodDelete, schemaroute.VillageDeleteMyAccount.Pattern(), "", sign)
		if gone.Code != http.StatusOK || strings.TrimSpace(gone.Body.String()) != `{"status":"deleted"}` {
			t.Fatalf("delete the account = %d %s, want 200 deleted", gone.Code, gone.Body.String())
		}
		deleted = true
		var rows int
		if err := w.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id = $1`, account).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("the account row survived deletion: rows=%d err=%v", rows, err)
		}
	}

	keysBefore := w.countKeys(t, account)
	me := w.serve(t, http.MethodGet, schemaroute.VillageGetAuthenticatedVillageUser.Pattern(), "", sign)
	write := w.serve(t, http.MethodPost, schemaroute.VillageCreateMyAPIKey.Pattern(), `{"label":"after"}`, sign)
	ownRead := w.serve(t, http.MethodGet, sessionLifecycleTranscriptPath(t, own), "", sign)
	otherPrivate := w.serve(t, http.MethodGet, sessionLifecycleTranscriptPath(t, w.otherPrivate), "", sign)
	otherPublic := w.serve(t, http.MethodGet, sessionLifecycleTranscriptPath(t, w.otherPublic), "", sign)
	anonymousPublic := w.serve(t, http.MethodGet, sessionLifecycleTranscriptPath(t, w.otherPublic), "", func(*http.Request) {})

	if otherPrivate.Code != http.StatusNotFound || otherPrivate.Body.String() != w.missingAnswer.Body.String() {
		t.Fatalf("another owner's private transcript = %d %s, want the missing-id 404 %s", otherPrivate.Code, otherPrivate.Body.String(), w.missingAnswer.Body.String())
	}
	if anonymousPublic.Code != http.StatusOK || otherPublic.Code != http.StatusOK {
		t.Fatalf("another owner's public transcript = %d (anonymous %d), want 200: %s", otherPublic.Code, anonymousPublic.Code, otherPublic.Body.String())
	}

	if c.SignedIn {
		var who struct {
			ID string `json:"id"`
		}
		if me.Code != http.StatusOK || json.Unmarshal(me.Body.Bytes(), &who) != nil || who.ID != account.String() {
			t.Fatalf("GET /auth/me = %d %s, want 200 naming %s", me.Code, me.Body.String(), account)
		}
		if write.Code != http.StatusCreated || w.countKeys(t, account) != keysBefore+1 {
			t.Fatalf("POST /auth/api-keys = %d %s, want 201 and one more key row", write.Code, write.Body.String())
		}
		if ownRead.Code != http.StatusOK {
			t.Fatalf("the account's own private transcript = %d %s, want 200", ownRead.Code, ownRead.Body.String())
		}
		return
	}

	for _, refused := range []struct {
		route string
		got   *httptest.ResponseRecorder
	}{{"GET /auth/me", me}, {"POST /auth/api-keys", write}} {
		if refused.got.Code != http.StatusUnauthorized || strings.TrimSpace(refused.got.Body.String()) != `{"error":"Authentication required"}` {
			t.Fatalf("%s after deletion = %d %s, want 401 Authentication required", refused.route, refused.got.Code, refused.got.Body.String())
		}
	}
	if keys := w.countKeys(t, account); keys != 0 {
		t.Fatalf("a refused write left %d API key rows for the deleted account", keys)
	}
	if ownRead.Code != http.StatusNotFound || ownRead.Body.String() != w.missingAnswer.Body.String() {
		t.Fatalf("the deleted account's former transcript = %d %s, want the missing-id 404", ownRead.Code, ownRead.Body.String())
	}
	if otherPublic.Body.String() != anonymousPublic.Body.String() {
		t.Fatalf("a deleted account's credential read the public transcript as\n%s\nwant the anonymous answer\n%s", otherPublic.Body.String(), anonymousPublic.Body.String())
	}
}

func newSessionLifecycleWorld(t *testing.T) *sessionLifecycleWorld {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the mounted session lifecycle test")
	}
	ctx := context.Background()
	poolConfig, err := database.PoolConfig(databaseURL)
	if err != nil {
		t.Fatalf("build the pool config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Skipf("cannot reach the test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	objects, err := storage.NewS3ObjectStore(&config.Config{S3Endpoint: os.Getenv("TEST_S3_ENDPOINT"), S3Bucket: os.Getenv("TEST_S3_BUCKET"), S3AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"), S3SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), S3UsePathStyle: true})
	if err != nil {
		t.Fatalf("construct test object store: %v", err)
	}
	ring, err := config.ParseTranscriptKeyring("1", `{"1":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}`)
	if err != nil {
		t.Fatalf("construct test keyring: %v", err)
	}
	blobs, err := storage.NewEncryptedTranscriptStore(objects, ring)
	if err != nil {
		t.Fatalf("construct encrypted store: %v", err)
	}
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct the title pipeline: %v", err)
	}
	cfg := &config.Config{JWTSecret: "session-account-lifecycle-route", FrontendURL: "https://app.example.com"}
	w := &sessionLifecycleWorld{pool: pool, blobs: blobs, routes: New(cfg, pool, blobs, titles), secret: cfg.JWTSecret}

	other, _ := w.insertUser(t, "session-other")
	w.otherPrivate = w.insertTranscript(t, other, "private")
	w.otherPublic = w.insertTranscript(t, other, "public")
	t.Cleanup(func() {
		w.deleteTranscriptsAsSystem(t, w.otherPrivate, w.otherPublic)
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, other); err != nil {
			t.Errorf("delete the other owner: %v", err)
		}
		w.purgeAudit(t, w.otherPrivate, w.otherPublic)
	})
	w.missingAnswer = w.serve(t, http.MethodGet, sessionLifecycleTranscriptPath(t, uuid.New()), "", func(*http.Request) {})
	if w.missingAnswer.Code != http.StatusNotFound {
		t.Fatalf("a missing transcript id = %d %s, want 404", w.missingAnswer.Code, w.missingAnswer.Body.String())
	}
	return w
}

func (w *sessionLifecycleWorld) serve(t *testing.T, method, path, body string, sign func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	sign(r)
	rec := httptest.NewRecorder()
	w.routes.ServeHTTP(rec, r)
	return rec
}

func sessionLifecycleTranscriptPath(t *testing.T, id uuid.UUID) string {
	t.Helper()
	path, err := schemaroute.VillageGetTranscriptMetadataPath(id.String())
	if err != nil {
		t.Fatalf("build the transcript path: %v", err)
	}
	return path
}

func (w *sessionLifecycleWorld) insertUser(t *testing.T, prefix string) (uuid.UUID, string) {
	t.Helper()
	githubID := 8_000_000_000 + rand.Int63n(1_000_000_000)
	login := prefix + "-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	var id uuid.UUID
	if err := w.pool.QueryRow(context.Background(), `
		INSERT INTO users (github_id, github_username, provider_user_id) VALUES ($1, $2, $1::bigint::text) RETURNING id
	`, githubID, login).Scan(&id); err != nil {
		t.Fatalf("insert user %s: %v", login, err)
	}
	return id, login
}

func (w *sessionLifecycleWorld) insertTranscript(t *testing.T, owner uuid.UUID, visibility string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	content, err := os.ReadFile(sessionLifecycleContentPath)
	if err != nil {
		t.Fatalf("read existing transcript fixture: %v", err)
	}
	id := uuid.New()
	descriptor, identity, err := w.blobs.Write(ctx, id, content)
	if err != nil {
		t.Fatalf("write encrypted transcript fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := w.blobs.Delete(context.Background(), descriptor); err != nil {
			t.Errorf("delete encrypted transcript fixture: %v", err)
		}
	})
	w.execAsSystem(t, `
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key, blob_size_bytes,
		                         schema_version, content_hash, wrapped_data_key, encryption_algorithm, key_version, project_hash)
		VALUES ($1, $2, $3, 'session lifecycle', $4, 'claude-code', 'model', $5, $6, '0.1.0', $7, $8, $9, $10, $11)
	`, id, owner, "session-life-"+id.String(), visibility, string(descriptor.ObjectKey()), identity.PlaintextSize(), string(identity.Hash()),
		descriptor.WrappedDEK(), string(descriptor.Algorithm()), descriptor.KeyVersion(), strings.Repeat("7", 64))
	return id
}

func (w *sessionLifecycleWorld) countKeys(t *testing.T, account uuid.UUID) int {
	t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM api_keys WHERE user_id = $1`, account).Scan(&n); err != nil {
		t.Fatalf("count API keys: %v", err)
	}
	return n
}

func (w *sessionLifecycleWorld) execAsSystem(t *testing.T, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := w.pool.Begin(ctx)
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

func (w *sessionLifecycleWorld) deleteTranscriptsAsSystem(t *testing.T, ids ...uuid.UUID) {
	t.Helper()
	w.execAsSystem(t, `DELETE FROM transcripts WHERE id = ANY($1::uuid[])`, ids)
}

// purgeAudit removes the append-only governance rows the fixtures produced,
// last, through the sanctioned maintenance escape.
func (w *sessionLifecycleWorld) purgeAudit(t *testing.T, ids ...uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tx, err := w.pool.Begin(ctx)
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
