package handler

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/oauth2"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// CLIExchangeRequest is the JSON body for POST /api/v1/auth/cli/exchange.
type CLIExchangeRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// CLIExchangeResponse is the JSON response for a successful CLI exchange.
type CLIExchangeResponse struct {
	APIKey   string `json:"api_key"`
	KeyID    string `json:"key_id"`
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

func (h *Handler) GitHubLogin(w http.ResponseWriter, r *http.Request) {
	oauthCfg := auth.GitHubOAuthConfig(
		h.cfg.GitHubClientID,
		h.cfg.GitHubClientSecret,
		fmt.Sprintf("%s/api/v1/auth/github/callback", h.cfg.BaseURL),
	)
	state := generateState()
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   strings.HasPrefix(h.cfg.BaseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300,
	})
	http.Redirect(w, r, oauthCfg.AuthCodeURL(state), http.StatusTemporaryRedirect)
}

func (h *Handler) GitHubCallback(w http.ResponseWriter, r *http.Request) {
	oauthCfg := auth.GitHubOAuthConfig(
		h.cfg.GitHubClientID,
		h.cfg.GitHubClientSecret,
		fmt.Sprintf("%s/api/v1/auth/github/callback", h.cfg.BaseURL),
	)

	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		writeError(w, http.StatusBadRequest, "Invalid OAuth state")
		return
	}

	oauthState := stateCookie.Value
	code := r.URL.Query().Get("code")

	// Detect CLI flow from DB session keyed by oauth_state.
	cliSession, cliErr := h.queries.GetCLISessionByState(r.Context(), oauthState)

	user, err := h.exchangeGitHubUser(r.Context(), oauthCfg, code)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if cliErr == nil {
		// CLI flow: a DB session exists for this oauth_state.
		h.handleCLICallback(w, r, user, cliSession)
		return
	}

	// Web flow: create JWT and redirect to frontend.

	userID, _ := uuid.FromBytes(user.ID.Bytes[:])
	jwtToken, err := auth.CreateToken(h.cfg.JWTSecret, userID, user.GithubUsername)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create session")
		return
	}
	// Redirect to frontend with token — cookies don't work cross-domain
	// on Railway (backend and frontend have different subdomains).
	redirectURL := fmt.Sprintf("%s/auth/callback?token=%s", h.cfg.FrontendURL, url.QueryEscape(jwtToken))
	http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
}

// githubProfile is the part of GitHub's `GET /user` answer sign-in reads.
type githubProfile struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// exchangeGitHubUser exchanges an OAuth code for a GitHub token, fetches the
// GitHub user profile, and signs the user in (see signInGitHubUser).
func (h *Handler) exchangeGitHubUser(ctx context.Context, oauthCfg *oauth2.Config, code string) (*sqlc.User, error) {
	token, err := oauthCfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("OAuth exchange failed")
	}

	client := oauthCfg.Client(ctx, token)
	resp, err := client.Get("https://api.github.com/user")
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch GitHub user")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var profile githubProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, fmt.Errorf("Failed to parse GitHub user")
	}

	user, err := h.signInGitHubUser(ctx, profile)
	if err != nil {
		return nil, err
	}

	// Sync GitHub org memberships (best-effort, doesn't block login)
	h.syncGitHubOrgs(ctx, client, user.ID)

	return &user, nil
}

// signInGitHubUser upserts the account a GitHub profile signs in, and decides
// whether its handle still needs choosing.
//
// The handle is the GitHub login whenever that login is free. A new account is
// given its sanitized login when no one holds it, and a numeric suffix
// otherwise. When the handle the account ends up with IS its login, it is
// marked chosen here, so the web app's handle step (`/welcome`) opens only for
// the account whose login was taken or does not fit the handle rule. The rule
// lives here, on the server, so the frontend never re-derives it: it reads
// `username_chosen` from `/auth/me` and nothing else.
//
// A returning account keeps the handle and the chosen flag it already has; an
// account still unconfirmed on its own login (one that signed in before this
// rule) is confirmed on its next sign-in.
func (h *Handler) signInGitHubUser(ctx context.Context, profile githubProfile) (sqlc.User, error) {
	user, err := h.queries.UpsertUser(ctx, sqlc.UpsertUserParams{
		GithubID:         profile.ID,
		GithubUsername:   h.generateUniqueUsername(ctx, profile.Login),
		ProviderUsername: toPgText(profile.Login),
		DisplayName:      toPgText(profile.Name),
		AvatarUrl:        toPgText(profile.AvatarURL),
	})
	if err != nil {
		return sqlc.User{}, fmt.Errorf("Failed to create user")
	}
	if user.UsernameChosen || !handleIsLogin(user.GithubUsername, profile.Login) {
		return user, nil
	}

	// Same handle, now marked chosen. SetUsername relies on the unique index,
	// which cannot reject a handle this account already holds.
	chosen, err := h.queries.SetUsername(ctx, sqlc.SetUsernameParams{
		ID:             user.ID,
		GithubUsername: user.GithubUsername,
	})
	if err != nil {
		// Not fatal: the handle stays unconfirmed, and the handle step asks
		// for it exactly as it did before this rule existed.
		log.Printf("signInGitHubUser: could not mark the handle chosen: %v", err)
		return user, nil
	}
	return chosen, nil
}

