//go:build integration

package main

// Integration test for village-setup-demo: real-auth key mint, the GetAPIKeyByHash
// round-trip (the minted key authenticates through the same lookup the village
// auth middleware uses), and idempotent re-runs.
//
// Requires a running PostgreSQL instance. Run with:
//
//	TEST_DATABASE_URL="postgres://test:test@localhost:55432/village_test?sslmode=disable" \
//	  go test -tags=integration ./cmd/village-setup-demo/...
//
// The demo user (and its keys, via FK cascade) is deleted on completion.

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func setupDemoTestDatabaseURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return "postgres://test:test@localhost:55432/village_test?sslmode=disable"
}

func pgUUIDString(t *testing.T, id [16]byte) string {
	t.Helper()
	return uuid.UUID(id).String()
}

func TestSetupDemo_RealAuthKeyMint_RoundTripAndIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, setupDemoTestDatabaseURL(t))
	if err != nil {
		t.Skipf("cannot create pool (%v); set TEST_DATABASE_URL", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("cannot reach test database (%v); set TEST_DATABASE_URL", err)
	}
	if err := database.RunMigrations(pool); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(ctx, "DELETE FROM users WHERE github_id = $1", int64(demoGitHubID)); err != nil {
			t.Errorf("cleanup demo user: %v", err)
		}
	}()

	const villageURL = "http://localhost:8080"
	queries := sqlc.New(pool)

	// First run.
	creds1, err := setupDemo(ctx, pool, villageURL)
	if err != nil {
		t.Fatalf("setupDemo run1: %v", err)
	}
	if !auth.IsAPIKey(creds1.APIKey) {
		t.Errorf("minted key %q lacks the production prefix %q", creds1.APIKey, auth.APIKeyPrefix)
	}
	if creds1.UserID == "" || creds1.KeyID == "" {
		t.Fatalf("creds missing ids: %+v", creds1)
	}
	if creds1.Username != demoUsername || creds1.VillageURL != villageURL {
		t.Errorf("creds fields wrong: %+v", creds1)
	}

	// THE KEY AUTHENTICATES: GetAPIKeyByHash is exactly the lookup the auth
	// middleware performs. Hashing the emitted plaintext must resolve the row, and
	// that row must point at the demo user + the emitted key id.
	row, err := queries.GetAPIKeyByHash(ctx, auth.HashAPIKey(creds1.APIKey))
	if err != nil {
		t.Fatalf("GetAPIKeyByHash(minted key): %v — the demo key would not authenticate", err)
	}
	if got := pgUUIDString(t, row.ID.Bytes); got != creds1.KeyID {
		t.Errorf("authenticated key id %q != emitted key_id %q", got, creds1.KeyID)
	}
	if got := pgUUIDString(t, row.UserID.Bytes); got != creds1.UserID {
		t.Errorf("authenticated user id %q != emitted user_id %q", got, creds1.UserID)
	}
	if row.GithubUsername != demoUsername {
		t.Errorf("authenticated username %q != %q", row.GithubUsername, demoUsername)
	}

	// Second run: idempotent user, exactly one key, fresh value that still
	// authenticates; the prior key is gone.
	creds2, err := setupDemo(ctx, pool, villageURL)
	if err != nil {
		t.Fatalf("setupDemo run2: %v", err)
	}
	if creds2.UserID != creds1.UserID {
		t.Errorf("user not idempotent: run1 %q, run2 %q", creds1.UserID, creds2.UserID)
	}

	var keyCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM api_keys WHERE user_id = $1", creds1.UserID,
	).Scan(&keyCount); err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if keyCount != 1 {
		t.Errorf("after two runs: got %d api_keys for the demo user, want exactly 1 (idempotent)", keyCount)
	}

	if _, err := queries.GetAPIKeyByHash(ctx, auth.HashAPIKey(creds2.APIKey)); err != nil {
		t.Errorf("run2 key does not authenticate: %v", err)
	}
	if _, err := queries.GetAPIKeyByHash(ctx, auth.HashAPIKey(creds1.APIKey)); err == nil {
		t.Errorf("run1 key still authenticates after re-run; old key should have been cleared")
	}
}
