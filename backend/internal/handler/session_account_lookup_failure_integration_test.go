//go:build integration

package handler

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema/route"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

//go:embed testdata/session_account_lookup_failure.yaml
var sessionAccountLookupFailureYAML []byte

type sessionLookupCredential string

const (
	sessionLookupCredentialCookie sessionLookupCredential = "cookie"
	sessionLookupCredentialBearer sessionLookupCredential = "bearer"
	// The failing account's cookie plus a live bearer session for another account.
	sessionLookupCredentialCookieWithLiveBearer sessionLookupCredential = "cookie_with_live_bearer"
)

// failingAccountLookup is the production queries with one dependency failure:
// reading the named account answers a database error. Every other read and
// write, including reads of other accounts, reaches PostgreSQL.
type failingAccountLookup struct {
	Querier
	account  pgtype.UUID
	failures *atomic.Int64
}

// unstring-allow: go-endpoint-literal — API key creation is not in the served contract yet
const lookupFailureCreateAPIKeyPath = "/api/v1/auth/api-keys"

var errAccountLookupUnavailable = errors.New("connection to the account database was lost")

func (q failingAccountLookup) GetUserByID(ctx context.Context, id pgtype.UUID) (sqlc.User, error) {
	if id == q.account {
		q.failures.Add(1)
		return sqlc.User{}, errAccountLookupUnavailable
	}
	return q.Querier.GetUserByID(ctx, id)
}

