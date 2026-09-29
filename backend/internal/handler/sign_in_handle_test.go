package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/sign-in-handle.yaml
var signInHandleFixtures []byte

type signInHandleReturning struct {
	Handle string `yaml:"handle"`
	Chosen bool   `yaml:"chosen"`
}

type signInHandleCase struct {
	Name          string                 `yaml:"name"`
	Why           string                 `yaml:"why"`
	Flow          string                 `yaml:"flow"`
	Login         string                 `yaml:"login"`
	TakenByOthers []string               `yaml:"taken_by_others"`
	Returning     *signInHandleReturning `yaml:"returning"`
	ExpectHandle  string                 `yaml:"expect_handle"`
	ExpectChosen  bool                   `yaml:"expect_chosen"`
}

type signInHandleCorpus struct {
	Required []string           `yaml:"required_names"`
	Cases    []signInHandleCase `yaml:"cases"`
}

func loadSignInHandleFixtures(t *testing.T) signInHandleCorpus {
	t.Helper()
	var corpus signInHandleCorpus
	d := yaml.NewDecoder(bytes.NewReader(signInHandleFixtures))
	d.KnownFields(true)
	if err := d.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Required) == 0 {
		t.Fatal("sign-in-handle fixture has no required-name manifest")
	}
	names := map[string]bool{}
	for _, c := range corpus.Cases {
		if c.Name == "" || names[c.Name] {
			t.Fatalf("duplicate or empty sign-in-handle case %q", c.Name)
		}
		names[c.Name] = true
		if strings.TrimSpace(c.Why) == "" {
			t.Fatalf("sign-in-handle case %q has no why", c.Name)
		}
		if c.Flow != "web" && c.Flow != "cli" {
			t.Fatalf("sign-in-handle case %q: flow %q is not web or cli", c.Name, c.Flow)
		}
	}
	for _, required := range corpus.Required {
		if !names[required] {
			t.Fatalf("required-name manifest names a missing case %q", required)
		}
	}
	if len(names) != len(corpus.Required) {
		t.Fatalf("required-name manifest covers %d of %d cases; every case must be named", len(corpus.Required), len(names))
	}

	// The corpus must be able to fail. A corpus with no collision could not
	// tell a sign-in that confirms every handle from one that follows the rule,
	// and one with no CLI case whose handle differs from the login could not
	// tell a session that stores the login from one that stores the handle.
	var sawChosen, sawUnchosen, sawCLIHandleNotLogin bool
	for _, c := range corpus.Cases {
		sawChosen = sawChosen || c.ExpectChosen
		sawUnchosen = sawUnchosen || !c.ExpectChosen
		sawCLIHandleNotLogin = sawCLIHandleNotLogin || (c.Flow == "cli" && c.ExpectHandle != c.Login)
	}
	if !sawChosen || !sawUnchosen {
		t.Fatal("sign-in-handle corpus must hold a case that skips the handle step and one that opens it")
	}
	if !sawCLIHandleNotLogin {
		t.Fatal("sign-in-handle corpus must hold a cli case whose handle is not the login it signed in with")
	}
	return corpus
}

// signInUsers is the slice of the users table a GitHub sign-in touches: the
// handles other accounts hold, and this GitHub account's own row once it
// exists. It enforces the one constraint these queries rely on, the
// case-insensitive unique handle.
type signInUsers struct {
	others      map[string]bool
	self        *sqlc.User
	setUsername []sqlc.SetUsernameParams
}

func handleTaken() error { return &pgconn.PgError{Code: uniqueViolation} }

func (s *signInUsers) querier(t *testing.T, githubID int64) *mockQuerier {
	return &mockQuerier{
		getUserByUsername: func(_ context.Context, handle string) (sqlc.User, error) {
			if s.others[strings.ToLower(handle)] {
				return sqlc.User{GithubUsername: strings.ToLower(handle)}, nil
			}
			if s.self != nil && strings.EqualFold(s.self.GithubUsername, handle) {
				return *s.self, nil
			}
			return sqlc.User{}, pgx.ErrNoRows
		},
		upsertUser: func(_ context.Context, arg sqlc.UpsertUserParams) (sqlc.User, error) {
			if arg.GithubID != githubID {
				t.Errorf("UpsertUser github_id = %d, want %d", arg.GithubID, githubID)
			}
			if s.self != nil {
				// ON CONFLICT (github_id): the handle and the chosen flag are
				// preserved; only the provider fields refresh.
				s.self.ProviderUsername = arg.ProviderUsername
				return *s.self, nil
			}
			if s.others[strings.ToLower(arg.GithubUsername)] {
				return sqlc.User{}, handleTaken()
			}
			s.self = &sqlc.User{
				ID:               pgUUIDFrom(uuid.New()),
				GithubID:         arg.GithubID,
				GithubUsername:   arg.GithubUsername,
				ProviderUsername: arg.ProviderUsername,
				Provider:         "github",
				UsernameChosen:   false,
			}
			return *s.self, nil
		},
		setUsername: func(_ context.Context, arg sqlc.SetUsernameParams) (sqlc.User, error) {
			s.setUsername = append(s.setUsername, arg)
			if s.self == nil || arg.ID != s.self.ID {
				t.Fatalf("SetUsername for an account the sign-in did not upsert")
			}
			if s.others[strings.ToLower(arg.GithubUsername)] {
				return sqlc.User{}, handleTaken()
			}
			s.self.GithubUsername = arg.GithubUsername
			s.self.UsernameChosen = true
			return *s.self, nil
		},
		getUserByID: func(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
			if s.self == nil || id != s.self.ID {
				return sqlc.User{}, pgx.ErrNoRows
			}
			return *s.self, nil
		},
	}
}

