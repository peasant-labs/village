package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/github"
)

// commitRefreshMaxPages bounds how many pages of commits a single refresh walks
// (100 commits/page), so even a large repo's refresh stays bounded.
const commitRefreshMaxPages = 5

// commitRefreshPerPage is the page size requested from GitHub (max allowed 100).
const commitRefreshPerPage = 100

// commitListLimit caps how many cached commits the list endpoint returns.
const commitListLimit = 200

// githubGuard returns the GitHub App client, or writes a 501 and returns false
// when the feature is not configured. Every collective-repo handler calls this
// first so the absent-config path is uniform and never panics.
func (h *Handler) githubGuard(w http.ResponseWriter) (*github.Client, bool) {
	if h.gh == nil {
		writeError(w, http.StatusNotImplemented, "GitHub repository linking is not configured on this server")
		return nil, false
	}
	return h.gh, true
}

// requireGroupOwner loads the caller's membership and rejects non-owners.
// Village's role model is owner | member | contributor | pending; "owner" is the
// admin role, so write operations (link/unlink/refresh) are gated to owners —
// matching every other owner-only mutation in groups.go.
func (h *Handler) requireGroupOwner(w http.ResponseWriter, r *http.Request, groupID pgtype.UUID) bool {
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return false
	}
	member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
		GroupID: groupID,
		UserID:  user.PgID(),
	})
	if err != nil || member.Role != "owner" {
		writeError(w, http.StatusForbidden, "Collective owner access required")
		return false
	}
	return true
}

// linkRepoRequest is the body for POST /groups/{id}/repositories.
type linkRepoRequest struct {
	Owner          string `json:"owner"`
	Name           string `json:"name"`
	InstallationID int64  `json:"installation_id"`
}

// repoResponse is the wire shape for a linked repository.
type repoResponse struct {
	ID             pgtype.UUID        `json:"id"`
	GroupID        pgtype.UUID        `json:"group_id"`
	Owner          string             `json:"owner"`
	Name           string             `json:"name"`
	InstallationID int64              `json:"installation_id"`
	IsPrivate      bool               `json:"is_private"`
	LinkedBy       pgtype.UUID        `json:"linked_by"`
	LastSyncedAt   pgtype.Timestamptz `json:"last_synced_at"`
	CreatedAt      pgtype.Timestamptz `json:"created_at"`
}

func toRepoResponse(row sqlc.CollectiveRepository) repoResponse {
	return repoResponse{
		ID:             row.ID,
		GroupID:        row.GroupID,
		Owner:          row.Owner,
		Name:           row.Name,
		InstallationID: row.InstallationID,
		IsPrivate:      row.IsPrivate,
		LinkedBy:       row.LinkedBy,
		LastSyncedAt:   row.LastSyncedAt,
		CreatedAt:      row.CreatedAt,
	}
}

