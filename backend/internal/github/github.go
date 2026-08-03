// Package github implements a config-gated GitHub App client used to link
// collective repositories and fetch their commit history.
//
// Auth model (locked product decision): GitHub *App* auth. We mint a short-lived
// App JWT (RS256, signed with the App's private key), exchange it for a
// per-installation access token, and cache that token until just before it
// expires. Installation tokens — not OAuth user tokens — let a collective read
// both public and private repositories the App has been granted access to.
//
// The entire feature is config-gated: if GITHUB_APP_ID / GITHUB_APP_PRIVATE_KEY
// are absent, NewClient returns (nil, ErrNotConfigured) and callers surface a
// clean "not configured" response (HTTP 501) rather than panicking.
package github

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrNotConfigured is returned when the GitHub App env config is absent. It is
// the sentinel callers check to decide whether to emit a 501 Not Implemented.
var ErrNotConfigured = errors.New("github app not configured")

// defaultAPIBaseURL is the public GitHub REST API root. Tests override it with
// an httptest server so we never touch real GitHub.
const defaultAPIBaseURL = "https://api.github.com"

// appJWTTTL is how long the minted App JWT is valid. GitHub rejects App JWTs
// with an expiry more than 10 minutes out; 9 minutes leaves clock-skew slack.
const appJWTTTL = 9 * time.Minute

// tokenExpiryGuard is subtracted from an installation token's real expiry so we
// refresh slightly early rather than racing a 401.
const tokenExpiryGuard = 1 * time.Minute

// Config carries the GitHub App credentials read from the environment.
type Config struct {
	AppID         string // GITHUB_APP_ID (numeric, as a string)
	PrivateKeyPEM string // GITHUB_APP_PRIVATE_KEY (PKCS#1 or PKCS#8 PEM)
}

// IsConfigured reports whether both required credentials are present.
func (c Config) IsConfigured() bool {
	return strings.TrimSpace(c.AppID) != "" && strings.TrimSpace(c.PrivateKeyPEM) != ""
}

// Commit is the normalized shape we cache for a repo's commit, decoupled from
// GitHub's wire format.
type Commit struct {
	SHA         string
	Message     string
	AuthorName  string
	AuthorEmail string
	AuthoredAt  time.Time
	CommittedAt time.Time
}

// Repository is the minimal repo metadata returned when validating access.
type Repository struct {
	Owner   string
	Name    string
	Private bool
}

// cachedToken is an installation access token plus its refresh deadline.
type cachedToken struct {
	token     string
	expiresAt time.Time
}

// Client is a configured GitHub App client. It is safe for concurrent use.
type Client struct {
	appID      string
	privateKey *rsa.PrivateKey
	baseURL    string
	httpClient *http.Client
	now        func() time.Time

	mu     sync.Mutex
	tokens map[int64]cachedToken // installationID -> token
}

// Option customizes a Client. Used by tests to inject a base URL / clock.
type Option func(*Client)

// WithBaseURL overrides the GitHub API root (for httptest in tests).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

// WithClock overrides the time source (for deterministic token-expiry tests).
func WithClock(now func() time.Time) Option {
	return func(c *Client) { c.now = now }
}

// NewClient builds a Client from cfg. If cfg is not configured it returns
// ErrNotConfigured; callers treat that as "feature disabled" (HTTP 501).
func NewClient(cfg Config, opts ...Option) (*Client, error) {
	if !cfg.IsConfigured() {
		return nil, ErrNotConfigured
	}

	key, err := parsePrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("github: parse private key: %w", err)
	}

	c := &Client{
		appID:      strings.TrimSpace(cfg.AppID),
		privateKey: key,
		baseURL:    defaultAPIBaseURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		now:        time.Now,
		tokens:     map[int64]cachedToken{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// parsePrivateKey accepts both PKCS#1 ("RSA PRIVATE KEY") and PKCS#8
// ("PRIVATE KEY") PEM blocks, which covers what GitHub hands out plus keys
// re-encoded by openssl.
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	pemBytes := []byte(pemStr)
	if key, err := jwt.ParseRSAPrivateKeyFromPEM(pemBytes); err == nil {
		return key, nil
	} else {
		return nil, err
	}
}

// appJWT mints a short-lived App JWT signed with the App private key (RS256).
// The "iss" claim is the App ID; "iat" is backdated 60s to tolerate clock skew.
func (c *Client) appJWT() (string, error) {
	now := c.now()
	claims := jwt.RegisteredClaims{
		Issuer:    c.appID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(appJWTTTL)),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return tok.SignedString(c.privateKey)
}

// installationToken returns a valid installation access token for the given
// installation, minting (and caching) a new one only when the cached token is
// missing or near expiry.
func (c *Client) installationToken(ctx context.Context, installationID int64) (string, error) {
	c.mu.Lock()
	if t, ok := c.tokens[installationID]; ok && c.now().Before(t.expiresAt) {
		tok := t.token
		c.mu.Unlock()
		return tok, nil
	}
	c.mu.Unlock()

	appJWT, err := c.appJWT()
	if err != nil {
		return "", fmt.Errorf("github: mint app jwt: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.baseURL, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: request installation token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", apiError(resp, "create installation token")
	}

	var body struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("github: decode installation token: %w", err)
	}
	if body.Token == "" {
		return "", errors.New("github: empty installation token in response")
	}

	expiry := body.ExpiresAt
	if expiry.IsZero() {
		expiry = c.now().Add(appJWTTTL)
	}
	c.mu.Lock()
	c.tokens[installationID] = cachedToken{token: body.Token, expiresAt: expiry.Add(-tokenExpiryGuard)}
	c.mu.Unlock()

	return body.Token, nil
}

// GetRepository fetches repo metadata using the installation token, which both
// validates that the installation can access owner/name and tells us whether
// the repo is private. A 404 here means "installation has no access".
func (c *Client) GetRepository(ctx context.Context, installationID int64, owner, name string) (*Repository, error) {
	token, err := c.installationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/repos/%s/%s", c.baseURL, owner, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: request repository: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp, "get repository")
	}

	var body struct {
		Name    string `json:"name"`
		Private bool   `json:"private"`
		Owner   struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("github: decode repository: %w", err)
	}
	return &Repository{Owner: body.Owner.Login, Name: body.Name, Private: body.Private}, nil
}

