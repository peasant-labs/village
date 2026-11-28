//go:build integration

package router

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	schemaroute "github.com/peasant-labs/schema/route"
	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/config"
	"github.com/peasant-labs/village/backend/internal/handler"
)

//go:embed testdata/session_identity_probe.yaml
var sessionIdentityProbeYAML []byte

// requiredSessionIdentityProbeNames is the fixture's name manifest.
var requiredSessionIdentityProbeNames = []string{
	"deleted-cookie-live-api-key",
	"deleted-cookie-live-bearer-signs-in-as-bearer",
	"session-optional-auth-anonymous-after-account-deletion",
}

type probeCookie string

const (
	probeCookieNone    probeCookie = "none"
	probeCookieDeleted probeCookie = "deleted"
)

type probeBearer string

const (
	probeBearerNone           probeBearer = "none"
	probeBearerDeletedSession probeBearer = "deleted_session"
	probeBearerLiveSession    probeBearer = "live_session"
	probeBearerLiveAPIKey     probeBearer = "live_api_key"
)

type probeViewer string

const (
	probeViewerAnonymous probeViewer = "anonymous"
	probeViewerDeleted   probeViewer = "deleted"
	probeViewerLive      probeViewer = "live"
)

type sessionIdentityProbeRequest struct {
	Cookie         probeCookie `yaml:"cookie"`
	Bearer         probeBearer `yaml:"bearer"`
	Viewer         probeViewer `yaml:"viewer"`
	RequiredStatus int         `yaml:"required_status"`
}

type sessionIdentityProbeFile struct {
	Cases []struct {
		Name     string                        `yaml:"name"`
		Requests []sessionIdentityProbeRequest `yaml:"requests"`
	} `yaml:"cases"`
}

const (
	optionalProbePath = "/probe/optional"
	requiredProbePath = "/probe/required"
)

// identityProbe answers the viewer the auth middleware attached, or an empty
// viewer when it attached none.
var identityProbe = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	viewer := ""
	if user := handler.GetUser(r.Context()); user != nil {
		viewer = user.ID.String()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"viewer": viewer})
})