func TestSessionAccountLookupFailureFailsClosed(t *testing.T) {
	cases, err := decodeFixtureRows[struct {
		Name       string                  `yaml:"name"`
		Credential sessionLookupCredential `yaml:"credential"`
	}](sessionAccountLookupFailureYAML)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, c := range cases {
		names = append(names, c.Name)
	}
	if err := requireExactNames("session account lookup failure", names, []string{
		"cookie-lookup-failure-with-live-bearer-fails-closed",
		"session-cookie-lookup-failure-fails-closed",
		"session-lookup-failure-fails-closed",
	}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	pool := govTestPool(t)
	t.Cleanup(pool.Close)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	accountName := "lookup-fail-" + suffix
	account := shareInsertUser(t, ctx, pool, accountName)
	otherName := "lookup-other-" + suffix
	other := shareInsertUser(t, ctx, pool, otherName)
	t.Cleanup(func() { cleanupOwners(t, context.Background(), pool, account, other) })
	own := shareInsertTranscript(t, ctx, pool, account, "lookup-own-"+suffix, dbVisibilityPrivate)
	public := shareInsertTranscript(t, ctx, pool, other, "lookup-public-"+suffix, dbVisibilityPublic)

	h := NewWithTitlePipeline(minimalConfig(), pool, nil, nil)
	var failures atomic.Int64
	h.queries = failingAccountLookup{Querier: h.queries, account: account, failures: &failures}
	// The same middleware and handlers the production router mounts for these
	// paths; only the account read fails.
	routes := chi.NewRouter()
	routes.With(h.AuthRequired).Get(route.VillageGetAuthenticatedVillageUser.Pattern(), h.Me)
	routes.With(h.AuthRequired).Post(lookupFailureCreateAPIKeyPath, h.CreateAPIKey)
	routes.With(h.AuthOptional).Get(route.VillageGetTranscriptMetadata.Pattern(), h.GetTranscript)
	// Probes that echo the viewer the same middleware attached, so anonymity is
	// observed directly rather than inferred from a route's answer.
	routes.With(h.AuthOptional).Get(lookupFailureOptionalProbePath, lookupFailureViewerProbe)
	routes.With(h.AuthRequired).Get(lookupFailureRequiredProbePath, lookupFailureViewerProbe)

	// Capture the structured log for the failing requests. Restored on cleanup.
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	token, err := auth.CreateToken(h.cfg.JWTSecret, uuid.UUID(account.Bytes), accountName)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := auth.CreateToken(h.cfg.JWTSecret, uuid.UUID(other.Bytes), otherName)
	if err != nil {
		t.Fatal(err)
	}
	serve := func(method, path, body string, sign func(*http.Request)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		sign(r)
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, r)
		return rec
	}
	anonymous := func(*http.Request) {}
	missing := serve(http.MethodGet, sessionLookupTranscriptPath(t, uuid.New()), "", anonymous)
	anonymousPublic := serve(http.MethodGet, sessionLookupTranscriptPath(t, uuid.UUID(public.Bytes)), "", anonymous)
	if missing.Code != http.StatusNotFound || anonymousPublic.Code != http.StatusOK {
		t.Fatalf("anonymous baselines = missing %d %s / public %d %s, want 404 / 200", missing.Code, missing.Body.String(), anonymousPublic.Code, anonymousPublic.Body.String())
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var sign func(*http.Request)
			switch c.Credential {
			case sessionLookupCredentialCookie:
				sign = func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token}) }
			case sessionLookupCredentialBearer:
				sign = func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
			case sessionLookupCredentialCookieWithLiveBearer:
				sign = func(r *http.Request) {
					r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
					r.Header.Set("Authorization", "Bearer "+otherToken)
				}
			default:
				t.Fatalf("unknown credential %q", c.Credential)
			}
			for _, required := range []struct{ method, path, body string }{
				{http.MethodGet, route.VillageGetAuthenticatedVillageUser.Pattern(), ""},
				{http.MethodPost, lookupFailureCreateAPIKeyPath, `{"label":"during outage"}`},
				{http.MethodGet, lookupFailureRequiredProbePath, ""},
			} {
				got := serve(required.method, required.path, required.body, sign)
				var body map[string]string
				_ = json.Unmarshal(got.Body.Bytes(), &body)
				want := map[string]string{"error": sessionAccountUnconfirmedMessage}
				if got.Code != http.StatusServiceUnavailable || got.Header().Get("Retry-After") != sessionAccountRetryAfterSeconds || !maps.Equal(body, want) {
					t.Fatalf("%s %s with an unreadable account = %d (Retry-After %q) %s, want 503 (Retry-After %q) %v",
						required.method, required.path, got.Code, got.Header().Get("Retry-After"), got.Body.String(), sessionAccountRetryAfterSeconds, want)
				}
			}
			probe := serve(http.MethodGet, lookupFailureOptionalProbePath, "", sign)
			if probe.Code != http.StatusOK || strings.TrimSpace(probe.Body.String()) != `{"viewer":""}` {
				t.Fatalf("the optional-auth probe during the outage = %d %s, want no viewer", probe.Code, probe.Body.String())
			}
			var keys int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM api_keys WHERE user_id = $1`, account).Scan(&keys); err != nil || keys != 0 {
				t.Fatalf("a refused write left %d API key rows (err %v)", keys, err)
			}
			ownRead := serve(http.MethodGet, sessionLookupTranscriptPath(t, uuid.UUID(own.Bytes)), "", sign)
			if ownRead.Code != http.StatusNotFound || ownRead.Body.String() != missing.Body.String() {
				t.Fatalf("the account's own private transcript during the outage = %d %s, want the anonymous missing-id 404 %s", ownRead.Code, ownRead.Body.String(), missing.Body.String())
			}
			publicRead := serve(http.MethodGet, sessionLookupTranscriptPath(t, uuid.UUID(public.Bytes)), "", sign)
			if publicRead.Code != http.StatusOK || publicRead.Body.String() != anonymousPublic.Body.String() {
				t.Fatalf("a public transcript during the outage = %d\n%s\nwant the anonymous answer\n%s", publicRead.Code, publicRead.Body.String(), anonymousPublic.Body.String())
			}
		})
	}

	// One log line per failed lookup, naming no account, username or token.
	captured := logs.String()
	if got, want := int64(strings.Count(captured, "session account lookup failed")), failures.Load(); want == 0 || got != want {
		t.Fatalf("logged %d lookup failures for %d failed lookups:\n%s", got, want, captured)
	}
	for _, secret := range []string{uuid.UUID(account.Bytes).String(), accountName, token, uuid.UUID(other.Bytes).String(), otherName, otherToken} {
		if strings.Contains(captured, secret) {
			t.Fatalf("the lookup-failure log carries identity data %q:\n%s", secret, captured)
		}
	}
}

const (
	lookupFailureOptionalProbePath = "/probe/optional"
	lookupFailureRequiredProbePath = "/probe/required"
)

// lookupFailureViewerProbe answers the viewer the auth middleware attached, or
// an empty viewer when it attached none.
var lookupFailureViewerProbe = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	viewer := ""
	if user := GetUser(r.Context()); user != nil {
		viewer = user.ID.String()
	}
	writeJSON(w, http.StatusOK, map[string]string{"viewer": viewer})
})

func sessionLookupTranscriptPath(t *testing.T, id uuid.UUID) string {
	t.Helper()
	path, err := route.VillageGetTranscriptMetadataPath(id.String())
	if err != nil {
		t.Fatalf("build the transcript path: %v", err)
	}
	return path
}