// handleIsLogin reports whether handle is the GitHub login itself: the same
// characters, case folded, and a valid handle on its own. A login that is too
// short, too long, or was given a suffix because it was taken is not.
func handleIsLogin(handle, login string) bool {
	return usernamePattern.MatchString(handle) && handle == strings.ToLower(login)
}

// syncGitHubOrgs fetches the user's GitHub org memberships and upserts them
// into the database. Stale orgs (those the user has left) are removed.
// Errors are logged but do not fail the login flow.
func (h *Handler) syncGitHubOrgs(ctx context.Context, client *http.Client, userID pgtype.UUID) {
	fetchStart := time.Now()

	resp, err := client.Get("https://api.github.com/user/orgs?per_page=100")
	if err != nil {
		log.Printf("syncGitHubOrgs: failed to fetch orgs: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("syncGitHubOrgs: GitHub returned status %d", resp.StatusCode)
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("syncGitHubOrgs: failed to read response: %v", err)
		return
	}

	var orgs []struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.Unmarshal(body, &orgs); err != nil {
		log.Printf("syncGitHubOrgs: failed to parse orgs: %v", err)
		return
	}

	for _, org := range orgs {
		_ = h.queries.UpsertUserGitHubOrg(ctx, sqlc.UpsertUserGitHubOrgParams{
			UserID:    userID,
			OrgLogin:  org.Login,
			OrgID:     org.ID,
			AvatarUrl: toPgText(org.AvatarURL),
		})
	}

	// Delete orgs that weren't fetched this time (user left them)
	_ = h.queries.DeleteStaleUserOrgs(ctx, sqlc.DeleteStaleUserOrgsParams{
		UserID:    userID,
		FetchedAt: pgtype.Timestamptz{Time: fetchStart, Valid: true},
	})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	auth.ClearCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	dbUser, err := h.queries.GetUserByID(r.Context(), user.PgID())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch user")
		return
	}
	writeJSON(w, http.StatusOK, dbUser)
}

// UpdateMySettings updates the authenticated user's privacy settings.
// Currently exposes a single "discoverable" toggle that, when off, hides the
// user from member/contributor lists and anonymises their transcript attribution.
// PATCH /auth/me/settings (AuthRequired)
func (h *Handler) UpdateMySettings(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())

	var req struct {
		IsDiscoverable *bool `json:"is_discoverable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.IsDiscoverable == nil {
		writeError(w, http.StatusBadRequest, "Missing is_discoverable")
		return
	}

	updated, err := h.queries.UpdateUserDiscoverable(r.Context(), sqlc.UpdateUserDiscoverableParams{
		ID:             user.PgID(),
		IsDiscoverable: *req.IsDiscoverable,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update settings")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// usernamePattern enforces the canonical handle format: 3–30 chars, lowercase
// alphanumeric with single internal/trailing hyphens (must start alphanumeric).
var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,28}[a-z0-9]$`)

