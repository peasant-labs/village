package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// syntheticGitHubID derives a stable BIGINT (>0, fits a positive int63) from
// the external provider's user id. Used to populate the legacy github_id
// column for non-GitHub users without colliding with real GitHub IDs (which
// are bounded well below 2^53). The high bit of the hash word is forced ON,
// guaranteeing a value > math.MaxInt32 and effectively disjoint from real
// GitHub IDs; we also clear the sign bit so the value stays positive.
func syntheticGitHubID(provider, externalID string) int64 {
	sum := sha256.Sum256([]byte(provider + ":" + externalID))
	// Use the first 8 bytes; clear sign bit, set the high data bit.
	n := binary.BigEndian.Uint64(sum[:8])
	n &^= 1 << 63 // ensure positive when cast to int64
	n |= 1 << 62  // ensure far above any real GitHub user_id
	return int64(n)
}

// providerOAuthConfig returns the OAuth2 config for a provider, plus a flag
// indicating whether credentials are configured. Unconfigured providers
// short-circuit to a 503 so we don't redirect users into a broken flow.
func (h *Handler) providerOAuthConfig(provider, callbackURL string) (*oauth2.Config, bool) {
	switch provider {
	case "gitlab":
		if h.cfg.GitLabClientID == "" || h.cfg.GitLabClientSecret == "" {
			return nil, false
		}
		return auth.GitLabOAuthConfig(h.cfg.GitLabClientID, h.cfg.GitLabClientSecret, callbackURL), true
	case "huggingface":
		if h.cfg.HuggingFaceClientID == "" {
			return nil, false
		}
		return auth.HuggingFaceOAuthConfig(h.cfg.HuggingFaceClientID, h.cfg.HuggingFaceClientSecret, callbackURL), true
	case "codeberg":
		if h.cfg.CodebergClientID == "" || h.cfg.CodebergClientSecret == "" {
			return nil, false
		}
		return auth.CodebergOAuthConfig(h.cfg.CodebergClientID, h.cfg.CodebergClientSecret, callbackURL), true
	case "sourcehut":
		if h.cfg.SourceHutClientID == "" || h.cfg.SourceHutClientSecret == "" {
			return nil, false
		}
		return auth.SourceHutOAuthConfig(h.cfg.SourceHutClientID, h.cfg.SourceHutClientSecret, callbackURL), true
	}
	return nil, false
}

// providerLogin is the entry point for non-GitHub OAuth flows. The provider
// is hard-coded per-route so an arbitrary value can't be smuggled through.
func (h *Handler) providerLogin(w http.ResponseWriter, r *http.Request, provider string) {
	callbackURL := fmt.Sprintf("%s/api/v1/auth/%s/callback", h.cfg.BaseURL, provider)
	cfg, ok := h.providerOAuthConfig(provider, callbackURL)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("%s sign-in is not configured", provider))
		return
	}
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
	http.Redirect(w, r, cfg.AuthCodeURL(state), http.StatusTemporaryRedirect)
}

func (h *Handler) providerCallback(w http.ResponseWriter, r *http.Request, provider string) {
	callbackURL := fmt.Sprintf("%s/api/v1/auth/%s/callback", h.cfg.BaseURL, provider)
	cfg, ok := h.providerOAuthConfig(provider, callbackURL)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("%s sign-in is not configured", provider))
		return
	}

	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		writeError(w, http.StatusBadRequest, "Invalid OAuth state")
		return
	}

	code := r.URL.Query().Get("code")
	token, err := cfg.Exchange(r.Context(), code)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "OAuth exchange failed")
		return
	}

	profile, err := fetchProviderProfile(r.Context(), provider, cfg, token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if profile.Username == "" || profile.ID == "" {
		writeError(w, http.StatusInternalServerError, "Provider returned an empty profile")
		return
	}

	// github_username is the canonical, user-chosen handle. On first signup we
	// seed it with a unique candidate derived from the provider handle; the user
	// is prompted to confirm/change it (username_chosen=false). The raw provider
	// handle is kept in provider_username for the onboarding suggestion.
	user, err := h.queries.UpsertUserByProvider(r.Context(), sqlc.UpsertUserByProviderParams{
		GithubID:         syntheticGitHubID(provider, profile.ID),
		GithubUsername:   h.generateUniqueUsername(r.Context(), profile.Username),
		ProviderUsername: toPgText(profile.Username),
		DisplayName:      toPgText(profile.DisplayName),
		AvatarUrl:        toPgText(profile.AvatarURL),
		Provider:         provider,
		ProviderUserID:   profile.ID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create user")
		return
	}

	userID, _ := uuid.FromBytes(user.ID.Bytes[:])
	jwtToken, err := auth.CreateToken(h.cfg.JWTSecret, userID, user.GithubUsername)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create session")
		return
	}
	redirectURL := fmt.Sprintf("%s/auth/callback?token=%s", h.cfg.FrontendURL, url.QueryEscape(jwtToken))
	http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
}