// meAnswer is the part of `GET /auth/me` the web app's handle gate reads.
type meAnswer struct {
	GithubUsername string `json:"github_username"`
	UsernameChosen bool   `json:"username_chosen"`
}

func TestGitHubSignInHandle(t *testing.T) {
	corpus := loadSignInHandleFixtures(t)
	const githubID int64 = 4242
	for _, c := range corpus.Cases {
		t.Run(c.Name, func(t *testing.T) {
			users := &signInUsers{others: map[string]bool{}}
			for _, handle := range c.TakenByOthers {
				users.others[strings.ToLower(handle)] = true
			}
			if c.Returning != nil {
				users.self = &sqlc.User{
					ID:             pgUUIDFrom(uuid.New()),
					GithubID:       githubID,
					GithubUsername: c.Returning.Handle,
					Provider:       "github",
					UsernameChosen: c.Returning.Chosen,
				}
			}
			q := users.querier(t, githubID)
			h := newTestHandler(q, nil)

			user, err := h.signInGitHubUser(context.Background(), githubProfile{ID: githubID, Login: c.Login})
			if err != nil {
				t.Fatalf("signInGitHubUser: %v", err)
			}

			// What the web app is told: the handle gate opens /welcome only
			// when this answers username_chosen=false.
			accountID, err := uuid.FromBytes(user.ID.Bytes[:])
			if err != nil {
				t.Fatal(err)
			}
			me := httptest.NewRecorder()
			h.Me(me, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil).WithContext(withUserID(context.Background(), accountID)))
			if me.Code != http.StatusOK {
				t.Fatalf("GET /auth/me status = %d (%s)", me.Code, me.Body.String())
			}
			var answer meAnswer
			if err := json.Unmarshal(me.Body.Bytes(), &answer); err != nil {
				t.Fatal(err)
			}
			if answer.GithubUsername != c.ExpectHandle || answer.UsernameChosen != c.ExpectChosen {
				t.Fatalf("/auth/me = {handle %q, chosen %v}, want {handle %q, chosen %v}",
					answer.GithubUsername, answer.UsernameChosen, c.ExpectHandle, c.ExpectChosen)
			}

			// A handle is written chosen exactly once: when sign-in confirms
			// it. An account already chosen is never written again, and a
			// collision is left for the person to settle.
			wantWrites := 0
			if c.ExpectChosen && (c.Returning == nil || !c.Returning.Chosen) {
				wantWrites = 1
			}
			if len(users.setUsername) != wantWrites {
				t.Fatalf("SetUsername called %d times, want %d", len(users.setUsername), wantWrites)
			}

			if c.Flow == "cli" {
				if got := cliExchangeUsername(t, h, q, user); got != c.ExpectHandle {
					t.Fatalf("CLI exchange username = %q, want the village handle %q (login %q)", got, c.ExpectHandle, c.Login)
				}
			}
		})
	}
}

// cliExchangeUsername runs the CLI half of a sign-in for user: the real
// callback that records the session and redirects to the CLI with a one-time
// code, then the real exchange the CLI posts that code to. It returns the
// username the exchange hands the CLI.
func cliExchangeUsername(t *testing.T, h *Handler, q *mockQuerier, user sqlc.User) string {
	t.Helper()
	pending := sqlc.CliAuthSession{
		ID:         pgUUIDFrom(uuid.New()),
		OauthState: "oauth-state",
		CliPort:    51234,
		CliState:   "cli-state",
	}
	var stored *sqlc.CliAuthSession
	q.updateCLISessionWithCode = func(_ context.Context, arg sqlc.UpdateCLISessionWithCodeParams) error {
		if arg.OauthState != pending.OauthState {
			t.Errorf("UpdateCLISessionWithCode oauth_state = %q, want %q", arg.OauthState, pending.OauthState)
		}
		session := pending
		session.ExchangeCode = arg.ExchangeCode
		session.UserID = arg.UserID
		session.Username = arg.Username
		stored = &session
		return nil
	}
	q.exchangeCLISession = func(_ context.Context, arg sqlc.ExchangeCLISessionParams) (sqlc.CliAuthSession, error) {
		if stored == nil || arg.ExchangeCode != stored.ExchangeCode || arg.CliState != stored.CliState {
			return sqlc.CliAuthSession{}, pgx.ErrNoRows
		}
		return *stored, nil
	}
	q.createAPIKey = func(_ context.Context, arg sqlc.CreateAPIKeyParams) (sqlc.ApiKey, error) {
		return sqlc.ApiKey{ID: pgUUIDFrom(uuid.New()), UserID: arg.UserID, KeyHash: arg.KeyHash, KeyPrefix: arg.KeyPrefix}, nil
	}

	callback := httptest.NewRecorder()
	h.handleCLICallback(callback, httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback", nil), &user, pending)
	if callback.Code != http.StatusTemporaryRedirect {
		t.Fatalf("CLI callback status = %d (%s)", callback.Code, callback.Body.String())
	}
	location, err := url.Parse(callback.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	exchange := postCLIExchange(t, h, location.Query().Get("code"), location.Query().Get("state"))
	if exchange.Code != http.StatusOK {
		t.Fatalf("CLI exchange status = %d (%s)", exchange.Code, exchange.Body.String())
	}
	var resp CLIExchangeResponse
	if err := json.Unmarshal(exchange.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Username
}
