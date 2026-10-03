package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

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
	publishers, err := h.repositoryPublisherCounts(r.Context(), groupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not count who publishes from each repository; retry the request")
		return
	}
	for _, repo := range repos {
		response.Repositories = append(response.Repositories, schema.VillageAvailableRepository{
			Owner:          repo.Owner,
			Name:           repo.Name,
			IsPrivate:      repo.Private,
			PublisherCount: publishers[strings.ToLower(repo.Owner)+"/"+strings.ToLower(repo.Name)],
		})
	}
	writeJSON(w, http.StatusOK, response)
}

// repositoryPublisherCounts counts, for each GitHub repository, the distinct
// people whose transcripts are already shared with this collective and whose
// git remote is that repository. A transcript that is not shared with the
// collective, or whose submission is still awaiting review, counts nothing. The
// map is keyed by lowercased "owner/name".
func (h *Handler) repositoryPublisherCounts(ctx context.Context, groupID pgtype.UUID) (map[string]int32, error) {
	rows, err := h.queries.ListCollectiveSharedRemotes(ctx, groupID)
	if err != nil {
		return nil, err
	}
	owners := map[string]map[pgtype.UUID]bool{}
	for _, row := range rows {
		key, ok := githubRepositoryKey(row.GitRemote.String)
		if !ok {
			continue
		}
		if owners[key] == nil {
			owners[key] = map[pgtype.UUID]bool{}
		}
		owners[key][row.OwnerID] = true
	}
	counts := make(map[string]int32, len(owners))
	for key, people := range owners {
		counts[key] = int32(len(people))
	}
	return counts, nil
}

// githubRepositoryKey reduces a git remote to the lowercased "owner/name" of the
// github.com repository it names. It reads the remote with schema.RemoteLabel,
// the contract's rule for naming a remote's repository (the project display
// names use it too), so an SSH, HTTPS, or bare remote of the same repository
// agree, and a remote on any other host names no GitHub repository. Owner and
// name must both match: this counts who publishes from one repository, unlike
// the pull request matcher, which narrows by repository name alone and lets
// commits decide.
func githubRepositoryKey(remote string) (string, bool) {
	label, ok := schema.RemoteLabel(remote)
	if !ok {
		return "", false
	}
	host, path, found := strings.Cut(label, ":")
	if !found || host != "github.com" {
		return "", false
	}
	owner, name, found := strings.Cut(path, "/")
	if !found || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return strings.ToLower(owner) + "/" + strings.ToLower(name), true
}
