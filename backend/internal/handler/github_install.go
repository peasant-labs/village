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

	// The linked org is a fact of the installation, so the callback records it
	// rather than requiring the owner to have set a matching field by hand
	// first. Prefer a collective already bound to this account, then the
	// collective the handshake was started from, then the only owned collective
	// when it carries no org yet. With several unlinked collectives and no
	// state, the account does not say which one the owner meant, so nothing is
	// bound and they are sent to their collectives to start from the one they
	// want.
	account := strings.TrimSpace(inst.AccountLogin)
	var bound *sqlc.ListUserGroupsRow
	if account != "" {
		for i := range groups {
			g := &groups[i]
			if g.Role == "owner" && g.LinkedGithubOrg.Valid && strings.EqualFold(strings.TrimSpace(g.LinkedGithubOrg.String), account) {
				bound = g
				break
			}
		}
		if bound == nil && preferred != "" {
			for i := range groups {
				g := &groups[i]
				if g.Role == "owner" && uuid.UUID(g.ID.Bytes).String() == preferred {
					bound = g
					break
				}
			}
		}
		if bound == nil {
			unlinked := make([]*sqlc.ListUserGroupsRow, 0, 1)
			for i := range groups {
				g := &groups[i]
				if g.Role == "owner" && !g.LinkedGithubOrg.Valid {
					unlinked = append(unlinked, g)
				}
			}
			if len(unlinked) == 1 {
				bound = unlinked[0]
			}
		}
	}

	target := ""
	if bound != nil {
		target = uuid.UUID(bound.ID.Bytes).String()
		alreadyBound := bound.LinkedGithubOrg.Valid && strings.EqualFold(strings.TrimSpace(bound.LinkedGithubOrg.String), account)
		if !alreadyBound {
			if err := h.queries.SetGroupLinkedGitHubOrg(r.Context(), sqlc.SetGroupLinkedGitHubOrgParams{
				ID:              bound.ID,
				LinkedGithubOrg: pgtype.Text{String: account, Valid: true},
			}); err != nil {
				writeError(w, http.StatusInternalServerError, "Could not bind the collective to the installation")
				return
			}
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
