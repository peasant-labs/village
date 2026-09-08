package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

func collectiveGroupedFailure(status int, operation, reason, recovery string) error {
	return &GroupedScopeError{Status: status, Message: fmt.Sprintf(
		"handler.%s refused the collective grouped read: %s; no transcript list or members were returned and nothing was changed; %s", operation, reason, recovery)}
}

func (h *Handler) registerCollectiveGroupedScopes() error {
	if err := h.RegisterGroupedScope(GroupedRouteCollective, h.resolveCollectiveGroupedScope); err != nil {
		return err
	}
	if err := h.RegisterGroupedScope(GroupedRouteContributable, h.resolveCollectiveGroupedScope); err != nil {
		return err
	}
	if err := h.RegisterGroupedScope(GroupedRoutePending, h.resolveCollectiveGroupedScope); err != nil {
		return err
	}
	return h.RegisterGroupedScope(GroupedRouteMyShares, h.resolveCollectiveGroupedScope)
}

// RegisterCollectiveBrowseRoutes mounts the same read handlers for flat and
// grouped clients and installs their predicates in the shared member service.
func (h *Handler) RegisterCollectiveBrowseRoutes(r chi.Router) {
	if err := h.registerCollectiveGroupedScopes(); err != nil {
		panic(err)
	}
	r.With(h.AuthOptional).Get("/groups/{id}", h.GetGroup)
	r.With(h.AuthRequired).Get("/groups/{id}/contributable", h.ListContributable)
	r.With(h.AuthRequired).Get("/groups/{id}/pending", h.ListPendingShares)
	r.With(h.AuthRequired).Get("/groups/{id}/my-shares", h.ListMyGroupShares)
}

// collectiveGroupedAccess deliberately uses the same role policies as the flat
// routes. In particular, my-shares remains the owner's own history even after
// leaving a collective; membership is not added as an accidental new barrier.
func (h *Handler) collectiveGroupedAccess(ctx context.Context, request GroupedScopeRequest) (sqlc.Group, string, error) {
	var empty sqlc.Group
	id, err := uuid.Parse(request.Collective)
	if err != nil {
		return empty, "", collectiveGroupedFailure(400, "collectiveGroupedAccess", "the collective identifier is invalid", "open the collective page and retry")
	}
	user := GetUser(ctx)
	viewerID := ""
	if user != nil {
		viewerID = uuid.UUID(user.PgID().Bytes).String()
	}
	if request.ViewerID != viewerID {
		return empty, "", ErrGroupScopeExpired
	}
	role := ""
	if user != nil {
		member, err := h.queries.GetGroupMember(ctx, sqlc.GetGroupMemberParams{GroupID: toPgUUID(id), UserID: user.PgID()})
		if err == nil {
			role = member.Role
		}
	}
	switch request.Variant {
	case GroupedRouteCollective:
	case GroupedRouteContributable:
		if user == nil || role == "" {
			return empty, role, collectiveGroupedFailure(403, "collectiveGroupedAccess", "current collective membership is required to contribute", "join the collective and refresh its contribute page")
		}
	case GroupedRoutePending:
		if user == nil || role != "owner" {
			return empty, role, collectiveGroupedFailure(403, "collectiveGroupedAccess", "current collective owner access is required to review submissions", "ask an owner to review, or refresh after your access is restored")
		}
	case GroupedRouteMyShares:
		if user == nil {
			return empty, role, collectiveGroupedFailure(401, "collectiveGroupedAccess", "your contributions require a signed-in owner", "sign in and refresh your contributions")
		}
	default:
		return empty, role, collectiveGroupedFailure(400, "collectiveGroupedAccess", "the originating route is not a collective variant", "refresh the originating list")
	}
	group, err := h.queries.GetGroupByID(ctx, toPgUUID(id))
	if err != nil {
		return empty, role, collectiveGroupedFailure(404, "collectiveGroupedAccess", "the collective could not be found", "refresh your collective list and choose an available collective")
	}
	if request.Variant == GroupedRouteContributable {
		if _, refusal := shareStatusForGroup(ctx, h.queries, user, group); refusal != nil {
			return empty, role, collectiveGroupedFailure(403, "collectiveGroupedAccess", refusal.Message, "refresh the contribute page after restoring eligibility")
		}
	}
	return group, role, nil
}

func parseCollectiveGroupedRequest(r *http.Request, variant GroupedRouteVariant) (GroupedScopeRequest, int, int, error) {
	request := GroupedScopeRequest{Variant: variant}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return request, 0, 0, collectiveGroupedFailure(400, "parseCollectiveGroupedRequest", "the collective identifier is invalid", "open the collective from its own page")
	}
	request.Collective = id.String()
	if user := GetUser(r.Context()); user != nil {
		request.ViewerID = uuid.UUID(user.PgID().Bytes).String()
	}
	query := r.URL.Query()
	for key, values := range query {
		switch key {
		case "view", "page", "limit", "q", "project_hash":
		default:
			return request, 0, 0, collectiveGroupedFailure(400, "parseCollectiveGroupedRequest", "an unsupported filter was supplied", "use only view, page, limit, q and project_hash on this list")
		}
		if len(values) != 1 {
			return request, 0, 0, collectiveGroupedFailure(400, "parseCollectiveGroupedRequest", "a query parameter was repeated", "send each filter once")
		}
	}
	request.Query = strings.TrimSpace(query.Get("q"))
	request.ProjectHash = query.Get("project_hash")
	if request.ProjectHash != "" && !projectHashPattern.MatchString(request.ProjectHash) {
		return request, 0, 0, collectiveGroupedFailure(400, "parseCollectiveGroupedRequest", "the project hash is invalid", "copy the exact project hash from your project page")
	}
	page, limit := 1, 20
	if raw := query.Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			return request, 0, 0, collectiveGroupedFailure(400, "parseCollectiveGroupedRequest", "page must be a positive integer", "request page 1 and retry")
		}
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return request, 0, 0, collectiveGroupedFailure(400, "parseCollectiveGroupedRequest", "limit must be an integer from 1 through 200", "request a supported page size")
		}
	}
	if page > int(^uint(0)>>1)/limit {
		return request, 0, 0, collectiveGroupedFailure(400, "parseCollectiveGroupedRequest", "the page offset exceeds the supported integer range", "restart from page 1")
	}
	return request, page, limit, nil
}