// ListCommitsOptions tunes a commit fetch.
type ListCommitsOptions struct {
	// PerPage caps the page size (GitHub max 100). Zero means GitHub's default.
	PerPage int
	// MaxPages bounds how many pages we walk so a huge repo can't run forever.
	// Zero means a single page.
	MaxPages int
	// ETag, when non-empty, is sent as If-None-Match. A 304 response means the
	// branch head is unchanged and the caller can keep its cache (NotModified=true).
	ETag string
}

// ListCommitsResult is the outcome of a (possibly conditional) commit fetch.
type ListCommitsResult struct {
	Commits     []Commit
	ETag        string // latest ETag, to persist for the next conditional request
	NotModified bool   // true when GitHub returned 304 (nothing changed)
}

// ListCommits fetches a repo's commits via the installation token, honoring
// conditional requests (If-None-Match) so unchanged repos cost zero quota.
// Pagination follows RFC5988 Link rel="next" headers up to MaxPages.
func (c *Client) ListCommits(ctx context.Context, installationID int64, owner, name string, opts ListCommitsOptions) (*ListCommitsResult, error) {
	token, err := c.installationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}

	maxPages := opts.MaxPages
	if maxPages <= 0 {
		maxPages = 1
	}

	url := fmt.Sprintf("%s/repos/%s/%s/commits", c.baseURL, owner, name)
	if opts.PerPage > 0 {
		url += "?per_page=" + strconv.Itoa(opts.PerPage)
	}

	result := &ListCommitsResult{}
	for page := 0; page < maxPages && url != ""; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		// Only the first request carries the conditional header — the head SHA
		// of the branch is what the ETag tracks.
		if page == 0 && opts.ETag != "" {
			req.Header.Set("If-None-Match", opts.ETag)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github: request commits: %w", err)
		}

		if page == 0 && resp.StatusCode == http.StatusNotModified {
			resp.Body.Close()
			result.NotModified = true
			result.ETag = opts.ETag
			return result, nil
		}
		if resp.StatusCode != http.StatusOK {
			err := apiError(resp, "list commits")
			resp.Body.Close()
			return nil, err
		}
		if page == 0 {
			result.ETag = resp.Header.Get("ETag")
		}

		var raw []ghCommit
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("github: decode commits: %w", err)
		}
		next := nextPageURL(resp.Header.Get("Link"))
		resp.Body.Close()

		for _, rc := range raw {
			result.Commits = append(result.Commits, rc.normalize())
		}
		url = next
	}

	return result, nil
}

// ghCommit mirrors the relevant slice of GitHub's commit list response.
type ghCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Name  string    `json:"name"`
			Email string    `json:"email"`
			Date  time.Time `json:"date"`
		} `json:"author"`
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

func (rc ghCommit) normalize() Commit {
	return Commit{
		SHA:         rc.SHA,
		Message:     rc.Commit.Message,
		AuthorName:  rc.Commit.Author.Name,
		AuthorEmail: rc.Commit.Author.Email,
		AuthoredAt:  rc.Commit.Author.Date,
		CommittedAt: rc.Commit.Committer.Date,
	}
}

// nextPageURL extracts the rel="next" URL from a Link header, or "" if absent.
func nextPageURL(link string) string {
	if link == "" {
		return ""
	}
	for _, part := range strings.Split(link, ",") {
		segs := strings.Split(strings.TrimSpace(part), ";")
		if len(segs) < 2 {
			continue
		}
		rawURL := strings.Trim(strings.TrimSpace(segs[0]), "<>")
		for _, attr := range segs[1:] {
			if strings.TrimSpace(attr) == `rel="next"` {
				return rawURL
			}
		}
	}
	return ""
}

// apiError builds a descriptive error from a non-2xx GitHub response, including
// the status code and (truncated) body to aid debugging without leaking tokens.
func apiError(resp *http.Response, op string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	msg := strings.TrimSpace(string(bytes.TrimSpace(body)))
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("github: %s: status %d: %s", op, resp.StatusCode, msg)
}
