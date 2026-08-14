// Command village-setup-demo is a DEV / DEMO-ONLY helper.
//
// ████████████████████████████████████████████████████████████████████████████
// ██  DEV / DEMO ONLY — CANNOT TOUCH A REMOTE OR PRODUCTION DATABASE.        ██
// ██                                                                        ██
// ██  This tool writes a fixed demo user + a freshly-minted API key DIRECTLY ██
// ██  into a LOCAL village database, bypassing OAuth, so the local end-to-end ██
// ██  harness can authenticate a real `peasant village push`.                 ██
// ██                                                                        ██
// ██  Two independent fences make a prod mistake impossible:                  ██
// ██   1. it REFUSES unless DATABASE_URL's host is localhost / 127.0.0.1 /   ██
// ██      ::1 (host parsed via net/url — a userinfo spoof like                ██
// ██      `localhost@prod` resolves to host `prod` and is rejected);          ██
// ██   2. it requires the explicit --local flag.                             ██
// ██                                                                        ██
// ██  It is a SEPARATE main package — Go cannot import a main package, so it  ██
// ██  is structurally excluded from the server build.                        ██
// ████████████████████████████████████████████████████████████████████████████
//
// Usage:
//
//	DATABASE_URL=postgres://test:test@localhost:5432/village_test?sslmode=disable \
//	VILLAGE_URL=http://localhost:8080 \
//	  go run ./cmd/village-setup-demo --local > credentials.json
//
// It best-effort ensures the schema is migrated, idempotently upserts a demo user
// (plain SQL — github_id + github_username + provider_user_id are all NOT NULL;
// provider defaults to 'github'), mints an API key via the REAL
// auth.GenerateAPIKey() so the format + sha256 hash match the production auth
// path, idempotently (re)inserts the api_keys row, and writes a peasant
// credentials.json (api_key/key_id/user_id/username/village_url) to stdout.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database"
)

// Fixed identity for the demo user. github_id is the unique conflict key; it is
// a synthetic surrogate (the demo never touches real GitHub). Keeping it fixed
// makes the setup idempotent across runs.
const (
	demoGitHubID = 990001
	demoUsername = "demo-user"
	demoKeyLabel = "demo-e2e"
	setupTimeout = 20 * time.Second
)

// localDBHosts is the set of hosts a demo DATABASE_URL may point at. Anything
// else is refused so the tool can never mint against a remote/prod database.
var localDBHosts = map[string]bool{
	"localhost": true,
	"127.0.0.1": true,
	"::1":       true,
}

// demoCredentials is the peasant credentials.json shape (mirrors peasant's
// internal/auth.Credentials, which is not importable across repos). The peasant
// client reads exactly these JSON keys.
type demoCredentials struct {
	APIKey     string `json:"api_key"`
	KeyID      string `json:"key_id"`
	UserID     string `json:"user_id"`
	Username   string `json:"username"`
	VillageURL string `json:"village_url"`
}

func main() {
	local := flag.Bool("local", false,
		"REQUIRED: confirm this is a LOCAL dev/demo run (the tool writes a demo user + API key directly to DATABASE_URL)")
	flag.Parse()

	if !*local {
		fmt.Fprintln(os.Stderr,
			"village-setup-demo is DEV/DEMO ONLY and refuses to run without --local.\n"+
				"It writes a demo user + API key directly into DATABASE_URL, bypassing OAuth.\n"+
				"Pass --local to confirm you are targeting a local throwaway database.")
		os.Exit(2)
	}

	dbURL := os.Getenv("DATABASE_URL")
	villageURL := os.Getenv("VILLAGE_URL")
	if dbURL == "" || villageURL == "" {
		fmt.Fprintln(os.Stderr, "village-setup-demo: DATABASE_URL and VILLAGE_URL must both be set")
		os.Exit(2)
	}

	if err := validateLocalDatabaseURL(dbURL); err != nil {
		fmt.Fprintf(os.Stderr, "village-setup-demo: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), setupTimeout)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "village-setup-demo: connect to database: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Best-effort ensure-migrated: the e2e server normally migrates on startup, so
	// this is usually a no-op. RunMigrations is idempotent; a failure here is not
	// fatal (the schema may already be present and owned by the server).
	if err := database.RunMigrations(pool); err != nil {
		fmt.Fprintf(os.Stderr, "village-setup-demo: warning: ensure-migrated: %v\n", err)
	}

	creds, err := setupDemo(ctx, pool, villageURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "village-setup-demo: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(creds); err != nil {
		fmt.Fprintf(os.Stderr, "village-setup-demo: encode credentials: %v\n", err)
		os.Exit(1)
	}
}

// validateLocalDatabaseURL refuses any DATABASE_URL whose HOST is not a local
// loopback address. The host is extracted via net/url.Hostname(), NOT a substring
// match: that strips both the port and any userinfo, so a spoof such as
// "postgres://localhost@prod-db/app" resolves to host "prod-db" and is rejected.
func validateLocalDatabaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("DATABASE_URL is not a parseable URL (%v); refusing to run. "+
			"village-setup-demo only accepts a local postgres URL like "+
			"postgres://user:pass@localhost:5432/db", err)
	}
	host := u.Hostname()
	if !localDBHosts[host] {
		return fmt.Errorf("refusing to run against non-local DATABASE_URL host %q "+
			"(parsed from %q). village-setup-demo is dev/demo-only and may only target "+
			"localhost, 127.0.0.1, or ::1 — never a remote or production database. "+
			"Point DATABASE_URL at a local throwaway Postgres and retry", host, raw)
	}
	return nil
}

// setupDemo idempotently provisions the demo user + API key and returns the
// credentials the peasant client needs. It is the testable core of the command
// (exercised against a real Postgres by the integration test).
//
// Idempotency:
//   - the user is upserted on github_id (ON CONFLICT DO UPDATE) so repeated runs
//     resolve to the same user row;
//   - the API key is random per run (we cannot recover a prior plaintext), so all
//     existing keys for the demo user are deleted before inserting the fresh one
//     — repeated runs leave exactly one valid key matching the emitted creds.
func setupDemo(ctx context.Context, pool *pgxpool.Pool, villageURL string) (*demoCredentials, error) {
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider_user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (github_id) DO UPDATE SET github_username = EXCLUDED.github_username
		RETURNING id::text
	`, int64(demoGitHubID), demoUsername, strconv.Itoa(demoGitHubID)).Scan(&userID); err != nil {
		return nil, fmt.Errorf("upsert demo user: %w", err)
	}

	// Mint via the REAL production auth path so the key format + sha256 hash are
	// exactly what the village's auth middleware expects.
	plaintext, hash, prefix, err := auth.GenerateAPIKey()
	if err != nil {
		return nil, fmt.Errorf("generate api key: %w", err)
	}

	// Clear any prior demo keys so re-running does not accumulate rows.
	if _, err := pool.Exec(ctx, `DELETE FROM api_keys WHERE user_id = $1`, userID); err != nil {
		return nil, fmt.Errorf("clear prior demo api keys: %w", err)
	}

	var keyID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO api_keys (user_id, key_hash, key_prefix, label)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text
	`, userID, hash, prefix, demoKeyLabel).Scan(&keyID); err != nil {
		return nil, fmt.Errorf("insert demo api key: %w", err)
	}

	return &demoCredentials{
		APIKey:     plaintext,
		KeyID:      keyID,
		UserID:     userID,
		Username:   demoUsername,
		VillageURL: villageURL,
	}, nil
}