func writeCollectiveGroupedError(w http.ResponseWriter, err error) {
	WriteGroupedScopeError(w, err)
}

func (h *Handler) listCollectiveGrouped(w http.ResponseWriter, r *http.Request, variant GroupedRouteVariant) {
	request, page, limit, err := parseCollectiveGroupedRequest(r, variant)
	if err != nil {
		writeCollectiveGroupedError(w, err)
		return
	}
	list, err := h.GroupedList(r.Context(), request, page, limit)
	if err != nil {
		writeCollectiveGroupedError(w, err)
		return
	}
	if variant == GroupedRouteContributable {
		writeJSON(w, http.StatusOK, schema.VillageGroupedContributableResponse{GroupID: schema.VillageUUID(request.Collective), TranscriptList: list})
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// nullableGroupedViewer returns only the current authenticated viewer, never an
// ID supplied by a cache token or a query string.
func nullableGroupedViewer(ctx context.Context) pgtype.UUID {
	if user := GetUser(ctx); user != nil {
		return user.PgID()
	}
	return pgtype.UUID{}
}

func (h *Handler) resolveCollectiveGroupedScope(ctx context.Context, request GroupedScopeRequest) (GroupedScopeResult, error) {
	result := GroupedScopeResult{Rows: []schema.VillageSessionRow{}}
	if request.SelectionRevision != "" {
		return result, ErrGroupScopeExpired
	}
	group, role, err := h.collectiveGroupedAccess(ctx, request)
	if err != nil {
		return result, err
	}
	if request.Variant == GroupedRouteCollective && !canReadData(role, group.DataAccess) {
		return result, collectiveGroupedFailure(403, "resolveCollectiveGroupedScope", "current collective data access does not permit this read", "refresh the collective page after your access is restored")
	}
	if h.pool == nil {
		return result, collectiveGroupedFailure(500, "resolveCollectiveGroupedScope", "PostgreSQL is not configured", "configure the database through the handler constructor and retry")
	}
	if request.Variant == GroupedRouteContributable && h.contributableRowLimit <= 0 {
		return result, collectiveGroupedFailure(500, "resolveCollectiveGroupedScope", "the contribute row bound is not configured", "construct the handler with its contribute row limit and retry")
	}
	route := string(request.Variant)
	if request.Variant == GroupedRouteMyShares {
		route = "my-shares"
	}
	rows, err := sqlc.New(h.pool).ListCollectiveGroupedCandidates(ctx, sqlc.ListCollectiveGroupedCandidatesParams{
		RouteKind: route, GroupID: group.ID, ViewerID: nullableGroupedViewer(ctx),
		ProjectHash: request.ProjectHash, Search: request.Query,
	})
	if err != nil {
		return result, collectiveGroupedFailure(500, "resolveCollectiveGroupedScope", "the scoped candidate query failed", "retry after checking that database migrations and query generation match this server")
	}
	if request.Variant == GroupedRouteContributable && len(rows) > h.contributableRowLimit {
		return result, collectiveGroupedFailure(413, "resolveCollectiveGroupedScope", "the eligible candidate set exceeds the configured contribute row bound", "filter to a project before retrying")
	}
	keys := make([]projectIdentityKey, 0, len(rows))
	seen := map[projectIdentityKey]bool{}
	for _, row := range rows {
		key := projectIdentityKey{OwnerID: row.Transcript.OwnerID, ProjectHash: row.Transcript.ProjectHash}
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	projects := h.resolveProjectIdentities(ctx, keys)
	for _, row := range rows {
		key := projectIdentityKey{OwnerID: row.Transcript.OwnerID, ProjectHash: row.Transcript.ProjectHash}
		session, err := groupedSessionFromTranscript(row.Transcript, projects[key])
		if err != nil {
			return result, collectiveGroupedFailure(500, "resolveCollectiveGroupedScope", "stored transcript metadata could not be projected into the canonical response", "retry after repairing the stored metadata through an explicit owner update")
		}
		var projected schema.VillageSessionRow
		switch request.Variant {
		case GroupedRouteCollective:
			projected = collectiveSessionRow(session, row.OwnerUsername, pgTextPointer(row.OwnerAvatarUrl), row.OwnerIsDiscoverable)
		case GroupedRoutePending:
			projected = pendingSessionRow(session, row.OwnerUsername, row.OwnerIsDiscoverable, row.SharedAt.Time)
		case GroupedRouteMyShares:
			projected = myShareSessionRow(session, schema.VillageShareStatus(row.ShareStatus), row.SharedAt.Time)
		case GroupedRouteContributable:
			projected = contributableSessionRow(session, row.AlreadyShared)
		}
		if err := projected.Validate(); err != nil {
			return result, err
		}
		result.Rows = append(result.Rows, projected)
	}
	return result, nil
}
