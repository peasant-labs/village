package handler

import (
	"context"
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
// installation with the App JWT. The account is read from GitHub, never taken
// from the request, and it is recorded on a collective only when the caller's
// own account belongs to it, so a handshake reached by a link someone else sent
// cannot bind a collective to an installation its owner has no part in. The
// signed state, when present, selects which collective to bind; otherwise a
// collective already bound to the account, or the only owned collective with no
// org, is used.
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

	// Target collective: an owned collective the caller may bind, chosen by the
	// rules below rather than by a field the owner had to set first.
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
	// first. The signed state names the collective the handshake was started
	// from and is consulted first, because it is the owner's explicit choice.
	// Failing that, a collective already bound to this account is refreshed,
	// and failing that the only owned collective that carries no org yet. With
	// several unlinked collectives and no state, the account does not say which
	// one the owner meant, so nothing is bound and they are sent to their
	// collectives to start from the one they want.
	account := strings.TrimSpace(inst.AccountLogin)
	var bound *sqlc.ListUserGroupsRow
	if account != "" {
		if preferred != "" {
			for i := range groups {
				g := &groups[i]
				if g.Role == "owner" && uuid.UUID(g.ID.Bytes).String() == preferred {
					bound = g
					break
				}
			}
		}
		if bound == nil {
			for i := range groups {
				g := &groups[i]
				if g.Role == "owner" && g.LinkedGithubOrg.Valid && strings.EqualFold(strings.TrimSpace(g.LinkedGithubOrg.String), account) {
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
		alreadyBound := bound.LinkedGithubOrg.Valid && strings.EqualFold(strings.TrimSpace(bound.LinkedGithubOrg.String), account)
		if !alreadyBound {
			// The account is read from GitHub, never from the request, but the
			// handshake can be reached by a link someone else sent. Recording
			// only an account the caller belongs to keeps such a link from
			// binding a collective to an installation its owner has no part in.
			allowed, err := h.callerControlsAccount(r.Context(), user, account)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "Could not resolve the GitHub account")
				return
			}
			if !allowed {
				bound = nil
			}
		}
		if bound != nil {
			target = uuid.UUID(bound.ID.Bytes).String()
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
	}

	frontend := strings.TrimRight(h.cfg.FrontendURL, "/")
	if target == "" {
		// Installed, but nothing was bound: the account may not be one the
		// caller controls, no owned collective may be an unambiguous choice, or
		// the binding may have been refused. The installation is recorded; send
		// the caller to their collectives.
		http.Redirect(w, r, frontend+"/groups?github_installed=1", http.StatusFound)
		return
	}
	http.Redirect(w, r, frontend+"/groups/"+target+"/settings?github_installed=1", http.StatusFound)
}

// callerControlsAccount reports whether the account an installation belongs to is
// one the caller can be installing for: the GitHub login their sign-in recorded,
// or an organisation their GitHub account belongs to.
//
// The caller's Village handle is deliberately not consulted. It is theirs to
// choose, it is generated on first sign-in, and it need not match any GitHub
// login, so a handle that happens to spell an account's login proves nothing
// about the caller's relationship to that account.
//
// Membership is what matters here, not the org's visibility setting, which
// decides whether a collective may be linked to it by hand.
func (h *Handler) callerControlsAccount(ctx context.Context, user *AuthUser, account string) (bool, error) {
	if account == "" {
		return false, nil
	}
	row, err := h.queries.GetUserByID(ctx, user.PgID())
	if err != nil {
		return false, err
	}
	if strings.EqualFold(row.Provider, "github") && row.ProviderUsername.Valid &&
		strings.EqualFold(strings.TrimSpace(row.ProviderUsername.String), account) {
		return true, nil
	}
	orgs, err := h.queries.ListUserAllOrgs(ctx, user.PgID())
	if err != nil {
		return false, err
	}
	for _, org := range orgs {
		if strings.EqualFold(strings.TrimSpace(org.OrgLogin), account) {
			return true, nil
		}
	}
	return false, nil
}