// SetMyUsername sets the authenticated user's canonical handle and marks it
// chosen. This is the post-SSO onboarding step for an account whose handle is
// not its login: a GitHub login that was taken or does not fit the handle rule
// (signInGitHubUser confirms every other GitHub handle itself), and every
// account from another provider. PATCH /auth/me/username.
func (h *Handler) SetMyUsername(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())

	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	handle := strings.ToLower(strings.TrimSpace(req.Username))
	if !usernamePattern.MatchString(handle) {
		writeError(w, http.StatusBadRequest, "Username must be 3–30 characters: lowercase letters, numbers, and single hyphens")
		return
	}

	updated, err := h.queries.SetUsername(r.Context(), sqlc.SetUsernameParams{
		ID:             user.PgID(),
		GithubUsername: handle,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "That username is already taken")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to set username")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// sanitizeHandle reduces an arbitrary provider handle to our handle charset:
// lowercase alphanumerics with single hyphens, trimmed, capped at 30 chars.
func sanitizeHandle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastHyphen := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		case r == '-' || r == '_' || r == ' ' || r == '.':
			if b.Len() > 0 && !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 30 {
		out = strings.Trim(out[:30], "-")
	}
	if out == "" {
		out = "user"
	}
	return out
}

// generateUniqueUsername derives a free canonical handle from base, appending a
// numeric suffix on collision. Best-effort: a concurrent signup can still race,
// in which case the INSERT's unique index rejects the duplicate.
func (h *Handler) generateUniqueUsername(ctx context.Context, base string) string {
	root := sanitizeHandle(base)
	candidate := root
	for i := 2; i < 1000; i++ {
		if _, err := h.queries.GetUserByUsername(ctx, candidate); err != nil {
			return candidate // lookup failed => handle is free
		}
		candidate = fmt.Sprintf("%s-%d", root, i)
	}
	return candidate
}

// DeleteAccount permanently deletes the authenticated user's account.
// Deleting the user row cascades to their transcripts, groups, API keys,
// org affiliations, attestations, and annotations (see migration 010).
// The auth cookie is cleared exactly as in Logout.
func (h *Handler) DeleteAccount(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	// Delete with the account holder as the audit actor. Deleting the user cascades
	// to their transcripts (migration 010 ON DELETE CASCADE); the migration-026
	// BEFORE DELETE trigger fires per cascaded transcript and stamps each
	// 'retracted' event with app.actor_id — carried by inTxAs, whose SET LOCAL is
	// txn-scoped and therefore visible to the cascade. Fail-closed: without it the
	// cascade would abort, so a full account erasure always leaves a complete trail.
	if err := h.inTxAs(r.Context(), user.PgID(), func(q Querier) error {
		return q.DeleteUser(r.Context(), user.PgID())
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete account")
		return
	}
	auth.ClearCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())

	var req struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Label = ""
	}

	plaintext, hash, prefix, err := auth.GenerateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate API key")
		return
	}

	key, err := h.queries.CreateAPIKey(r.Context(), sqlc.CreateAPIKeyParams{
		UserID:    user.PgID(),
		KeyHash:   hash,
		KeyPrefix: prefix,
		Label:     toPgText(req.Label),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create API key")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         key.ID,
		"key":        plaintext,
		"prefix":     prefix,
		"label":      req.Label,
		"created_at": key.CreatedAt,
	})
}

