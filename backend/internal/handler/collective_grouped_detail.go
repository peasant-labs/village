package handler

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func (h *Handler) getCollectiveGrouped(w http.ResponseWriter, r *http.Request) {
	request, page, limit, err := parseCollectiveGroupedRequest(r, GroupedRouteCollective)
	if err != nil {
		writeCollectiveGroupedError(w, err)
		return
	}
	group, role, err := h.collectiveGroupedAccess(r.Context(), request)
	if err != nil {
		writeCollectiveGroupedError(w, err)
		return
	}
	canRead := canReadData(role, group.DataAccess)
	list := schema.VillageSessionListPayload{Items: []schema.VillageSessionListItem{}, Page: page, Limit: limit}
	if canRead {
		list, err = h.GroupedList(r.Context(), request, page, limit)
		if err != nil {
			writeCollectiveGroupedError(w, err)
			return
		}
	}
	response, err := h.collectiveGroupedDetail(r, group, role, canRead, list)
	if err != nil {
		writeCollectiveGroupedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// collectiveGroupedDetail retains the existing collective body. Only its
// transcript collection is replaced by a grouped page. Roster visibility and
// pending-member visibility still follow the same owner flag as the flat route.
func (h *Handler) collectiveGroupedDetail(r *http.Request, group sqlc.Group, role string, canRead bool, list schema.VillageSessionListPayload) (schema.VillageGroupedGroupDetailResponse, error) {
	response := schema.VillageGroupedGroupDetailResponse{
		Group: schema.VillageGroup{
			ID: schema.VillageUUID(uuid.UUID(group.ID.Bytes).String()), Name: group.Name,
			Description: pgTextPointer(group.Description), CreatedBy: schema.VillageUUID(uuid.UUID(group.CreatedBy.Bytes).String()),
			CreatedAt: group.CreatedAt.Time, UpdatedAt: group.UpdatedAt.Time,
			AcceptanceMode: schema.VillageGroupAcceptanceMode(group.AcceptanceMode),
			DataAccess:     schema.VillageGroupDataAccess(group.DataAccess), LinkedGithubOrg: pgTextPointer(group.LinkedGithubOrg),
			DisplayMembers: group.DisplayMembers, TranscriptDeletionPolicy: schema.VillageTranscriptDeletionPolicy(group.TranscriptDeletionPolicy),
		},
		Members: []schema.VillageGroupMember{}, Models: []schema.VillageGroupModelBreakdown{},
		Contributors: []schema.VillageGroupContributor{}, CanRead: canRead,
		YourRole: schema.VillageGroupViewerRole(role), TranscriptList: list,
	}
	failure := func() (schema.VillageGroupedGroupDetailResponse, error) {
		return response, collectiveGroupedFailure(500, "collectiveGroupedDetail", "collective metadata could not be read", "retry the collective page; a failed read is not an empty roster or dataset")
	}
	ctx := r.Context()
	members, err := h.queries.ListGroupMembers(ctx, sqlc.ListGroupMembersParams{GroupID: group.ID, ViewerIsOwner: role == "owner"})
	if err != nil {
		return failure()
	}
	for _, row := range members {
		orgs := append([]string{}, row.GithubOrgs...)
		response.Members = append(response.Members, schema.VillageGroupMember{
			Role: schema.VillageGroupRole(row.Role), JoinedAt: row.JoinedAt.Time,
			ID: schema.VillageUUID(uuid.UUID(row.ID.Bytes).String()), GithubUsername: row.GithubUsername,
			DisplayName: pgTextPointer(row.DisplayName), AvatarURL: pgTextPointer(row.AvatarUrl), GithubOrgs: orgs,
		})
	}
	stats, err := h.queries.GetGroupTranscriptStats(ctx, group.ID)
	if err != nil {
		return failure()
	}
	response.Stats = schema.VillageGroupTranscriptStats{TotalTranscripts: stats.TotalTranscripts,
		ContributorCount: stats.ContributorCount, TotalTurns: stats.TotalTurns, TotalDurationMs: stats.TotalDurationMs, TotalTokens: stats.TotalTokens}
	models, err := h.queries.ListGroupModelBreakdown(ctx, group.ID)
	if err != nil {
		return failure()
	}
	for _, row := range models {
		response.Models = append(response.Models, schema.VillageGroupModelBreakdown{ModelProvider: row.ModelProvider, TranscriptCount: row.TranscriptCount})
	}
	contributors, err := h.queries.ListGroupContributors(ctx, sqlc.ListGroupContributorsParams{GroupID: group.ID, ViewerIsOwner: role == "owner"})
	if err != nil {
		return failure()
	}
	for _, row := range contributors {
		response.Contributors = append(response.Contributors, schema.VillageGroupContributor{
			ID: schema.VillageUUID(uuid.UUID(row.ID.Bytes).String()), GithubUsername: row.GithubUsername,
			AvatarURL: pgTextPointer(row.AvatarUrl), TranscriptCount: row.TranscriptCount,
		})
	}
	if role == "owner" {
		pending, err := h.queries.ListGroupPendingMembers(ctx, group.ID)
		if err != nil {
			return failure()
		}
		response.PendingMembers = []schema.VillageGroupMember{}
		for _, row := range pending {
			response.PendingMembers = append(response.PendingMembers, schema.VillageGroupMember{
				Role: schema.VillageGroupRole(row.Role), JoinedAt: row.JoinedAt.Time,
				ID: schema.VillageUUID(uuid.UUID(row.ID.Bytes).String()), GithubUsername: row.GithubUsername,
				DisplayName: pgTextPointer(row.DisplayName), AvatarURL: pgTextPointer(row.AvatarUrl), GithubOrgs: append([]string{}, row.GithubOrgs...),
			})
		}
	}
	return response, response.Validate()
}
