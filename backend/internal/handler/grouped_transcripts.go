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
	rows, err := sqlc.New(h.pool).ListGroupedTranscriptCandidates(ctx, sqlc.ListGroupedTranscriptCandidatesParams{
		ViewerID: viewer, SearchText: request.Query, SearchPattern: "%" + request.Query + "%", Provider: request.Provider,
		OwnerName: request.Owner, ProjectName: request.Project, ProjectPattern: "%" + request.Project + "%", ProjectHash: request.ProjectHash,
		Repository: request.Repository, RepositoryPattern: "%" + request.Repository + "%", Org: request.Org, Tags: tags, Origin: request.Origin,
	})
	if err != nil {
		return GroupedScopeResult{}, fmt.Errorf("handler.resolveGroupedTranscripts: selected SQL read failed; retry after checking migrated database: %w", err)
	}
	keys := make([]projectIdentityKey, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, projectIdentityKey{OwnerID: row.OwnerID, ProjectHash: row.ProjectHash})
	}
	projects := h.resolveProjectIdentities(ctx, keys)
	result := GroupedScopeResult{Rows: []schema.VillageSessionRow{}}
	for _, row := range rows {
		// Reuse the existing public projection and its privacy policy. JSONB
		// fields must remain JSON rather than the []byte base64 representation.
		projected := listTranscriptResponse(row, projects[projectIdentityKey{OwnerID: row.OwnerID, ProjectHash: row.ProjectHash}])
		encoded, err := json.Marshal(struct {
			transcriptResponse
			Subagents json.RawMessage `json:"subagents"`
			Warnings  json.RawMessage `json:"diagnostics_warnings"`
		}{projected, row.Subagents, row.DiagnosticsWarnings})
		if err != nil {
			return GroupedScopeResult{}, err
		}
		var session schema.VillageTranscript
		if err := json.Unmarshal(encoded, &session); err != nil {
			return GroupedScopeResult{}, err
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
			return GroupedScopeResult{}, err
		}
		result.Rows = append(result.Rows, schema.VillageSessionRow{Session: session})
	}
	return result, nil
}
