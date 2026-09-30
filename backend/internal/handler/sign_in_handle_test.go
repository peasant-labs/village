package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v3"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/sign-in-handle.yaml
var signInHandleFixtures []byte

type signInHandleReturning struct {
	Handle string `yaml:"handle"`
	Chosen bool   `yaml:"chosen"`
}

type signInHandleCase struct {
	Name                 string                 `yaml:"name"`
	Why                  string                 `yaml:"why"`
	Flow                 string                 `yaml:"flow"`
	Login                string                 `yaml:"login"`
	TakenByOthers        []string               `yaml:"taken_by_others"`
	Returning            *signInHandleReturning `yaml:"returning"`
	ConfirmFails         bool                   `yaml:"confirm_fails"`
	RenamedBeforeConfirm string                 `yaml:"renamed_before_confirm"`
	ExpectHandle         string                 `yaml:"expect_handle"`
	ExpectChosen         bool                   `yaml:"expect_chosen"`
}

// unitOnly reports whether a case injects something a real database cannot be
// made to do on cue, so only the mocked corpus can run it.
func (c signInHandleCase) unitOnly() bool {
	return c.ConfirmFails || c.RenamedBeforeConfirm != ""
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
		// A failed confirm leaves the handle step open; a handle chosen in
		// another tab is the handle the account ends up with, chosen.
		if c.ConfirmFails && c.ExpectChosen {
			t.Fatalf("sign-in-handle case %q: a failed confirm cannot leave the handle chosen", c.Name)
		}
		if c.RenamedBeforeConfirm != "" && (c.ExpectHandle != c.RenamedBeforeConfirm || !c.ExpectChosen) {
			t.Fatalf("sign-in-handle case %q: a handle chosen in another tab must be the chosen handle", c.Name)
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
	// one with no CLI case whose handle differs from the login could not tell a
	// session that stores the login from one that stores the handle, and one
	// with no web case could not tell the callback's two branches apart.
	var sawChosen, sawUnchosen, sawCLIHandleNotLogin, sawWeb bool
	for _, c := range corpus.Cases {
		sawChosen = sawChosen || c.ExpectChosen
		sawUnchosen = sawUnchosen || !c.ExpectChosen
		sawCLIHandleNotLogin = sawCLIHandleNotLogin || (c.Flow == "cli" && c.ExpectHandle != c.Login)
		sawWeb = sawWeb || c.Flow == "web"
	}
	if !sawChosen || !sawUnchosen {
		t.Fatal("sign-in-handle corpus must hold a case that skips the handle step and one that opens it")
	}
	if !sawCLIHandleNotLogin || !sawWeb {
		t.Fatal("sign-in-handle corpus must hold a web case and a cli case whose handle is not the login")
	}
	return corpus
}

// fakeGitHubOAuth answers the three GitHub requests a sign-in makes: the OAuth
// token exchange, the profile read and the org list. It reaches the handler
// through the request context (oauth2.HTTPClient), which is where both the
// token exchange and the authenticated client take their transport from.
type fakeGitHubOAuth struct {
	id    int64
	login string
}

func (g fakeGitHubOAuth) RoundTrip(r *http.Request) (*http.Response, error) {
	var body string
	switch {
	case r.URL.Host == "github.com" && r.URL.Path == "/login/oauth/access_token":
		body = `{"access_token":"fake-token","token_type":"bearer"}`
	case r.URL.Host == "api.github.com" && r.URL.Path == "/user":
		b, _ := json.Marshal(map[string]any{"id": g.id, "login": g.login})
		body = string(b)
	case r.URL.Host == "api.github.com" && r.URL.Path == "/user/orgs":
		body = `[]`
	default:
		return nil, fmt.Errorf("fake GitHub has no answer for %s", r.URL)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

// signInOutcome is what a sign-in through the mounted callback leaves behind,
// read the way its consumers read it.
type signInOutcome struct {
	// handle the session names: the web token's username, or the username the
	// CLI exchange hands the CLI.
	sessionHandle string
	// the account as `GET /auth/me` answers it.
	me meAnswer
}

// meAnswer is the part of `GET /auth/me` the web app's handle gate reads.
type meAnswer struct {
	GithubUsername string `json:"github_username"`
	UsernameChosen bool   `json:"username_chosen"`
}

// signInThroughCallback runs one GitHub sign-in through the REAL
// `GET /auth/github/callback`, with GitHub faked at the transport. For the web
// flow it follows the token in the frontend redirect; for the CLI flow it
// follows the loopback redirect into the REAL `POST /auth/cli/exchange`.
func signInThroughCallback(t *testing.T, h *Handler, github fakeGitHubOAuth, oauthState, flow string) signInOutcome {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback?state="+url.QueryEscape(oauthState)+"&code=fake-code", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: oauthState})
	req = req.WithContext(context.WithValue(req.Context(), oauth2.HTTPClient, &http.Client{Transport: github}))
	callback := httptest.NewRecorder()
	h.GitHubCallback(callback, req)
	if callback.Code != http.StatusTemporaryRedirect {
		t.Fatalf("callback status = %d (%s)", callback.Code, callback.Body.String())
	}
	location, err := url.Parse(callback.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}

	var accountID uuid.UUID
	var sessionHandle string
	switch flow {
	case "web":
		frontend, _ := url.Parse(h.cfg.FrontendURL)
		if location.Host != frontend.Host || location.Path != "/auth/callback" {
			t.Fatalf("web sign-in redirected to %s, want the frontend's /auth/callback", location)
		}
		claims, err := auth.ValidateToken(h.cfg.JWTSecret, location.Query().Get("token"))
		if err != nil {
			t.Fatalf("web sign-in token: %v", err)
		}
		accountID, sessionHandle = claims.UserID, claims.Username
	case "cli":
		if location.Hostname() != "127.0.0.1" || location.Path != "/callback" {
			t.Fatalf("cli sign-in redirected to %s, want the CLI's loopback /callback", location)
		}
		exchange := postCLIExchange(t, h, location.Query().Get("code"), location.Query().Get("state"))
		if exchange.Code != http.StatusOK {
			t.Fatalf("CLI exchange status = %d (%s)", exchange.Code, exchange.Body.String())
		}
		var resp CLIExchangeResponse
		if err := json.Unmarshal(exchange.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if accountID, err = uuid.Parse(resp.UserID); err != nil {
			t.Fatal(err)
		}
		sessionHandle = resp.Username
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
	return signInOutcome{sessionHandle: sessionHandle, me: answer}
}

// signInUsers is the slice of the users table a GitHub sign-in touches: the
// handles other accounts hold, and this GitHub account's own row once it
// exists. It enforces the one constraint these queries rely on, the
// case-insensitive unique handle, and a copy of the confirm query's guard (the
// real guard is pinned against PostgreSQL by confirm-own-handle.yaml).
type signInUsers struct {
	others       map[string]bool
	self         *sqlc.User
	failConfirm  bool
	renameTo     string
	confirmCalls int
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
			var read sqlc.User
			if s.self != nil {
				// ON CONFLICT (github_id): the handle and the chosen flag are
				// preserved; only the provider fields refresh.
				s.self.ProviderUsername = arg.ProviderUsername
				read = *s.self
			} else {
				if s.others[strings.ToLower(arg.GithubUsername)] {
					return sqlc.User{}, handleTaken()
				}
				s.self = &sqlc.User{
					ID:               pgUUIDFrom(uuid.New()),
					GithubID:         arg.GithubID,
					GithubUsername:   arg.GithubUsername,
					ProviderUsername: arg.ProviderUsername,
					Provider:         "github",
				}
				read = *s.self
			}
			if s.renameTo != "" {
				// The handle step in another tab lands right after this read.
				s.self.GithubUsername, s.self.UsernameChosen = s.renameTo, true
			}
			return read, nil
		},
		confirmOwnHandle: func(_ context.Context, arg sqlc.ConfirmOwnHandleParams) (sqlc.User, error) {
			s.confirmCalls++
			if s.failConfirm {
				return sqlc.User{}, errors.New("connection reset")
			}
			if s.self == nil || arg.ID != s.self.ID || arg.GithubUsername != s.self.GithubUsername || s.self.UsernameChosen {
				return sqlc.User{}, pgx.ErrNoRows
			}
			s.self.UsernameChosen = true
			return *s.self, nil
		},
		getUserByID: func(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
			if s.self == nil || id != s.self.ID {
				return sqlc.User{}, pgx.ErrNoRows
			}
			return *s.self, nil
		},
		// setUsername is left unstubbed: sign-in must never go through the
		// person's own rename, and a call panics.
	}
}

// cliSessions is the CLI half of the mock: one pending session per OAuth
// state, recorded when the callback stores its code and handed back once to
// the exchange.
func stubCLISessions(t *testing.T, q *mockQuerier, pending *sqlc.CliAuthSession) {
	var stored *sqlc.CliAuthSession
	q.getCLISessionByState = func(_ context.Context, state string) (sqlc.CliAuthSession, error) {
		if pending == nil || state != pending.OauthState {
			return sqlc.CliAuthSession{}, pgx.ErrNoRows
		}
		return *pending, nil
	}
	q.updateCLISessionWithCode = func(_ context.Context, arg sqlc.UpdateCLISessionWithCodeParams) error {
		if pending == nil {
			// A web sign-in must never reach the CLI branch; fail the case
			// by name and let the callback answer its error.
			t.Errorf("a web sign-in stored a CLI exchange code")
			return errors.New("no CLI session for this state")
		}
		session := *pending
		session.ExchangeCode, session.UserID, session.Username = arg.ExchangeCode, arg.UserID, arg.Username
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
}

func TestGitHubSignInHandle(t *testing.T) {
	corpus := loadSignInHandleFixtures(t)
	const githubID int64 = 4242
	for _, c := range corpus.Cases {
		t.Run(c.Name, func(t *testing.T) {
			users := &signInUsers{
				others:      map[string]bool{},
				failConfirm: c.ConfirmFails,
				renameTo:    c.RenamedBeforeConfirm,
			}
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
			const state = "oauth-state"
			var pending *sqlc.CliAuthSession
			if c.Flow == "cli" {
				pending = &sqlc.CliAuthSession{ID: pgUUIDFrom(uuid.New()), OauthState: state, CliPort: 51234, CliState: "cli-state"}
			}
			stubCLISessions(t, q, pending)
			h := newTestHandler(q, nil)

			got := signInThroughCallback(t, h, fakeGitHubOAuth{id: githubID, login: c.Login}, state, c.Flow)

			// What the web app is told: the handle gate opens /welcome only
			// when this answers username_chosen=false.
			if got.me.GithubUsername != c.ExpectHandle || got.me.UsernameChosen != c.ExpectChosen {
				t.Fatalf("/auth/me = {handle %q, chosen %v}, want {handle %q, chosen %v}",
					got.me.GithubUsername, got.me.UsernameChosen, c.ExpectHandle, c.ExpectChosen)
			}
			// The session names the account by its village handle, never by
			// the login GitHub sent.
			if got.sessionHandle != c.ExpectHandle {
				t.Fatalf("%s session names %q, want the village handle %q (login %q)", c.Flow, got.sessionHandle, c.ExpectHandle, c.Login)
			}
			// A handle the person already chose is never written again.
			if c.Returning != nil && c.Returning.Chosen && users.confirmCalls != 0 {
				t.Fatalf("a chosen handle was confirmed again (%d calls)", users.confirmCalls)
			}
		})
	}
}