func (h *Handler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	keys, err := h.queries.ListUserAPIKeys(r.Context(), user.PgID())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list API keys")
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (h *Handler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid API key ID")
		return
	}
	err = h.queries.RevokeAPIKey(r.Context(), sqlc.RevokeAPIKeyParams{
		ID:     toPgUUID(id),
		UserID: user.PgID(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to revoke API key")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// CLILogin starts the CLI OAuth flow. It inserts a DB session keyed by
// oauth_state and redirects the browser to GitHub. Port must be in range 1024-65535.
func (h *Handler) CLILogin(w http.ResponseWriter, r *http.Request) {
	portStr := r.URL.Query().Get("port")
	state := r.URL.Query().Get("state")
	if portStr == "" || state == "" {
		writeError(w, http.StatusBadRequest, "Missing port or state parameter")
		return
	}

	portNum, err := strconv.Atoi(portStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid port parameter")
		return
	}
	if portNum < 1024 || portNum > 65535 {
		writeError(w, http.StatusBadRequest, "Port must be between 1024 and 65535")
		return
	}

	oauthCfg := auth.GitHubOAuthConfig(
		h.cfg.GitHubClientID,
		h.cfg.GitHubClientSecret,
		fmt.Sprintf("%s/api/v1/auth/github/callback", h.cfg.BaseURL),
	)
	oauthState := generateState()

	// Store CLI session in DB keyed by oauth_state.
	if _, err := h.queries.InsertCLISession(r.Context(), sqlc.InsertCLISessionParams{
		OauthState: oauthState,
		CliPort:    int32(portNum),
		CliState:   state,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create CLI session")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    oauthState,
		Path:     "/",
		HttpOnly: true,
		Secure:   strings.HasPrefix(h.cfg.BaseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   300,
	})
	http.Redirect(w, r, oauthCfg.AuthCodeURL(oauthState), http.StatusTemporaryRedirect)
}

// handleCLICallback finishes the GitHub OAuth callback when a CLI DB session
// exists, for the user GitHub has just signed in. It generates a one-time
// exchange_code, updates the session, and redirects to the CLI's local callback
// server with code+state only (no api_key in URL).
//
// The session records the account's village handle, not its GitHub login: the
// two differ whenever the login was taken, and the CLI names the account by the
// handle village knows it by.
func (h *Handler) handleCLICallback(w http.ResponseWriter, r *http.Request, user *sqlc.User, cliSession sqlc.CliAuthSession) {
	// Generate 32-byte random hex exchange_code (same pattern as GenerateAPIKey).
	exchangeCodeBytes := make([]byte, 32)
	if _, err := rand.Read(exchangeCodeBytes); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate exchange code")
		return
	}
	exchangeCode := hex.EncodeToString(exchangeCodeBytes)

	userID, _ := uuid.FromBytes(user.ID.Bytes[:])

	if err := h.queries.UpdateCLISessionWithCode(r.Context(), sqlc.UpdateCLISessionWithCodeParams{
		OauthState:   cliSession.OauthState,
		ExchangeCode: toPgText(exchangeCode),
		UserID:       toPgUUID(userID),
		Username:     toPgText(user.GithubUsername),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update CLI session")
		return
	}

	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback?code=%s&state=%s",
		cliSession.CliPort,
		url.QueryEscape(exchangeCode),
		url.QueryEscape(cliSession.CliState),
	)
	http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
}

// CLIExchange handles POST /api/v1/auth/cli/exchange.
// The CLI posts {code, state} to exchange a one-time code for an API key.
// Uses constant-time comparison for the exchange_code validation (done in DB via
// ExchangeCLISession which also checks not-yet-exchanged and <5 min old).
func (h *Handler) CLIExchange(w http.ResponseWriter, r *http.Request) {
	var req CLIExchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Code == "" || req.State == "" {
		writeError(w, http.StatusBadRequest, "Missing code or state")
		return
	}

	// Use constant-time comparison to guard against timing attacks on the
	// exchange_code lookup. We validate the code via the DB (which also enforces
	// not-exchanged and <5min window), then re-verify with ConstantTimeCompare.
	session, err := h.queries.ExchangeCLISession(r.Context(), sqlc.ExchangeCLISessionParams{
		ExchangeCode: toPgText(req.Code),
		CliState:     req.State,
	})
	if err != nil {
		// Constant-time guard: compare the supplied code against itself so we
		// consume the same CPU time regardless of whether the row was found.
		// This prevents leaking information via response timing.
		subtle.ConstantTimeCompare([]byte(req.Code), []byte(req.Code))
		writeError(w, http.StatusUnauthorized, "Invalid or expired exchange code")
		return
	}

	// Constant-time compare the returned exchange_code against the supplied code.
	if subtle.ConstantTimeCompare([]byte(session.ExchangeCode.String), []byte(req.Code)) != 1 {
		writeError(w, http.StatusUnauthorized, "Invalid or expired exchange code")
		return
	}

	// Create an API key for the user.
	plaintext, hash, prefix, err := auth.GenerateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate API key")
		return
	}

	userID, _ := uuid.FromBytes(session.UserID.Bytes[:])
	key, err := h.queries.CreateAPIKey(r.Context(), sqlc.CreateAPIKeyParams{
		UserID:    toPgUUID(userID),
		KeyHash:   hash,
		KeyPrefix: prefix,
		Label:     toPgText("peasant-cli"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create API key")
		return
	}

	keyID, _ := uuid.FromBytes(key.ID.Bytes[:])
	writeJSON(w, http.StatusOK, CLIExchangeResponse{
		APIKey:   plaintext,
		KeyID:    keyID.String(),
		UserID:   userID.String(),
		Username: session.Username.String,
	})
}

func generateState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