// LinkRepository links a GitHub repository to a collective. Owner-only.
// It validates that the supplied installation can actually access the repo
// (via GetRepository) before persisting the link — so a bad installation_id or
// a repo the App was never granted produces a clean 400/404, not a dangling row.
// POST /groups/{id}/repositories (AuthRequired)
func (h *Handler) LinkRepository(w http.ResponseWriter, r *http.Request) {
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

	var req linkRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	req.Owner = strings.TrimSpace(req.Owner)
	req.Name = strings.TrimSpace(req.Name)
	if req.Owner == "" || req.Name == "" {
		writeError(w, http.StatusBadRequest, "owner and name are required")
		return
	}
	if req.InstallationID <= 0 {
		writeError(w, http.StatusBadRequest, "a valid installation_id is required")
		return
	}

	// Validate access through the installation. This both proves the App can
	// reach the repo and tells us whether it's private.
	repo, err := gh.GetRepository(r.Context(), req.InstallationID, req.Owner, req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "The configured installation cannot access "+req.Owner+"/"+req.Name)
		return
	}

	user := GetUser(r.Context())
	row, err := h.queries.LinkCollectiveRepository(r.Context(), sqlc.LinkCollectiveRepositoryParams{
		GroupID:        groupID,
		Owner:          repo.Owner,
		Name:           repo.Name,
		InstallationID: req.InstallationID,
		IsPrivate:      repo.Private,
		LinkedBy:       user.PgID(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to link repository")
		return
	}

	writeJSON(w, http.StatusCreated, toRepoResponse(row))
}

// UnlinkRepository removes a repository link from a collective. Owner-only.
// DELETE /groups/{id}/repositories/{owner}/{name} (AuthRequired)
func (h *Handler) UnlinkRepository(w http.ResponseWriter, r *http.Request) {
	groupID, ok := h.parseGroupID(w, r)
	if !ok {
		return
	}
	if !h.requireGroupOwner(w, r, groupID) {
		return
	}

	owner := strings.TrimSpace(chi.URLParam(r, "owner"))
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if owner == "" || name == "" {
		writeError(w, http.StatusBadRequest, "owner and name are required")
		return
	}

	affected, err := h.queries.UnlinkCollectiveRepository(r.Context(), sqlc.UnlinkCollectiveRepositoryParams{
		GroupID: groupID,
		Lower:   owner,
		Lower_2: name,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to unlink repository")
		return
	}
	if affected == 0 {
		writeError(w, http.StatusNotFound, "Repository is not linked to this collective")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
}

// ListRepositories returns a collective's linked repositories. Readable by any
// authenticated member (group membership is required to enumerate links).
// GET /groups/{id}/repositories (AuthRequired)
func (h *Handler) ListRepositories(w http.ResponseWriter, r *http.Request) {
	groupID, ok := h.parseGroupID(w, r)
	if !ok {
		return
	}

	// Any member may list; non-members get 403. Owners and members alike see
	// the link list (the commit data itself is gated separately per the group's
	// data-access policy at fetch time).
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if _, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
		GroupID: groupID,
		UserID:  user.PgID(),
	}); err != nil {
		writeError(w, http.StatusForbidden, "Collective membership required")
		return
	}

	rows, err := h.queries.ListCollectiveRepositories(r.Context(), groupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list repositories")
		return
	}
	out := make([]repoResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toRepoResponse(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": out})
}

// commitResponse is the wire shape for a cached repository commit.
type commitResponse struct {
	SHA         string             `json:"sha"`
	Message     pgtype.Text        `json:"message"`
	AuthorName  pgtype.Text        `json:"author_name"`
	AuthorEmail pgtype.Text        `json:"author_email"`
	AuthoredAt  pgtype.Timestamptz `json:"authored_at"`
	CommittedAt pgtype.Timestamptz `json:"committed_at"`
}

// ListRepositoryCommits returns a linked repo's commits. It is cache-first:
// it serves the cached rows unless ?refresh=true is passed (owner-only), in
// which case it fetches from GitHub using a conditional request (the stored
// ETag) and only re-writes the cache when GitHub reports changes.
// GET /groups/{id}/repositories/{owner}/{name}/commits (AuthRequired)
func (h *Handler) ListRepositoryCommits(w http.ResponseWriter, r *http.Request) {
	groupID, ok := h.parseGroupID(w, r)
	if !ok {
		return
	}

	owner := strings.TrimSpace(chi.URLParam(r, "owner"))
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if owner == "" || name == "" {
		writeError(w, http.StatusBadRequest, "owner and name are required")
		return
	}

	// The repo must be linked to this collective; this also scopes the request
	// to a group the caller can be checked against.
	repo, err := h.queries.GetCollectiveRepository(r.Context(), sqlc.GetCollectiveRepositoryParams{
		GroupID: groupID,
		Lower:   owner,
		Lower_2: name,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "Repository is not linked to this collective")
		return
	}

	// Any member may read commits.
	user := GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if _, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
		GroupID: groupID,
		UserID:  user.PgID(),
	}); err != nil {
		writeError(w, http.StatusForbidden, "Collective membership required")
		return
	}

	refresh := r.URL.Query().Get("refresh") == "true"
	refreshed := false
	if refresh {
		// Refresh is a mutation of cached data; gate it to owners.
		member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
			GroupID: groupID,
			UserID:  user.PgID(),
		})
		if err != nil || member.Role != "owner" {
			writeError(w, http.StatusForbidden, "Collective owner access required to refresh commits")
			return
		}
		gh, ok := h.githubGuard(w)
		if !ok {
			return
		}
		if err := h.refreshRepositoryCommits(r, gh, repo); err != nil {
			writeError(w, http.StatusBadGateway, "Failed to fetch commits from GitHub: "+err.Error())
			return
		}
		refreshed = true
	}

	rows, err := h.queries.ListRepositoryCommits(r.Context(), sqlc.ListRepositoryCommitsParams{
		Lower:   repo.Owner,
		Lower_2: repo.Name,
		Limit:   commitListLimit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read cached commits")
		return
	}

	out := make([]commitResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, commitResponse{
			SHA:         row.Sha,
			Message:     row.Message,
			AuthorName:  row.AuthorName,
			AuthorEmail: row.AuthorEmail,
			AuthoredAt:  row.AuthoredAt,
			CommittedAt: row.CommittedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"owner":        repo.Owner,
		"name":         repo.Name,
		"refreshed":    refreshed,
		"last_synced":  repo.LastSyncedAt,
		"commit_count": len(out),
		"commits":      out,
	})
}

