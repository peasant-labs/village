package handler

import (
	"net/http"
	"strings"

	"github.com/peasant-labs/schema"
)

// ListAvailableRepositories returns the repositories the GitHub App can offer a
// collective: the repositories of the installation for the collective's linked
// organization. Owner-only. A collective with no linked organization, or with no
// installation for it, gets an empty list rather than an error, so the picker can
// point at Connect GitHub.
// GET /api/v1/groups/{id}/repositories/available (AuthRequired)
func (h *Handler) ListAvailableRepositories(w http.ResponseWriter, r *http.Request) {
	gh, ok := h.githubGuard(w)
	if !ok {
		return
	}
	groupID, ok := h.parseGroupID(w, r)
	if !ok {
		return
	}
	if !h.requireGroupOwner(w, r, groupID) {
		return
	}

	group, err := h.queries.GetGroupByID(r.Context(), groupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read the collective")
		return
	}

	response := schema.VillageAvailableRepositoriesResponse{Repositories: []schema.VillageAvailableRepository{}}
	org := ""
	if group.LinkedGithubOrg.Valid {
		org = strings.TrimSpace(group.LinkedGithubOrg.String)
	}
	if org == "" {
		writeJSON(w, http.StatusOK, response)
		return
	}

	installations, err := gh.ListInstallations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the GitHub App installations")
		return
	}
	installationID := int64(0)
	installationAccountID := int64(0)
	for _, inst := range installations {
		if strings.EqualFold(strings.TrimSpace(inst.AccountLogin), org) {
			installationID = inst.ID
			installationAccountID = inst.AccountID
			break
		}
	}
	if installationID == 0 {
		writeJSON(w, http.StatusOK, response)
		return
	}

	// The repositories belong to the organisation that installed the App, not to
	// the collective, so the inventory is shown only to a caller whose own
	// GitHub account controls that organisation. Memberships are the ones the
	// caller's last GitHub sign-in synced, so an owner removed from the
	// organisation keeps this until that sign-in happens again.
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	identity, err := h.loadViewerIdentity(r.Context(), user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not resolve the caller's GitHub account")
		return
	}
	if !identity.controlsAccount(installationAccountID) {
		writeError(w, http.StatusForbidden, "This collective's organisation is not one your GitHub account is part of")
		return
	}

	repos, err := gh.ListInstallationRepositories(r.Context(), installationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not read the repositories from GitHub")
		return
	}
	for _, repo := range repos {
		response.Repositories = append(response.Repositories, schema.VillageAvailableRepository{
			Owner:     repo.Owner,
			Name:      repo.Name,
			IsPrivate: repo.Private,
		})
	}
	writeJSON(w, http.StatusOK, response)
}
