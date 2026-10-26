package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
)

// RegisterTranscriptBrowseRoutes mounts both legacy and opt-in grouped browsing
// and the one shared member endpoint. Other route families share this Handler.
func (h *Handler) RegisterTranscriptBrowseRoutes(r chi.Router) {
	for _, variant := range []GroupedRouteVariant{GroupedRouteTranscripts, GroupedRouteProfile, GroupedRouteProject} {
		if err := h.RegisterGroupedScope(variant, h.resolveGroupedTranscripts); err != nil {
			panic(err)
		}
	}
	r.With(h.AuthOptional).Get("/transcripts", h.ListTranscripts)
	r.With(h.AuthOptional).Get("/transcript-groups/{groupId}/members", h.ListHelperMembers)
}

func (h *Handler) listGroupedTranscripts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if _, _, err := parseListOriginScope(q.Get("origin")); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	request := GroupedScopeRequest{
		Variant: GroupedRouteTranscripts, Owner: q.Get("owner"), Project: q.Get("project"), ProjectHash: q.Get("project_hash"),
		Query: q.Get("q"), Provider: q.Get("provider"), Repository: q.Get("repo"), Org: q.Get("org"), Origin: q.Get("origin"), Sort: q.Get("sort"),
	}
	if request.Owner != "" {
		request.Variant = GroupedRouteProfile
	}
	if request.ProjectHash != "" {
		request.Variant = GroupedRouteProject
	}
	if q.Get("tags") != "" {
		for _, tag := range strings.Split(q.Get("tags"), ",") {
			request.Tags = append(request.Tags, strings.TrimSpace(tag))
		}
		sort.Strings(request.Tags)
	}
	page, limit := groupedQueryPage(q)
	payload, err := h.GroupedList(r.Context(), request, page, limit)
	if err != nil {
		WriteGroupedScopeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// groupedSessionFromTranscript is the one canonical SQL transcript row to
// canonical session projection for grouped reads. Every grouped resolver
// projects through it so list, collective, and member surfaces cannot render
// contradictory identity, count, or graph values for one stored row. The
// nullable graph/count columns carry the validated durable projection, never
// inferred parent availability or a re-count of the transcript's turns.
func groupedSessionFromTranscript(row sqlc.Transcript, resolved projectname.Resolved) (schema.VillageTranscript, error) {
	var session schema.VillageTranscript
	transcriptID := uuidFromPg(row.ID).String()
	// Reuse the existing public projection and its privacy policy. JSONB
	// fields must remain JSON rather than the []byte base64 representation.
	projected := listTranscriptResponse(row, resolved)
	encoded, err := json.Marshal(struct {
		transcriptResponse
		Subagents json.RawMessage `json:"subagents"`
		Warnings  json.RawMessage `json:"diagnostics_warnings"`
	}{projected, row.Subagents, row.DiagnosticsWarnings})
	if err != nil {
		return session, fmt.Errorf("handler.groupedSessionFromTranscript: stored transcript metadata for transcript %s could not be encoded during grouped projection; no rows returned; retry after the deployed contract and the stored metadata agree: %w", transcriptID, err)
	}
	if err := json.Unmarshal(encoded, &session); err != nil {
		return session, fmt.Errorf("handler.groupedSessionFromTranscript: encoded transcript metadata for transcript %s could not be decoded into the canonical session during grouped projection; no rows returned; retry after the deployed contract and the stored metadata agree: %w", transcriptID, err)
	}
	// Nullable graph/count columns are the validated durable projection, not
	// inferred parent availability or a re-count of the transcript's turns.
	if row.InputSubmissionCount.Valid {
		value := row.InputSubmissionCount.Int64
		session.InputSubmissionCount = &value
	}
	if row.RootSessionID.Valid {
		value := schema.SessionID(row.RootSessionID.String)
		session.RootSessionID = &value
	}
	session.Purpose = schema.SessionPurpose(row.SessionPurpose.String)
	if err := json.Unmarshal(row.SessionRelationships, &session.Relationships); err != nil {
		return session, fmt.Errorf("handler.groupedSessionFromTranscript: stored session relationships for transcript %s could not be decoded during grouped projection; no rows returned; retry after the deployed contract and the stored metadata agree: %w", transcriptID, err)
	}
	return session, nil
}

func (h *Handler) resolveGroupedTranscripts(ctx context.Context, request GroupedScopeRequest) (GroupedScopeResult, error) {
	if request.ViewerID != groupedViewerID(ctx) || request.SelectionRevision != "" {
		return GroupedScopeResult{}, ErrGroupScopeExpired
	}
	if h.pool == nil {
		return GroupedScopeResult{}, fmt.Errorf("handler.resolveGroupedTranscripts: no database during current-scope read; no rows returned; configure PostgreSQL and retry")
	}
	viewer := pgtype.UUID{}
	if user := GetUser(ctx); user != nil {
		viewer = user.PgID()
	}
	tags := append([]string{}, request.Tags...)
	filters := listFilterRequest{OwnerName: request.Owner, Org: request.Org, ProjectHash: request.ProjectHash}
	if request.Project != "" {
		filters.Project = "%" + request.Project + "%"
	}
	if request.Repository != "" {
		filters.Repository = "%" + request.Repository + "%"
	}
	filter, err := h.resolveOwnerFilter(ctx, GetUser(ctx), filters)
	if err != nil {
		return GroupedScopeResult{}, err
	}
	rows, err := sqlc.New(h.pool).ListGroupedTranscriptCandidates(ctx, sqlc.ListGroupedTranscriptCandidatesParams{
		ViewerID: viewer, SearchText: request.Query, SearchPattern: "%" + request.Query + "%", Provider: request.Provider,
		FilterByOwner: filter.Active, FilterOwnerIds: filter.OwnerIDs, ProjectName: request.Project, ProjectPattern: "%" + request.Project + "%", ProjectHash: request.ProjectHash,
		Repository: request.Repository, RepositoryPattern: "%" + request.Repository + "%", Tags: tags, Origin: request.Origin,
	})
	if err != nil {
		return GroupedScopeResult{}, fmt.Errorf("handler.resolveGroupedTranscripts: selected SQL read failed; retry after checking migrated database: %w", err)
	}
	keys := make([]projectIdentityKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, projectIdentityKey{OwnerID: row.OwnerID, ProjectHash: row.ProjectHash})
	}
	projects := h.resolveProjectIdentities(ctx, keys)
	ownerIDs := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		ownerIDs = append(ownerIDs, row.OwnerID)
	}
	owners, err := h.resolvePublicOwners(ctx, GetUser(ctx), ownerIDs)
	if err != nil {
		return GroupedScopeResult{}, fmt.Errorf("handler.resolveGroupedTranscripts: owner profile visibility could not be read; no rows returned; retry the list: %w", err)
	}
	parents, err := h.resolveParentTranscriptsOf(ctx, GetUser(ctx), rows)
	if err != nil {
		return GroupedScopeResult{}, fmt.Errorf("handler.resolveGroupedTranscripts: parent sessions could not be resolved; no rows returned; retry the list: %w", err)
	}
	result := GroupedScopeResult{Rows: []schema.VillageSessionRow{}}
	for _, row := range rows {
		session, err := groupedSessionFromTranscript(row, projects[projectIdentityKey{OwnerID: row.OwnerID, ProjectHash: row.ProjectHash}])
		if err != nil {
			// A row that cannot be projected fails the read instead of disappearing:
			// a silently shorter list would misstate what the route selected. Name
			// the transcript and the cause so an operator can find the row, and keep
			// the shared failure shape the other grouped reads already answer with.
			return GroupedScopeResult{}, &GroupedScopeError{Status: 500, Message: fmt.Sprintf(
				"handler.resolveGroupedTranscripts refused the grouped read: stored transcript metadata for transcript %s could not be projected into the canonical response; no transcript list or members were returned and nothing was changed; retry after the deployed contract and the stored metadata agree: %v",
				uuidFromPg(row.ID).String(), err)}
		}
		session.ParentTranscriptID = parents.of(row.ID)
		withholdSessionRepository(&session, owners.of(row.OwnerID))
		result.add(schema.VillageSessionRow{Session: session, Owner: owners.of(row.OwnerID)}, row.OwnerID)
	}
	return result, nil
}