// refreshRepositoryCommits fetches commits from GitHub for the linked repo using
// a conditional request keyed on the stored ETag, upserts any returned commits
// into the cache, and records the new ETag + sync time. When GitHub returns 304
// Not Modified, no rows are written and only the sync time is implied unchanged.
func (h *Handler) refreshRepositoryCommits(r *http.Request, gh *github.Client, repo sqlc.CollectiveRepository) error {
	etag := ""
	if repo.CommitsEtag.Valid {
		etag = repo.CommitsEtag.String
	}

	res, err := gh.ListCommits(r.Context(), repo.InstallationID, repo.Owner, repo.Name, github.ListCommitsOptions{
		PerPage:  commitRefreshPerPage,
		MaxPages: commitRefreshMaxPages,
		ETag:     etag,
	})
	if err != nil {
		return err
	}

	// 304: cache is current. Nothing to write.
	if res.NotModified {
		return nil
	}

	for _, c := range res.Commits {
		if c.SHA == "" {
			continue
		}
		if err := h.queries.UpsertRepositoryCommit(r.Context(), sqlc.UpsertRepositoryCommitParams{
			Owner:       repo.Owner,
			Name:        repo.Name,
			Sha:         c.SHA,
			Message:     toPgText(c.Message),
			AuthorName:  toPgText(c.AuthorName),
			AuthorEmail: toPgText(c.AuthorEmail),
			AuthoredAt:  timeToPg(c.AuthoredAt),
			CommittedAt: timeToPg(c.CommittedAt),
		}); err != nil {
			return err
		}
	}

	// Persist the new ETag so the next refresh can short-circuit on 304.
	if err := h.queries.UpdateCollectiveRepositorySync(r.Context(), sqlc.UpdateCollectiveRepositorySyncParams{
		GroupID:     repo.GroupID,
		Lower:       repo.Owner,
		Lower_2:     repo.Name,
		CommitsEtag: toPgText(res.ETag),
	}); err != nil {
		return err
	}
	return nil
}

// timeToPg converts a time.Time into pgtype.Timestamptz, treating the zero time
// as SQL NULL.
func timeToPg(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// parseGroupID parses the {id} path param as a group UUID.
func (h *Handler) parseGroupID(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return pgtype.UUID{}, false
	}
	return toPgUUID(id), true
}