// providerProfile is the normalized identity returned by each provider's
// user-info endpoint. ID is the external stable identifier (numeric or
// string depending on provider).
type providerProfile struct {
	ID          string
	Username    string
	DisplayName string
	AvatarURL   string
}

func fetchProviderProfile(ctx context.Context, provider string, cfg *oauth2.Config, token *oauth2.Token) (*providerProfile, error) {
	client := cfg.Client(ctx, token)

	switch provider {
	case "gitlab":
		return fetchGitLabProfile(client)
	case "huggingface":
		return fetchHuggingFaceProfile(client)
	case "codeberg":
		return fetchCodebergProfile(client)
	case "sourcehut":
		return fetchSourceHutProfile(client)
	}
	return nil, fmt.Errorf("unknown provider %q", provider)
}

func fetchGitLabProfile(client *http.Client) (*providerProfile, error) {
	resp, err := client.Get("https://gitlab.com/api/v4/user")
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch GitLab user")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var u struct {
		ID        int64  `json:"id"`
		Username  string `json:"username"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("Failed to parse GitLab user")
	}
	return &providerProfile{
		ID:          strconv.FormatInt(u.ID, 10),
		Username:    u.Username,
		DisplayName: u.Name,
		AvatarURL:   u.AvatarURL,
	}, nil
}

func fetchHuggingFaceProfile(client *http.Client) (*providerProfile, error) {
	// /oauth/userinfo returns OIDC-style claims when openid scope was granted.
	resp, err := client.Get("https://huggingface.co/oauth/userinfo")
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch Hugging Face user")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var u struct {
		Sub               string `json:"sub"`
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
		Picture           string `json:"picture"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("Failed to parse Hugging Face user")
	}
	return &providerProfile{
		ID:          u.Sub,
		Username:    u.PreferredUsername,
		DisplayName: u.Name,
		AvatarURL:   u.Picture,
	}, nil
}

func fetchCodebergProfile(client *http.Client) (*providerProfile, error) {
	// Gitea/Forgejo OIDC userinfo. Returns sub, preferred_username, name, picture.
	resp, err := client.Get("https://codeberg.org/login/oauth/userinfo")
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch Codeberg user")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var u struct {
		Sub               string `json:"sub"`
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
		Picture           string `json:"picture"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("Failed to parse Codeberg user")
	}
	return &providerProfile{
		ID:          u.Sub,
		Username:    u.PreferredUsername,
		DisplayName: u.Name,
		AvatarURL:   u.Picture,
	}, nil
}

func fetchSourceHutProfile(client *http.Client) (*providerProfile, error) {
	// meta.sr.ht exposes a GraphQL `me` query — no REST profile endpoint.
	body := strings.NewReader(`{"query":"query { me { id username canonicalName url } }"}`)
	req, err := http.NewRequest(http.MethodPost, "https://meta.sr.ht/query", body)
	if err != nil {
		return nil, fmt.Errorf("Failed to build Sourcehut request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch Sourcehut user")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var result struct {
		Data struct {
			Me struct {
				ID            int64  `json:"id"`
				Username      string `json:"username"`
				CanonicalName string `json:"canonicalName"`
				URL           string `json:"url"`
			} `json:"me"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&result); err != nil {
		return nil, fmt.Errorf("Failed to parse Sourcehut user")
	}
	me := result.Data.Me
	if me.ID == 0 {
		return nil, fmt.Errorf("Sourcehut returned an empty profile")
	}
	return &providerProfile{
		ID:          strconv.FormatInt(me.ID, 10),
		Username:    me.Username,
		DisplayName: me.CanonicalName,
		AvatarURL:   "",
	}, nil
}

// --- Per-provider HTTP handler shims -----------------------------------------

func (h *Handler) GitLabLogin(w http.ResponseWriter, r *http.Request) {
	h.providerLogin(w, r, "gitlab")
}
func (h *Handler) GitLabCallback(w http.ResponseWriter, r *http.Request) {
	h.providerCallback(w, r, "gitlab")
}

func (h *Handler) HuggingFaceLogin(w http.ResponseWriter, r *http.Request) {
	h.providerLogin(w, r, "huggingface")
}
func (h *Handler) HuggingFaceCallback(w http.ResponseWriter, r *http.Request) {
	h.providerCallback(w, r, "huggingface")
}

func (h *Handler) CodebergLogin(w http.ResponseWriter, r *http.Request) {
	h.providerLogin(w, r, "codeberg")
}
func (h *Handler) CodebergCallback(w http.ResponseWriter, r *http.Request) {
	h.providerCallback(w, r, "codeberg")
}

func (h *Handler) SourceHutLogin(w http.ResponseWriter, r *http.Request) {
	h.providerLogin(w, r, "sourcehut")
}
func (h *Handler) SourceHutCallback(w http.ResponseWriter, r *http.Request) {
	h.providerCallback(w, r, "sourcehut")
}
