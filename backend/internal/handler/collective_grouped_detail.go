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
// The pull-request check settings are the same stored row facts the flat route
// serves, so the grouped body carries them too instead of dropping them.
func (h *Handler) collectiveGroupedDetail(r *http.Request, group sqlc.Group, role string, canRead bool, list schema.VillageSessionListPayload) (schema.VillageGroupedCollectiveDetailResponse, error) {
	promptsCheckMode := schema.VillagePromptsCheckMode(group.PromptsCheckMode)
	response := schema.VillageGroupedCollectiveDetailResponse{
		Group: schema.VillageCollectiveDetailRecord{
			ID: schema.VillageUUID(uuid.UUID(group.ID.Bytes).String()), Name: group.Name,
			Description: pgTextPointer(group.Description),
			CreatedAt:   group.CreatedAt.Time, UpdatedAt: group.UpdatedAt.Time,
			AcceptanceMode: schema.VillageCollectiveAcceptanceMode(group.AcceptanceMode),
			DataAccess:     schema.VillageCollectiveDataAccess(group.DataAccess), LinkedGithubOrg: pgTextPointer(group.LinkedGithubOrg),
			DisplayMembers: group.DisplayMembers, TranscriptDeletionPolicy: schema.VillageTranscriptDeletionPolicy(group.TranscriptDeletionPolicy),
			PostPromptsCheck: &group.PostPromptsCheck, PromptsCheckMode: &promptsCheckMode,
		},
		Members: []schema.VillageCollectiveMember{}, Models: []schema.VillageCollectiveModelBreakdown{},
		Contributors: []schema.VillageCollectiveContributor{}, CanRead: canRead,
		YourRole: schema.VillageCollectiveViewerRole(role), TranscriptList: list,
	}
	// A stored mode outside the closed menu is refused rather than replaced by a
	// guess, because a client cannot interpret it and a substituted setting would
	// silently change the collective's pull-request behaviour.
	if !promptsCheckMode.IsValid() {
		return response, collectiveGroupedFailure(500, "collectiveGroupedDetail", "the stored pull-request check mode is outside the supported menu", "repair the collective's pull-request check setting through an explicit owner update and retry")
	}
	failure := func() (schema.VillageGroupedCollectiveDetailResponse, error) {
		return response, collectiveGroupedFailure(500, "collectiveGroupedDetail", "collective metadata could not be read", "retry the collective page; a failed read is not an empty roster or dataset")
	}
	ctx := r.Context()
	creator, err := h.resolvePublicOwner(ctx, GetUser(ctx), group.CreatedBy)
	if err != nil {
		return failure()
	}
	response.Group.Creator = creator
	people, err := h.collectivePeopleFor(ctx, GetUser(ctx), group.ID, role == "owner")
	if err != nil {
		return failure()
	}
	response.Members = people.Members
	response.Contributors = people.Contributors
	response.PendingMembers = people.PendingMembers
	stats, err := h.queries.GetGroupTranscriptStats(ctx, group.ID)
	if err != nil {
		return failure()
	}
	pullRequestCount, err := h.collectivePullRequestCountFor(ctx, GetUser(ctx), group.ID, canRead)
	if err != nil {
		return failure()
	}
	response.Stats = schema.VillageCollectiveTranscriptStats{TotalTranscripts: stats.TotalTranscripts,
		ContributorCount: stats.ContributorCount, TotalTurns: stats.TotalTurns, TotalDurationMs: stats.TotalDurationMs, TotalTokens: stats.TotalTokens,
		PullRequestCount: pullRequestCount}
	models, err := h.queries.ListGroupModelBreakdown(ctx, group.ID)
	if err != nil {
		return failure()
	}
	for _, row := range models {
		response.Models = append(response.Models, schema.VillageCollectiveModelBreakdown{ModelProvider: row.ModelProvider, TranscriptCount: row.TranscriptCount})
	}
	return response, response.Validate()
}
