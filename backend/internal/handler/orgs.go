package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// ListUserPublicOrgs returns the visible GitHub orgs for a user by username.
// GET /users/{username}/orgs (AuthOptional)
func (h *Handler) ListUserPublicOrgs(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	if username == "" {
		writeError(w, http.StatusBadRequest, "Missing username")
		return
	}

	orgs, err := h.queries.GetUserVisibleOrgsByUsername(r.Context(), username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch orgs")
		return
	}
	writeJSON(w, http.StatusOK, orgs)
}

// GetUserPublicProfile returns a user's public profile by username.
// Hidden (is_discoverable=false) users return 404 to non-owners so their
// existence isn't confirmed via direct URL lookup.
// GET /users/{username} (AuthOptional)
func (h *Handler) GetUserPublicProfile(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	if username == "" {
		writeError(w, http.StatusBadRequest, "Missing username")
		return
	}

	target, err := h.queries.GetUserByUsername(r.Context(), username)
	if err != nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	if !target.IsDiscoverable {
		viewer := GetUser(r.Context())
		if viewer == nil || viewer.PgID() != target.ID {
			writeError(w, http.StatusNotFound, "User not found")
			return
		}
	}

	writeJSON(w, http.StatusOK, target)
}

// ListMyOrgs returns all GitHub orgs for the authenticated user (settings page).
// GET /auth/orgs (AuthRequired)
func (h *Handler) ListMyOrgs(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	orgs, err := h.queries.ListUserAllOrgs(r.Context(), user.PgID())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch orgs")
		return
	}
	writeJSON(w, http.StatusOK, orgs)
}

// SetOrgVisibility toggles the visibility of a GitHub org for the authenticated user.
// PATCH /auth/orgs/{orgLogin}/visibility (AuthRequired)
func (h *Handler) SetOrgVisibility(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	orgLogin := chi.URLParam(r, "orgLogin")
	if orgLogin == "" {
		writeError(w, http.StatusBadRequest, "Missing org login")
		return
	}

	var req struct {
		Visible bool `json:"visible"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	err := h.queries.SetOrgVisibility(r.Context(), sqlc.SetOrgVisibilityParams{
		UserID:   user.PgID(),
		OrgLogin: orgLogin,
		Visible:  req.Visible,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update visibility")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "updated",
		"visible": req.Visible,
	})
}

// SearchOrgs returns organizations whose login matches the query, with aggregate
// member and public-transcript counts.
// GET /orgs/search?q=<query>&limit=<n> (AuthOptional)
func (h *Handler) SearchOrgs(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]any{"organizations": []any{}})
		return
	}

	limit := int32(10)
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			if parsed > 50 {
				parsed = 50
			}
			limit = int32(parsed)
		}
	}

	orgs, err := h.queries.SearchOrgs(r.Context(), sqlc.SearchOrgsParams{
		Column1: pgtype.Text{String: query, Valid: true},
		Limit:   limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to search organizations")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"organizations": orgs})
}

// orgCollectiveSummary is the per-collective shape returned in GetOrg's
// new `collectives` field. We hand-shape it so JSON keys are stable
// regardless of the underlying sqlc row column ordering.
type orgCollectiveSummary struct {
	ID              pgtype.UUID `json:"id"`
	Name            string      `json:"name"`
	Description     pgtype.Text `json:"description"`
	MemberCount     int32       `json:"member_count"`
	TranscriptCount int32       `json:"transcript_count"`
}

// GetOrg returns an organization's aggregate stats, its members, and any
// collectives linked to it (visible to the caller).
// GET /orgs/{login} (AuthOptional)
func (h *Handler) GetOrg(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context()) // may be nil (AuthOptional)

	login := chi.URLParam(r, "login")
	if login == "" {
		writeError(w, http.StatusBadRequest, "Missing org login")
		return
	}

	stats, err := h.queries.GetOrgStats(r.Context(), login)
	if err != nil {
		writeError(w, http.StatusNotFound, "Organization not found")
		return
	}

	members, err := h.queries.ListOrgMembers(r.Context(), login)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch organization members")
		return
	}

	// Collectives linked to this org, filtered to those the caller can see.
	var callerID pgtype.UUID
	if user != nil {
		callerID = user.PgID()
	}
	rows, _ := h.queries.ListCollectivesByGitHubOrg(r.Context(), sqlc.ListCollectivesByGitHubOrgParams{
		Lower:  login,
		UserID: callerID,
	})
	collectives := make([]orgCollectiveSummary, 0, len(rows))
	for _, row := range rows {
		collectives = append(collectives, orgCollectiveSummary{
			ID:              row.ID,
			Name:            row.Name,
			Description:     row.Description,
			MemberCount:     row.MemberCount,
			TranscriptCount: row.TranscriptCount,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"org_login":        stats.OrgLogin,
		"avatar_url":       stats.AvatarUrl,
		"member_count":     stats.MemberCount,
		"transcript_count": stats.TranscriptCount,
		"members":          members,
		"collectives":      collectives,
	})
}
