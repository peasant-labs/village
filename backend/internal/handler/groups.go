package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

var validAcceptanceModes = map[string]bool{
	"open":          true,
	"verified_only": true,
	"curated":       true,
}

var validDataAccess = map[string]bool{
	"members_only": true,
	"contributors": true,
	"public":       true,
}

var validDeletionPolicies = map[string]bool{
	"user_choice": true,
	"mandatory":   true,
}

func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	var req struct {
		Name            string `json:"name"`
		Description     string `json:"description"`
		AcceptanceMode  string `json:"acceptance_mode"`
		DataAccess      string `json:"data_access"`
		LinkedGitHubOrg string `json:"linked_github_org"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "Name is required")
		return
	}
	if req.AcceptanceMode != "" && !validAcceptanceModes[req.AcceptanceMode] {
		writeError(w, http.StatusBadRequest, "Invalid acceptance mode")
		return
	}
	if req.DataAccess != "" && !validDataAccess[req.DataAccess] {
		writeError(w, http.StatusBadRequest, "Invalid data access policy")
		return
	}

	acceptanceMode := req.AcceptanceMode
	if acceptanceMode == "" {
		acceptanceMode = "open"
	}
	dataAccess := req.DataAccess
	if dataAccess == "" {
		dataAccess = "members_only"
	}

	linkedOrg := pgtype.Text{Valid: false}
	if trimmed := strings.TrimSpace(req.LinkedGitHubOrg); trimmed != "" {
		// Caller must currently have this org marked visible.
		ok, err := h.queries.HasUserVisibleOrg(r.Context(), sqlc.HasUserVisibleOrgParams{
			UserID: user.PgID(),
			Lower:  trimmed,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to verify org membership")
			return
		}
		if !ok {
			writeError(w, http.StatusBadRequest, "You must have the GitHub organization marked visible to link this collective to it")
			return
		}
		linkedOrg = pgtype.Text{String: trimmed, Valid: true}
	}

	group, err := h.queries.CreateGroup(r.Context(), sqlc.CreateGroupParams{
		Name:            req.Name,
		Description:     toPgText(req.Description),
		CreatedBy:       user.PgID(),
		AcceptanceMode:  acceptanceMode,
		DataAccess:      dataAccess,
		LinkedGithubOrg: linkedOrg,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create group")
		return
	}

	h.queries.AddGroupMember(r.Context(), sqlc.AddGroupMemberParams{
		GroupID: group.ID,
		UserID:  user.PgID(),
		Role:    "owner",
	})

	writeJSON(w, http.StatusCreated, group)
}

func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	groups, err := h.queries.ListUserGroups(r.Context(), user.PgID())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list groups")
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (h *Handler) ListPublicGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.queries.ListAllGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list groups")
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

// collectiveSummary is the wire shape returned by SearchCollectives and the
// extended GetOrg response. We hand-shape it so JSON keys are stable
// regardless of the underlying sqlc row column ordering.
type collectiveSummary struct {
	ID              pgtype.UUID `json:"id"`
	Name            string      `json:"name"`
	Description     pgtype.Text `json:"description"`
	LinkedGithubOrg pgtype.Text `json:"linked_github_org"`
	MemberCount     int32       `json:"member_count"`
	TranscriptCount int32       `json:"transcript_count"`
}

// SearchCollectives returns collectives matching the query, filtered to those
// the caller can see. GET /groups/search?q=<query>&limit=<n> (AuthOptional).
func (h *Handler) SearchCollectives(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context()) // may be nil (AuthOptional)

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusOK, map[string]any{"collectives": []any{}})
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

	// Caller user_id: pgtype.UUID with Valid: false serializes to NULL for the
	// "OR caller is a member" branch, so unauthenticated callers only see
	// public / open collectives.
	var callerID pgtype.UUID
	if user != nil {
		callerID = user.PgID()
	}

	rows, err := h.queries.SearchCollectives(r.Context(), sqlc.SearchCollectivesParams{
		Column1: pgtype.Text{String: query, Valid: true},
		UserID:  callerID,
		Limit:   limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to search collectives")
		return
	}

	out := make([]collectiveSummary, 0, len(rows))
	for _, row := range rows {
		out = append(out, collectiveSummary{
			ID:              row.ID,
			Name:            row.Name,
			Description:     row.Description,
			LinkedGithubOrg: row.LinkedGithubOrg,
			MemberCount:     row.MemberCount,
			TranscriptCount: row.TranscriptCount,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"collectives": out})
}

// canReadData determines whether a user with the given role can browse the
// group's pooled dataset based on the group's data_access policy.
func canReadData(role string, dataAccess string) bool {
	switch dataAccess {
	case "public":
		return true
	case "contributors":
		return role == "contributor" || role == "member" || role == "owner"
	default: // "members_only"
		return role == "member" || role == "owner"
	}
}

func (h *Handler) GetGroup(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context()) // may be nil (AuthOptional)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}

	pgID := toPgUUID(id)

	group, err := h.queries.GetGroupByID(r.Context(), pgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Group not found")
		return
	}

	// Determine caller's role in the group
	yourRole := ""
	if user != nil {
		member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
			GroupID: pgID,
			UserID:  user.PgID(),
		})
		if err == nil {
			yourRole = member.Role
		}
	}

	canRead := canReadData(yourRole, group.DataAccess)

	members, _ := h.queries.ListGroupMembers(r.Context(), sqlc.ListGroupMembersParams{
		GroupID:       pgID,
		ViewerIsOwner: yourRole == "owner",
	})
	stats, _ := h.queries.GetGroupTranscriptStats(r.Context(), pgID)
	models, _ := h.queries.ListGroupModelBreakdown(r.Context(), pgID)
	contributors, _ := h.queries.ListGroupContributors(r.Context(), sqlc.ListGroupContributorsParams{
		GroupID:       pgID,
		ViewerIsOwner: yourRole == "owner",
	})

	resp := map[string]any{
		"group":        group,
		"members":      members,
		"stats":        stats,
		"models":       models,
		"contributors": contributors,
		"can_read":     canRead,
		"your_role":    yourRole,
	}

	if yourRole == "owner" {
		pendingMembers, _ := h.queries.ListGroupPendingMembers(r.Context(), pgID)
		resp["pending_members"] = pendingMembers
	}

	// Only include transcripts if the caller can read data
	if canRead {
		limit := int32(20)
		if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
			limit = int32(l)
		}
		offset := int32(0)
		if o, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && o >= 0 {
			offset = int32(o)
		}
		transcriptRows, _ := h.queries.ListGroupTranscripts(r.Context(), sqlc.ListGroupTranscriptsParams{
			GroupID: pgID,
			Limit:   limit,
			Offset:  offset,
		})
		transcripts := make([]groupTranscriptResponse, 0, len(transcriptRows))
		for _, row := range transcriptRows {
			transcripts = append(transcripts, groupTranscriptFromRow(row))
		}
		resp["transcripts"] = transcripts
	} else {
		resp["transcripts"] = []any{}
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}

	pgID := toPgUUID(id)
	member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
		GroupID: pgID,
		UserID:  user.PgID(),
	})
	if err != nil || member.Role != "owner" {
		writeError(w, http.StatusForbidden, "Owner access required")
		return
	}

	var req struct {
		Name                     string  `json:"name"`
		Description              string  `json:"description"`
		DataAccess               string  `json:"data_access"`
		AcceptanceMode           string  `json:"acceptance_mode"`
		LinkedGitHubOrg          *string `json:"linked_github_org"`
		DisplayMembers           *bool   `json:"display_members"`
		TranscriptDeletionPolicy string  `json:"transcript_deletion_policy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.DataAccess != "" && !validDataAccess[req.DataAccess] {
		writeError(w, http.StatusBadRequest, "Invalid data access policy")
		return
	}
	if req.AcceptanceMode != "" && !validAcceptanceModes[req.AcceptanceMode] {
		writeError(w, http.StatusBadRequest, "Invalid acceptance mode")
		return
	}
	if req.TranscriptDeletionPolicy != "" && !validDeletionPolicies[req.TranscriptDeletionPolicy] {
		writeError(w, http.StatusBadRequest, "Invalid transcript deletion policy")
		return
	}

	// Fetch current group to use existing values if not provided.
	currentGroup, err := h.queries.GetGroupByID(r.Context(), pgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Group not found")
		return
	}
	dataAccess := req.DataAccess
	if dataAccess == "" {
		dataAccess = currentGroup.DataAccess
	}
	acceptanceMode := req.AcceptanceMode
	if acceptanceMode == "" {
		acceptanceMode = currentGroup.AcceptanceMode
	}
	displayMembers := currentGroup.DisplayMembers
	if req.DisplayMembers != nil {
		displayMembers = *req.DisplayMembers
	}
	deletionPolicy := currentGroup.TranscriptDeletionPolicy
	if req.TranscriptDeletionPolicy != "" {
		deletionPolicy = req.TranscriptDeletionPolicy
	}

	// linked_github_org: nil pointer => no change; non-nil empty => clear;
	// non-nil non-empty => set (after validation against caller's visible orgs).
	linkedOrg := currentGroup.LinkedGithubOrg
	if req.LinkedGitHubOrg != nil {
		trimmed := strings.TrimSpace(*req.LinkedGitHubOrg)
		if trimmed == "" {
			linkedOrg = pgtype.Text{Valid: false}
		} else {
			ok, err := h.queries.HasUserVisibleOrg(r.Context(), sqlc.HasUserVisibleOrgParams{
				UserID: user.PgID(),
				Lower:  trimmed,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "Failed to verify org membership")
				return
			}
			if !ok {
				writeError(w, http.StatusBadRequest, "You must have the GitHub organization marked visible to link this collective to it")
				return
			}
			linkedOrg = pgtype.Text{String: trimmed, Valid: true}
		}
	}

	group, err := h.queries.UpdateGroup(r.Context(), sqlc.UpdateGroupParams{
		ID:                       pgID,
		Name:                     req.Name,
		Description:              toPgText(req.Description),
		DataAccess:               dataAccess,
		AcceptanceMode:           acceptanceMode,
		LinkedGithubOrg:          linkedOrg,
		DisplayMembers:           displayMembers,
		TranscriptDeletionPolicy: deletionPolicy,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update group")
		return
	}

	writeJSON(w, http.StatusOK, group)
}

