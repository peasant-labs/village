//go:build integration

package router

import (
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/redact"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/database"
)

// TestTranscriptPullRequestsRouteReadsTheSession drives the production router
// with a signed-in owner and with no session, on a private transcript. The
// anonymous route-auth rows cannot tell an optional session from none at all;
// this can: only a route that reads the session answers the owner 200, and it
// still answers a caller with no session 404.
func TestTranscriptPullRequestsRouteReadsTheSession(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the mounted router session test")
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

	githubID := 8_000_000_000 + rand.Int63n(1_000_000_000)
	login := "route-pulls-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id) VALUES ($1, $2, $1::bigint::text) RETURNING id
	`, githubID, login).Scan(&owner); err != nil {
		t.Fatalf("insert the owner: %v", err)
	}
	transcript := uuid.New()
	asSystem := func(sql string, args ...any) {
		t.Helper()
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
	t.Cleanup(func() {
		asSystem(`DELETE FROM transcripts WHERE id = $1`, transcript)
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, owner); err != nil {
			t.Errorf("delete the owner: %v", err)
		}
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
		if _, err := tx.Exec(ctx, `DELETE FROM transcript_governance_events_audit WHERE transcript_id = $1`, transcript); err != nil {
			t.Errorf("purge audit rows: %v", err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			t.Errorf("commit audit purge: %v", err)
		}
	})
	localID := "route-pulls-" + transcript.String()
	asSystem(`
		INSERT INTO transcripts (id, owner_id, local_id, title, visibility, model_provider, model_name, blob_key, blob_size_bytes,
		                         schema_version, content_hash, wrapped_data_key, encryption_algorithm, key_version, project_hash)
		VALUES ($1, $2, $3, 'route pulls', 'private', 'claude-code', 'model', $4, 1, '0.1.0', $5, $6, 'aes-256-gcm-random-nonce-v1', 1, $7)
	`, transcript, owner, localID, "blob/"+localID, schema.ComputeTranscriptHash([]byte(localID)),
		[]byte("fixture-wrapped-data-key"), strings.Repeat("7", 64))

	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatalf("construct the title pipeline: %v", err)
	}
	cfg := &config.Config{JWTSecret: "transcript-pulls-route", FrontendURL: "https://app.example.com"}
	routes := New(cfg, pool, nil, titles)
	token, err := auth.CreateToken(cfg.JWTSecret, owner, login)
	if err != nil {
		t.Fatalf("mint the owner's session: %v", err)
	}
	get := func(bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/transcripts/"+transcript.String()+"/pulls", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, req)
		return rec
	}
	if owned := get(token); owned.Code != http.StatusOK || strings.TrimSpace(owned.Body.String()) != `{"pull_requests":[]}` {
		t.Fatalf("the owner's read of their private transcript's pull requests = %d %s, want 200 with an empty list", owned.Code, owned.Body.String())
	}
	if anonymous := get(""); anonymous.Code != http.StatusNotFound {
		t.Fatalf("an anonymous read of a private transcript's pull requests = %d, want 404", anonymous.Code)
	}
}
