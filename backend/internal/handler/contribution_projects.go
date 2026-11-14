package handler

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// The project picker page size is counted in projects, 1 to 500, and 500 when
// the caller names none. Every count it returns is in sessions.
const (
	contributionProjectsLimitDefault = 500
	contributionProjectsLimitMaximum = 500
)

// contributionProjectsParams is the validated query of the project picker.
type contributionProjectsParams struct {
	query    string
	provider string
	limit    int
	cursor   *contributionProjectCursor
}

// contributionProjectCursor is the opaque position of the picker. It is the
// (display name, project hash) of the last project a page returned, bound to the
// collective, the viewer and the search that issued it, so it never resumes a
// different list.
type contributionProjectCursor struct {
	Collective string `json:"c"`
	Viewer     string `json:"u"`
	Query      string `json:"q"`
	Provider   string `json:"p"`
	Name       string `json:"n"`
	Hash       string `json:"h"`
}

func encodeContributionProjectCursor(c contributionProjectCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeContributionProjectCursor(raw string) (contributionProjectCursor, error) {
	var c contributionProjectCursor
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	return c, nil
}

// parseContributionProjectsQuery accepts exactly the parameters the contract
// names. It returns a refusal message, empty when the query is valid.
func parseContributionProjectsQuery(values url.Values) (contributionProjectsParams, string) {
	params := contributionProjectsParams{limit: contributionProjectsLimitDefault}
	for key, vs := range values {
		switch key {
		case "q", "provider", "cursor", "limit":
		default:
			return params, "an unsupported parameter was supplied to the project picker. Use only q, provider, cursor and limit. Nothing was read."
		}
		if len(vs) != 1 {
			return params, fmt.Sprintf("the parameter %q was repeated. Send each parameter once. Nothing was read.", key)
		}
	}
	params.query = strings.TrimSpace(values.Get("q"))
	params.provider = strings.TrimSpace(values.Get("provider"))
	if raw := values.Get("limit"); values.Has("limit") {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > contributionProjectsLimitMaximum {
			return params, fmt.Sprintf("limit must be a whole number from 1 through %d, counted in projects. Nothing was read.", contributionProjectsLimitMaximum)
		}
		params.limit = limit
	}
	if raw := values.Get("cursor"); values.Has("cursor") {
		cursor, err := decodeContributionProjectCursor(raw)
		if err != nil {
			return params, "the cursor is not one this project picker issued. Start again from the first page. Nothing was read."
		}
		params.cursor = &cursor
	}
	return params, ""
}

// escapeContributionLikePattern makes a search text literal inside an ILIKE
// pattern, so % and _ in the search match themselves.
func escapeContributionLikePattern(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(text) + "%"
}

// contributionProjectAfter reports whether a project sorts strictly after the
// cursor. The order is the display name, then the project hash, which is unique
// within the viewer's projects, so the order is total and paging cannot repeat or
// skip a project.
func contributionProjectAfter(s schema.VillageContributionProjectSummary, name, hash string) bool {
	if s.ProjectDisplayName != name {
		return s.ProjectDisplayName > name
	}
	return string(s.ProjectHash) > hash
}

// ListContributionProjects serves the project picker of one collective: every
// project of the caller that holds a family matching the search and filters, with
// the counts that decide what a selection can hold.
// GET /api/v1/collectives/{id}/contributable/projects, and the /groups alias
// (AuthRequired)
func (h *Handler) ListContributionProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := GetUser(ctx)

	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"the collective id %q in this address is not a valid identifier, so there is no collective to answer for. "+
				"Nothing was read. Open the collective from its own page and retry from there.", chi.URLParam(r, "id")))
		return
	}

	params, refusal := parseContributionProjectsQuery(r.URL.Query())
	if refusal != "" {
		writeError(w, http.StatusBadRequest, refusal)
		return
	}

	pgGroupID := toPgUUID(groupID)
	viewer := user.PgID()
	if _, err := h.queries.GetGroupMember(ctx, sqlc.GetGroupMemberParams{
		GroupID: pgGroupID,
		UserID:  viewer,
	}); err != nil {
		writeError(w, http.StatusForbidden, fmt.Sprintf(
			"you are not a member of collective %s, so Village will not list what you could contribute to it. Nothing "+
				"was read. Join the collective first, then open its contribute page.", groupID))
		return
	}

	collective := groupID.String()
	viewerID := uuid.UUID(viewer.Bytes).String()
	if c := params.cursor; c != nil &&
		(c.Collective != collective || c.Viewer != viewerID || c.Query != params.query || c.Provider != params.provider) {
		writeError(w, http.StatusBadRequest, "the cursor was issued for a different collective, viewer or search, so it cannot continue this list. "+
			"Start again from the first page. Nothing was read.")
		return
	}

	queries := sqlc.New(h.pool)
	roots, err := queries.ListOwnerContributionRootProjects(ctx, viewer)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"your projects could not be read, so Village cannot say what you could contribute to this collective. "+
				"Nothing was changed. Retry in a moment.")
		return
	}
	keys := make([]projectIdentityKey, 0, len(roots))
	for _, hash := range roots {
		keys = append(keys, projectIdentityKey{OwnerID: viewer, ProjectHash: hash})
	}
	identities := h.resolveProjectIdentities(ctx, keys)

	// Names are matched here, in Go, on the displayed name only. The SQL
	// receives the resulting project set and never classifies a name.
	nameMatched := []string{}
	hasQuery := params.query != ""
	if hasQuery {
		needle := strings.ToLower(params.query)
		for _, key := range keys {
			if strings.Contains(strings.ToLower(identities[key].DisplayName), needle) {
				nameMatched = append(nameMatched, key.ProjectHash)
			}
		}
	}

	counts, err := queries.CountOwnerContributionFamilyMatches(ctx, sqlc.CountOwnerContributionFamilyMatchesParams{
		OwnerID:             viewer,
		GroupID:             pgGroupID,
		HasQuery:            hasQuery,
		NameMatchedProjects: nameMatched,
		Provider:            params.provider,
		TitlePattern:        escapeContributionLikePattern(params.query),
		ExactText:           params.query,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"the project counts could not be read, so Village cannot say what you could contribute to this collective. "+
				"Nothing was changed. Retry in a moment.")
		return
	}
	facets, err := queries.CountOwnerContributionProviderMatches(ctx, sqlc.CountOwnerContributionProviderMatchesParams{
		OwnerID:             viewer,
		HasQuery:            hasQuery,
		NameMatchedProjects: nameMatched,
		TitlePattern:        escapeContributionLikePattern(params.query),
		ExactText:           params.query,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"the provider counts could not be read, so Village cannot say what you could contribute to this collective. "+
				"Nothing was changed. Retry in a moment.")
		return
	}

	all := make([]schema.VillageContributionProjectSummary, 0, len(counts))
	var matchingTotal, selectableTotal int
	for _, row := range counts {
		identity := identities[projectIdentityKey{OwnerID: viewer, ProjectHash: row.ProjectHash}]
		all = append(all, schema.VillageContributionProjectSummary{
			ProjectHash:        schema.ProjectHash(row.ProjectHash),
			ProjectDisplayName: identity.DisplayName,
			ProjectNameSource:  schema.VillageProjectNameSource(identity.Source),
			MatchingCount:      int(row.MatchingCount),
			SelectableCount:    int(row.SelectableCount),
		})
		matchingTotal += int(row.MatchingCount)
		selectableTotal += int(row.SelectableCount)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].ProjectDisplayName != all[j].ProjectDisplayName {
			return all[i].ProjectDisplayName < all[j].ProjectDisplayName
		}
		return all[i].ProjectHash < all[j].ProjectHash
	})

	providers := make([]schema.VillageContributionProviderFacet, 0, len(facets))
	for _, row := range facets {
		providers = append(providers, schema.VillageContributionProviderFacet{
			Provider: row.Provider,
			Count:    int(row.SessionCount),
		})
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Provider < providers[j].Provider })

	start := 0
	if c := params.cursor; c != nil {
		start = sort.Search(len(all), func(i int) bool {
			return contributionProjectAfter(all[i], c.Name, c.Hash)
		})
	}
	end := start + params.limit
	if end > len(all) {
		end = len(all)
	}
	page := make([]schema.VillageContributionProjectSummary, 0, end-start)
	page = append(page, all[start:end]...)

	response := schema.VillageContributionProjectPage{
		Projects:        page,
		HasMore:         end < len(all),
		MatchingTotal:   matchingTotal,
		SelectableTotal: selectableTotal,
		Providers:       providers,
	}
	if response.HasMore && len(page) > 0 {
		last := page[len(page)-1]
		next := encodeContributionProjectCursor(contributionProjectCursor{
			Collective: collective,
			Viewer:     viewerID,
			Query:      params.query,
			Provider:   params.provider,
			Name:       last.ProjectDisplayName,
			Hash:       string(last.ProjectHash),
		})
		response.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, response)
}