func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}

	pgID := toPgUUID(id)
	member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
		GroupID: pgID,
		UserID:  user.PgID(),
	})
	if err != nil || member.Role != "owner" {
		writeError(w, http.StatusForbidden, "Owner access required")
		return
	}

	if err := h.queries.DeleteGroup(r.Context(), pgID); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete group")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) AddGroupMember(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}

	pgID := toPgUUID(id)
	member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
		GroupID: pgID,
		UserID:  user.PgID(),
	})
	if err != nil || member.Role != "owner" {
		writeError(w, http.StatusForbidden, "Owner access required")
		return
	}

	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	targetUser, err := h.queries.GetUserByUsername(r.Context(), req.Username)
	if err != nil {
		writeError(w, http.StatusNotFound, "@"+req.Username+" hasn't joined the platform yet. They need to sign in first.")
		return
	}

	err = h.queries.AddGroupMember(r.Context(), sqlc.AddGroupMemberParams{
		GroupID: pgID,
		UserID:  targetUser.ID,
		Role:    "member",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to add member")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "added"})
}

// ListPendingShares returns pending transcript shares for a curated collective.
// GET /groups/{id}/pending (AuthRequired, owner only)
func (h *Handler) ListPendingShares(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}

	pgID := toPgUUID(id)
	member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
		GroupID: pgID,
		UserID:  user.PgID(),
	})
	if err != nil || member.Role != "owner" {
		writeError(w, http.StatusForbidden, "Owner access required")
		return
	}

	pending, err := h.queries.ListPendingGroupShares(r.Context(), pgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list pending shares")
		return
	}
	writeJSON(w, http.StatusOK, pending)
}

