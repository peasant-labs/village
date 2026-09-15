package handler

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/auth"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// GitHubInstall starts the GitHub App install handshake for a collective. The
// caller must own the collective; it redirects them to GitHub's install page for
// the Village App with a signed state binding this handshake to them and to the
// collective, so the callback cannot be driven by anyone else.
// GET /api/v1/integrations/github/install (AuthRequired)
func (h *Handler) GitHubInstall(w http.ResponseWriter, r *http.Request) {
	if h.gh == nil || strings.TrimSpace(h.cfg.GitHubAppSlug) == "" {
		writeError(w, http.StatusNotImplemented, "GitHub App installation is not configured on this server")
		return
	}
	gid, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("group_id")))
	if err != nil {
		writeError(w, http.StatusBadRequest, "group_id is required")
		return
	}
	groupID := pgtype.UUID{Bytes: gid, Valid: true}
	if !h.requireGroupOwner(w, r, groupID) {
		return
	}

	user := GetUser(r.Context())
	state, err := auth.CreateInstallState(h.cfg.JWTSecret, user.ID.String(), gid.String())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not start the GitHub App installation")
		return
	}

	target := "https://github.com/apps/" + url.PathEscape(strings.TrimSpace(h.cfg.GitHubAppSlug)) +
		"/installations/new?state=" + url.QueryEscape(state)
	http.Redirect(w, r, target, http.StatusFound)
}

// GitHubInstallCallback completes the install handshake: it reads the
// installation with the App's own credentials, records it, and returns the owner
// to the collective it was installed for.
//
// It does not trust the redirect's parameters alone. GitHub warns the
// installation_id can be spoofed and does not reliably forward the install URL's
// state to the setup URL, so the callback requires a session and reads the
// installation with the App JWT. The collective is the signed-in owner's
// collective bound to the installation's account; the signed state, when present,
// only selects among those. Nothing is exposed by a spoofed id: the account is
// read from GitHub, never taken from the request.
//
// GET /api/v1/integrations/github/callback (AuthRequired)
func (h *Handler) GitHubInstallCallback(w http.ResponseWriter, r *http.Request) {
	if h.gh == nil {
		writeError(w, http.StatusNotImplemented, "GitHub App installation is not configured on this server")
		return
	}
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}

	installationID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("installation_id")), 10, 64)
	if err != nil || installationID <= 0 {
		writeError(w, http.StatusBadRequest, "installation_id is required")
		return
	}

	inst, err := h.gh.GetInstallation(r.Context(), installationID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Could not read the GitHub App installation")
		return
	}
	if err := h.queries.UpsertGitHubAppInstallation(r.Context(), sqlc.UpsertGitHubAppInstallationParams{
		InstallationID: inst.ID,
		AccountLogin:   inst.AccountLogin,
		AccountID:      inst.AccountID,
		AccountType:    pgtype.Text{String: inst.AccountType, Valid: inst.AccountType != ""},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not record the GitHub App installation")
		return
	}

	// Target collective: one the caller owns and that is bound to the
	// installation's account. The signed state selects among them when forwarded.
	groups, err := h.queries.ListUserGroups(r.Context(), user.PgID())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not resolve the collective")
		return
	}
	preferred := ""
	if claims, err := auth.ValidateInstallState(h.cfg.JWTSecret, strings.TrimSpace(r.URL.Query().Get("state"))); err == nil && claims.UserID == user.ID.String() {
		preferred = claims.GroupID
	}
	target := ""
	for _, g := range groups {
		if g.Role != "owner" || !g.LinkedGithubOrg.Valid {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(g.LinkedGithubOrg.String), strings.TrimSpace(inst.AccountLogin)) {
			continue
		}
		id := uuid.UUID(g.ID.Bytes).String()
		if target == "" {
			target = id
		}
		if preferred != "" && id == preferred {
			target = id
			break
		}
	}

	frontend := strings.TrimRight(h.cfg.FrontendURL, "/")
	if target == "" {
		// Installed, but no owned collective is bound to that account yet. The
		// installation is recorded; send the caller to their collectives.
		http.Redirect(w, r, frontend+"/groups?github_installed=1", http.StatusFound)
		return
	}
	http.Redirect(w, r, frontend+"/groups/"+target+"/settings?github_installed=1", http.StatusFound)
}