// TestSessionIdentityThroughProductionMiddleware observes the authenticated
// identity directly instead of inferring it from a route's answer: after an
// account is deleted, its cookie and bearer session reach an optional-auth
// handler with no user attached, and a dead cookie does not stop a live
// credential in the Authorization header from signing the caller in.
func TestSessionIdentityThroughProductionMiddleware(t *testing.T) {
	file, err := decodeSingleYAMLDocument[sessionIdentityProbeFile](sessionIdentityProbeYAML)
	if err != nil {
		t.Fatalf("load the session identity probe fixture: %v", err)
	}
	present := map[string]bool{}
	for _, c := range file.Cases {
		if present[c.Name] || len(c.Requests) == 0 {
			t.Fatalf("the session identity probe fixture repeats %q or gives it no requests", c.Name)
		}
		present[c.Name] = true
	}
	assertExactNames(t, "session_identity_probe", present, requiredSessionIdentityProbeNames)

	w := newSessionLifecycleWorld(t)
	// The same AuthOptional and AuthRequired the router mounts, from a handler
	// over the same database and signing secret as the production routes.
	h := handler.NewWithTitlePipeline(&config.Config{JWTSecret: w.secret, FrontendURL: "https://app.example.com"}, w.pool, nil, nil)
	probes := chi.NewRouter()
	probes.With(h.AuthOptional).Get(optionalProbePath, identityProbe)
	probes.With(h.AuthRequired).Get(requiredProbePath, identityProbe)

	for _, c := range file.Cases {
		t.Run(c.Name, func(t *testing.T) {
			deleted, deletedLogin := w.insertUser(t, "probe-deleted")
			live, liveLogin := w.insertUser(t, "probe-live")
			t.Cleanup(func() {
				// Neither account owns a transcript, so no governance trigger fires.
				if _, err := w.pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1::uuid[])`, []uuid.UUID{deleted, live}); err != nil {
					t.Errorf("delete the probe accounts: %v", err)
				}
			})
			deletedSession := mintSession(t, w.secret, deleted, deletedLogin)
			liveSession := mintSession(t, w.secret, live, liveLogin)
			created := w.serve(t, http.MethodPost, schemaroute.VillageCreateMyAPIKey.Pattern(), `{"label":"identity probe"}`, bearerOf(liveSession))
			var key struct {
				Key string `json:"key"`
			}
			if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &key) != nil || key.Key == "" {
				t.Fatalf("create the live account's API key = %d %s, want 201 with a key", created.Code, created.Body.String())
			}
			viewers := map[probeViewer]string{probeViewerAnonymous: "", probeViewerDeleted: deleted.String(), probeViewerLive: live.String()}

			// Before the deletion, both of the account's sessions sign it in.
			for _, sign := range []func(*http.Request){cookieOf(deletedSession), bearerOf(deletedSession)} {
				assertProbe(t, probes, sign, viewers[probeViewerDeleted], http.StatusOK)
			}

			gone := w.serve(t, http.MethodDelete, schemaroute.VillageDeleteMyAccount.Pattern(), "", bearerOf(deletedSession))
			if gone.Code != http.StatusOK {
				t.Fatalf("delete the account = %d %s, want 200", gone.Code, gone.Body.String())
			}

			for _, req := range c.Requests {
				var sign []func(*http.Request)
				switch req.Cookie {
				case probeCookieNone:
				case probeCookieDeleted:
					sign = append(sign, cookieOf(deletedSession))
				default:
					t.Fatalf("unknown cookie %q", req.Cookie)
				}
				switch req.Bearer {
				case probeBearerNone:
				case probeBearerDeletedSession:
					sign = append(sign, bearerOf(deletedSession))
				case probeBearerLiveSession:
					sign = append(sign, bearerOf(liveSession))
				case probeBearerLiveAPIKey:
					sign = append(sign, bearerOf(key.Key))
				default:
					t.Fatalf("unknown bearer %q", req.Bearer)
				}
				want, ok := viewers[req.Viewer]
				if !ok {
					t.Fatalf("unknown viewer %q", req.Viewer)
				}
				assertProbe(t, probes, func(r *http.Request) {
					for _, s := range sign {
						s(r)
					}
				}, want, req.RequiredStatus)
			}
		})
	}
}

// assertProbe checks the optional-auth probe first, so a ghost session is
// reported as the identity it carried, then the auth-required probe.
func assertProbe(t *testing.T, probes http.Handler, sign func(*http.Request), viewer string, requiredStatus int) {
	t.Helper()
	call := func(path string) (*httptest.ResponseRecorder, string) {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		sign(r)
		rec := httptest.NewRecorder()
		probes.ServeHTTP(rec, r)
		var body struct {
			Viewer string `json:"viewer"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec, body.Viewer
	}
	optional, seen := call(optionalProbePath)
	if optional.Code != http.StatusOK || seen != viewer {
		t.Fatalf("the optional-auth probe saw viewer %q (%d %s), want %q", seen, optional.Code, strings.TrimSpace(optional.Body.String()), viewer)
	}
	required, seen := call(requiredProbePath)
	if required.Code != requiredStatus {
		t.Fatalf("the auth-required probe = %d %s, want %d", required.Code, strings.TrimSpace(required.Body.String()), requiredStatus)
	}
	if requiredStatus == http.StatusOK && seen != viewer {
		t.Fatalf("the auth-required probe saw viewer %q, want %q", seen, viewer)
	}
}

func mintSession(t *testing.T, secret string, account uuid.UUID, login string) string {
	t.Helper()
	token, err := auth.CreateToken(secret, account, login)
	if err != nil {
		t.Fatalf("mint a session: %v", err)
	}
	return token
}

func cookieOf(token string) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token}) }
}

func bearerOf(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}
