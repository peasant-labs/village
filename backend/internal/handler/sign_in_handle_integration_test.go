//go:build integration

package handler

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// TestGitHubSignInHandleAgainstPostgres runs the sign-in-handle corpus through
// the REAL queries on migrated PostgreSQL. The unit corpus proves the decision
// over a mocked table; this one proves the two pieces of SQL behavior it rests
// on: confirming a handle rewrites an account to the handle it already holds,
// which the case-insensitive unique index must accept, and a returning
// account's upsert preserves its handle and chosen flag. The CLI cases also
// store and exchange a real session row.
func TestGitHubSignInHandleAgainstPostgres(t *testing.T) {
	corpus := loadSignInHandleFixtures(t)
	pool := govTestPool(t)
	defer pool.Close()

	for _, c := range corpus.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			h := &Handler{pool: pool, queries: sqlc.New(pool), cfg: minimalConfig()}

			// Fail loudly on a leftover account rather than reading it as a
			// collision this case did not ask for.
			for _, handle := range append([]string{c.ExpectHandle, strings.ToLower(c.Login)}, c.TakenByOthers...) {
				var n int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(github_username) = lower($1)`, handle).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != 0 {
					t.Fatalf("the test database already holds an account with handle %q; clean it before running this suite", handle)
				}
			}

			var created []pgtype.UUID
			var oauthStates []string
			t.Cleanup(func() {
				if len(oauthStates) > 0 {
					if _, err := pool.Exec(ctx, `DELETE FROM cli_auth_sessions WHERE oauth_state = ANY($1)`, oauthStates); err != nil {
						t.Errorf("delete cli sessions: %v", err)
					}
				}
				cleanupOwners(t, ctx, pool, created...)
			})

			for _, handle := range c.TakenByOthers {
				created = append(created, insertSignInAccount(t, ctx, pool, randomGitHubID(t), handle, true))
			}
			githubID := randomGitHubID(t)
			if c.Returning != nil {
				created = append(created, insertSignInAccount(t, ctx, pool, githubID, c.Returning.Handle, c.Returning.Chosen))
			}

			user, err := h.signInGitHubUser(ctx, githubProfile{ID: githubID, Login: c.Login})
			if err != nil {
				t.Fatalf("signInGitHubUser: %v", err)
			}
			if c.Returning == nil {
				created = append(created, user.ID)
			}

			var handle string
			var chosen bool
			if err := pool.QueryRow(ctx, `SELECT github_username, username_chosen FROM users WHERE github_id = $1`, githubID).Scan(&handle, &chosen); err != nil {
				t.Fatalf("read the account back: %v", err)
			}
			if handle != c.ExpectHandle || chosen != c.ExpectChosen {
				t.Fatalf("stored account = {handle %q, chosen %v}, want {handle %q, chosen %v}", handle, chosen, c.ExpectHandle, c.ExpectChosen)
			}

			if c.Flow != "cli" {
				return
			}
			state := fmt.Sprintf("sign-in-handle-%d", githubID)
			oauthStates = append(oauthStates, state)
			if _, err := h.queries.InsertCLISession(ctx, sqlc.InsertCLISessionParams{OauthState: state, CliPort: 51234, CliState: "cli-state"}); err != nil {
				t.Fatal(err)
			}
			session, err := h.queries.GetCLISessionByState(ctx, state)
			if err != nil {
				t.Fatal(err)
			}
			callback := httptest.NewRecorder()
			h.handleCLICallback(callback, httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback", nil), &user, session)
			if callback.Code != http.StatusTemporaryRedirect {
				t.Fatalf("CLI callback status = %d (%s)", callback.Code, callback.Body.String())
			}
			var stored string
			if err := pool.QueryRow(ctx, `SELECT username FROM cli_auth_sessions WHERE oauth_state = $1`, state).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != c.ExpectHandle {
				t.Fatalf("CLI session username = %q, want the village handle %q (login %q)", stored, c.ExpectHandle, c.Login)
			}
		})
	}
}

func insertSignInAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, githubID int64, handle string, chosen bool) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (github_id, github_username, provider, provider_user_id, username_chosen)
		VALUES ($1, $2, 'github', $1::bigint::text, $3) RETURNING id
	`, githubID, handle, chosen).Scan(&id); err != nil {
		t.Fatalf("insert account %s: %v", handle, err)
	}
	return id
}

// randomGitHubID is a positive id far from any fixture's, so a parallel
// package's accounts cannot collide on the unique github_id.
func randomGitHubID(t *testing.T) int64 {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return int64(binary.BigEndian.Uint64(b[:])>>2) + 1<<40
}
