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
	for _, inst := range installations {
		if strings.EqualFold(strings.TrimSpace(inst.AccountLogin), org) {
			installationID = inst.ID
			break
		}
	}
	if installationID == 0 {
		writeJSON(w, http.StatusOK, response)
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
