//go:build integration

package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// TestGitHubSignInHandleAgainstPostgres runs the sign-in-handle corpus through
// the REAL mounted callback and the REAL queries on migrated PostgreSQL, with
// only GitHub faked at the transport. The unit corpus decides over a mocked
// table; this run observes the real rows: a returning account's upsert keeps
// its handle and chosen flag, and a free login's confirm lands on the real
// row. The CLI cases store a real session row and exchange it through the real
// endpoint. Unit-only cases (an injected failure or a concurrent rename) are
// left to the unit corpus. The confirm's guard is pinned on its own by
// TestConfirmOwnHandleGuardAgainstPostgres, because no sign-in reaches a row
// the guard refuses.
func TestGitHubSignInHandleAgainstPostgres(t *testing.T) {
	corpus := loadSignInHandleFixtures(t)
	pool := govTestPool(t)
	defer pool.Close()

	for _, c := range corpus.Cases {
		if c.unitOnly() {
			// Not a skip: the integration gate rejects skipped tests, and these
			// cases inject what a real database cannot be made to do on cue.
			continue
		}
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

			githubID := randomGitHubID(t)
			state := fmt.Sprintf("sign-in-handle-%d", githubID)
			var created []pgtype.UUID
			t.Cleanup(func() {
				if _, err := pool.Exec(ctx, `DELETE FROM cli_auth_sessions WHERE oauth_state = $1`, state); err != nil {
					t.Errorf("delete cli session: %v", err)
				}
				var self pgtype.UUID
				if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE github_id = $1`, githubID).Scan(&self); err == nil {
					created = append(created, self)
				}
				cleanupOwners(t, ctx, pool, created...)
			})

			for _, handle := range c.TakenByOthers {
				created = append(created, insertSignInAccount(t, ctx, pool, randomGitHubID(t), handle, true))
			}
			if c.Returning != nil {
				insertSignInAccount(t, ctx, pool, githubID, c.Returning.Handle, c.Returning.Chosen)
			}
			if c.Flow == "cli" {
				if _, err := h.queries.InsertCLISession(ctx, sqlc.InsertCLISessionParams{OauthState: state, CliPort: 51234, CliState: "cli-state"}); err != nil {
					t.Fatal(err)
				}
			}

			got := signInThroughCallback(t, h, fakeGitHubOAuth{id: githubID, login: c.Login}, state, c.Flow)

			if got.me.GithubUsername != c.ExpectHandle || got.me.UsernameChosen != c.ExpectChosen {
				t.Fatalf("/auth/me = {handle %q, chosen %v}, want {handle %q, chosen %v}",
					got.me.GithubUsername, got.me.UsernameChosen, c.ExpectHandle, c.ExpectChosen)
			}
			if got.sessionHandle != c.ExpectHandle {
				t.Fatalf("%s session names %q, want the village handle %q (login %q)", c.Flow, got.sessionHandle, c.ExpectHandle, c.Login)
			}
			// The row itself, not only the handler's answer.
			var handle string
			var chosen bool
			if err := pool.QueryRow(ctx, `SELECT github_username, username_chosen FROM users WHERE github_id = $1`, githubID).Scan(&handle, &chosen); err != nil {
				t.Fatalf("read the account back: %v", err)
			}
			if handle != c.ExpectHandle || chosen != c.ExpectChosen {
				t.Fatalf("stored account = {handle %q, chosen %v}, want {handle %q, chosen %v}", handle, chosen, c.ExpectHandle, c.ExpectChosen)
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

//go:embed testdata/confirm-own-handle.yaml
var confirmOwnHandleFixtures []byte

type confirmOwnHandleCase struct {
	Name            string `yaml:"name"`
	Why             string `yaml:"why"`
	StoredHandle    string `yaml:"stored_handle"`
	StoredChosen    bool   `yaml:"stored_chosen"`
	ConfirmWith     string `yaml:"confirm_with"`
	ExpectConfirmed bool   `yaml:"expect_confirmed"`
}

type confirmOwnHandleCorpus struct {
	Required []string               `yaml:"required_names"`
	Cases    []confirmOwnHandleCase `yaml:"cases"`
}

func loadConfirmOwnHandleFixtures(t *testing.T) confirmOwnHandleCorpus {
	t.Helper()
	var corpus confirmOwnHandleCorpus
	d := yaml.NewDecoder(bytes.NewReader(confirmOwnHandleFixtures))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	var sawConfirmed, sawRefused bool
	for _, c := range corpus.Cases {
		if c.Name == "" || names[c.Name] || strings.TrimSpace(c.Why) == "" {
			t.Fatalf("confirm-own-handle case %q is unnamed, duplicated or has no why", c.Name)
		}
		names[c.Name] = true
		// The guard's rule, derived from the case's own data: confirmed only
		// on the approved handle and only while unchosen.
		if want := c.StoredHandle == c.ConfirmWith && !c.StoredChosen; c.ExpectConfirmed != want {
			t.Fatalf("confirm-own-handle case %q expects confirmed=%v, but its rows entail %v", c.Name, c.ExpectConfirmed, want)
		}
		sawConfirmed = sawConfirmed || c.ExpectConfirmed
		sawRefused = sawRefused || !c.ExpectConfirmed
	}
	for _, required := range corpus.Required {
		if !names[required] {
			t.Fatalf("required-name manifest names a missing case %q", required)
		}
	}
	if len(names) != len(corpus.Required) {
		t.Fatalf("required-name manifest covers %d of %d cases; every case must be named", len(corpus.Required), len(names))
	}
	if !sawConfirmed || !sawRefused {
		t.Fatal("confirm-own-handle corpus must hold a confirmed case and a refused one")
	}
	return corpus
}

// TestConfirmOwnHandleGuardAgainstPostgres runs the REAL guarded update on
// real rows, including the two the guard must refuse, which no sign-in
// reaches: a handle that changed since it was read, and a row already chosen.
func TestConfirmOwnHandleGuardAgainstPostgres(t *testing.T) {
	corpus := loadConfirmOwnHandleFixtures(t)
	pool := govTestPool(t)
	defer pool.Close()
	q := sqlc.New(pool)

	for _, c := range corpus.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			prefix := fmt.Sprintf("g%d-", randomGitHubID(t)%1_000_000)
			id := insertSignInAccount(t, ctx, pool, randomGitHubID(t), prefix+c.StoredHandle, c.StoredChosen)
			t.Cleanup(func() { cleanupOwners(t, ctx, pool, id) })

			var before sqlc.User
			if err := pool.QueryRow(ctx, `SELECT github_username, username_chosen, updated_at FROM users WHERE id = $1`, id).
				Scan(&before.GithubUsername, &before.UsernameChosen, &before.UpdatedAt); err != nil {
				t.Fatal(err)
			}

			got, err := q.ConfirmOwnHandle(ctx, sqlc.ConfirmOwnHandleParams{ID: id, GithubUsername: prefix + c.ConfirmWith})

			var after sqlc.User
			if err := pool.QueryRow(ctx, `SELECT github_username, username_chosen, updated_at FROM users WHERE id = $1`, id).
				Scan(&after.GithubUsername, &after.UsernameChosen, &after.UpdatedAt); err != nil {
				t.Fatal(err)
			}
			if c.ExpectConfirmed {
				if err != nil {
					t.Fatalf("ConfirmOwnHandle: %v", err)
				}
				if !got.UsernameChosen || !after.UsernameChosen || after.GithubUsername != before.GithubUsername {
					t.Fatalf("row after confirm = {handle %q, chosen %v}, want {handle %q, chosen true}", after.GithubUsername, after.UsernameChosen, before.GithubUsername)
				}
				return
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("ConfirmOwnHandle = (%+v, %v), want pgx.ErrNoRows", got, err)
			}
			if after != before {
				t.Fatalf("a refused confirm changed the row: before %+v, after %+v", before, after)
			}
		})
	}
}