// ReviewShare approves or rejects a pending transcript share.
// PATCH /groups/{id}/shares/{transcriptID} (AuthRequired, owner only)
func (h *Handler) ReviewShare(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}
	transcriptID, err := uuid.Parse(chi.URLParam(r, "transcriptID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}

	pgID := toPgUUID(id)
	member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
		GroupID: pgID,
		UserID:  user.PgID(),
	})
	if err != nil || member.Role != "owner" {
		writeError(w, http.StatusForbidden, "Owner access required")
		return
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Status != "approved" && req.Status != "rejected" {
		writeError(w, http.StatusBadRequest, "Status must be 'approved' or 'rejected'")
		return
	}

	// decided_by records the moderator on the attempt itself. Sharing is not a
	// licence or visibility change, so it does not cross the governance-audit
	// axis and needs no actor GUC; the decision is attributed on the row.
	err = h.queries.UpdateShareStatus(r.Context(), sqlc.UpdateShareStatusParams{
		TranscriptID: toPgUUID(transcriptID),
		GroupID:      pgID,
		Status:       req.Status,
		DecidedBy:    user.PgID(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update share status")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

// JoinGroup allows a logged-in user to self-join an open-acceptance collective
// as a contributor. POST /groups/{id}/join (AuthRequired)
func (h *Handler) JoinGroup(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}

	pgID := toPgUUID(id)
	group, err := h.queries.GetGroupByID(r.Context(), pgID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Group not found")
		return
	}

	switch group.AcceptanceMode {
	case "open":
		// anyone may join
	case "verified_only":
		// If the collective is linked to a specific GitHub org, require
		// the user to have THAT org marked visible. Otherwise require any
		// visible org.
		if group.LinkedGithubOrg.Valid && group.LinkedGithubOrg.String != "" {
			ok, err := h.queries.HasUserVisibleOrg(r.Context(), sqlc.HasUserVisibleOrgParams{
				UserID: user.PgID(),
				Lower:  group.LinkedGithubOrg.String,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "Failed to verify org membership")
				return
			}
			if !ok {
				writeError(w, http.StatusForbidden, "This collective requires verified membership in @"+group.LinkedGithubOrg.String)
				return
			}
		} else {
			visOrgs, _ := h.queries.ListUserVisibleOrgs(r.Context(), user.PgID())
			if len(visOrgs) == 0 {
				writeError(w, http.StatusForbidden, "This collective requires at least one verified GitHub organization")
				return
			}
		}
	default:
		writeError(w, http.StatusForbidden, "This collective requires an invitation to join")
		return
	}

	// Check if already a member
	_, err = h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
		GroupID: pgID,
		UserID:  user.PgID(),
	})
	if err == nil {
		writeError(w, http.StatusConflict, "Already a member of this collective")
		return
	}

	err = h.queries.AddGroupMember(r.Context(), sqlc.AddGroupMemberParams{
		GroupID: pgID,
		UserID:  user.PgID(),
		Role:    "contributor",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to join group")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "joined", "role": "contributor"})
}

// PromoteMember changes a member's role. Only owners can promote/demote.
// PATCH /groups/{id}/members/{userID}/role (AuthRequired)
func (h *Handler) PromoteMember(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	pgID := toPgUUID(id)

	// Verify caller is owner
	member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
		GroupID: pgID,
		UserID:  user.PgID(),
	})
	if err != nil || member.Role != "owner" {
		writeError(w, http.StatusForbidden, "Owner access required")
		return
	}

	var req struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Role != "contributor" && req.Role != "member" {
		writeError(w, http.StatusBadRequest, "Role must be 'contributor' or 'member'")
		return
	}

	// Verify target is actually a member of the group
	pgTargetID := toPgUUID(targetID)
	_, err = h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
		GroupID: pgID,
		UserID:  pgTargetID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "User is not a member of this group")
		return
	}

	err = h.queries.UpdateMemberRole(r.Context(), sqlc.UpdateMemberRoleParams{
		GroupID: pgID,
		UserID:  pgTargetID,
		Role:    req.Role,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update role")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "role": req.Role})
}

// RemoveGroupTranscript revokes a transcript's contribution to a collective.
// The transcript itself survives in its owner's library; only the
// transcript_shares row is deleted. Owner-only.
// DELETE /groups/{id}/transcripts/{transcriptID} (AuthRequired)
func (h *Handler) RemoveGroupTranscript(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}
	transcriptID, err := uuid.Parse(chi.URLParam(r, "transcriptID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid transcript ID")
		return
	}

	pgID := toPgUUID(id)
	member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
		GroupID: pgID,
		UserID:  user.PgID(),
	})
	if err != nil || member.Role != "owner" {
		writeError(w, http.StatusForbidden, "Owner access required")
		return
	}

	if err := h.queries.RemoveGroupTranscript(r.Context(), sqlc.RemoveGroupTranscriptParams{
		GroupID:      pgID,
		TranscriptID: toPgUUID(transcriptID),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to remove transcript from collective")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (h *Handler) RemoveGroupMember(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid user ID")
		return
	}

	pgID := toPgUUID(id)
	pgTargetID := toPgUUID(targetID)

	if targetID != user.ID {
		member, err := h.queries.GetGroupMember(r.Context(), sqlc.GetGroupMemberParams{
			GroupID: pgID,
			UserID:  user.PgID(),
		})
		if err != nil || member.Role != "owner" {
			writeError(w, http.StatusForbidden, "Owner access required")
			return
		}
	}

	// Resolve whether the departing user's transcripts should be retracted.
	// Mandatory policy always wins; otherwise the client passes ?retract=true
	// to opt in (from the leave-collective modal).
	retract := false
	if r.URL.Query().Get("retract") == "true" {
		retract = true
	}
	if group, err := h.queries.GetGroupByID(r.Context(), pgID); err == nil {
		if group.TranscriptDeletionPolicy == "mandatory" {
			retract = true
		}
	}

	err = h.queries.RemoveGroupMember(r.Context(), sqlc.RemoveGroupMemberParams{
		GroupID: pgID,
		UserID:  pgTargetID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to remove member")
		return
	}

	if retract {
		if err := h.queries.RetractUserSharesInGroup(r.Context(), sqlc.RetractUserSharesInGroupParams{
			GroupID: pgID,
			OwnerID: pgTargetID,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "Removed member but failed to retract transcripts")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "removed",
		"retracted": retract,
	})
}

// ListMyGroupShares returns the calling user's own transcripts contributed to
// the given collective (both approved and pending). Used by the "Your
// contributions" card and the leave-collective modal.
// GET /groups/{id}/my-shares (AuthRequired)
func (h *Handler) ListMyGroupShares(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid group ID")
		return
	}

	rows, err := h.queries.ListUserSharesInGroup(r.Context(), sqlc.ListUserSharesInGroupParams{
		GroupID: toPgUUID(id),
		OwnerID: user.PgID(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list your shares")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}
