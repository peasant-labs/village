package handler

// The paged contribution read: one page of contribution families, a root session
// together with every helper that descends from it, in session-time order.
//
//	GET /api/v1/collectives/{id}/contributable?view=paged
//	GET /api/v1/groups/{id}/contributable?view=paged        (deprecated alias)
//
// The page is bounded in ROOT sessions. A root's helpers ride with it and are
// not counted, so a page never cuts a family in half. The rows come from one
// statement (ListContributionPage), so a page is one consistent snapshot of the
// owner's library and the ledger.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/projectname"
)

// contributableViewMenu names the accepted values of the view parameter, in the
// order the contract lists them. It is what an unknown value is told.
var contributableViewMenu = []string{contributableViewGrouped, contributableViewPaged, contributableViewSnapshot}

// dispatchContributableView serves the non-flat views of the contributable read
// and reports whether it did. An omitted view is the flat response, which the
// caller serves itself.
func (h *Handler) dispatchContributableView(w http.ResponseWriter, r *http.Request) bool {
	views := r.URL.Query()["view"]
	view := ""
	if len(views) > 0 {
		view = views[0]
	}
	if len(views) > 1 && (slices.Contains(views, contributableViewPaged) || slices.Contains(views, contributableViewSnapshot)) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"the parameter \"view\" was sent %d times; send it once. Nothing was read.", len(views)))
		return true
	}
	switch view {
	case "":
		return false
	case contributableViewGrouped:
		h.listCollectiveGrouped(w, r, GroupedRouteContributable)
	case contributableViewPaged:
		h.listContributablePaged(w, r)
	case contributableViewSnapshot:
		h.listContributionSnapshot(w, r)
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"the view %q is not one this listing serves. The accepted values are %s. Omit view for the flat list. "+
				"Nothing was read.", view, strings.Join(contributableViewMenu, ", ")))
	}
	return true
}

// pagedContributableTranscript is one member's row: the flat listing's row plus
// the fields only the paged and snapshot reads carry.
type pagedContributableTranscript struct {
	contributableTranscript
	SessionStart       *time.Time `json:"session_start,omitempty"`
	ParentTranscriptID *string    `json:"parent_transcript_id,omitempty"`
	LatestStatus       *string    `json:"latest_status,omitempty"`
}

type pagedContributionMember struct {
	Transcript pagedContributableTranscript `json:"transcript"`
	Matched    *bool                        `json:"matched,omitempty"`
}

type pagedContributionItem struct {
	Root    pagedContributionMember   `json:"root"`
	Helpers []pagedContributionMember `json:"helpers"`
}

type pagedContributionResponse struct {
	GroupID    string                  `json:"group_id"`
	Items      []pagedContributionItem `json:"items"`
	NextCursor *string                 `json:"next_cursor,omitempty"`
	HasMore    bool                    `json:"has_more"`
}

// contributionPageRequest is the validated query of a paged read.
type contributionPageRequest struct {
	Q           string
	ProjectHash string
	Provider    string
	Limit       int
	Cursor      *contributionPageCursor
}

// contributionPageCursor is the position after the last root of a page: that
// root's session time (microseconds, the precision the column holds) and id.
type contributionPageCursor struct {
	Version     int    `json:"v"`
	SortMicros  int64  `json:"t"`
	RootID      string `json:"i"`
	Fingerprint string `json:"k"`
}

const contributionCursorVersion = 1

// contributionCursorFingerprint binds a cursor to the viewer, the collective and
// the filters it was issued for. It is not a secret and grants nothing: the
// viewer and the collective are re-checked on every request, and the cursor
// carries a position, never an owner.
func contributionCursorFingerprint(viewer, group string, req contributionPageRequest) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"v1", viewer, group, req.Q, req.ProjectHash, req.Provider,
	}, "\x1f")))
	return hex.EncodeToString(sum[:16])
}

func encodeContributionCursor(sortTime time.Time, rootID uuid.UUID, fingerprint string) string {
	raw, _ := json.Marshal(contributionPageCursor{
		Version:     contributionCursorVersion,
		SortMicros:  sortTime.UnixMicro(),
		RootID:      rootID.String(),
		Fingerprint: fingerprint,
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeContributionCursor(raw, fingerprint string) (*contributionPageCursor, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, false
	}
	var cursor contributionPageCursor
	decoder := json.NewDecoder(strings.NewReader(string(decoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return nil, false
	}
	if cursor.Version != contributionCursorVersion || cursor.Fingerprint != fingerprint {
		return nil, false
	}
	if _, err := uuid.Parse(cursor.RootID); err != nil {
		return nil, false
	}
	return &cursor, true
}

// contributionPageParams reads and validates the query of a paged read. It
// uses view, project_hash, q, provider, limit and cursor; every other parameter
// is ignored. A failure is a message for the caller, already actionable.
func contributionPageParams(query url.Values, viewer, group string) (contributionPageRequest, string) {
	req := contributionPageRequest{Limit: schema.VillageContributionPageLimitDefault}

	if refusal := refuseRepeatedParameters(query, "project_hash", "limit", "cursor"); refusal != "" {
		return req, refusal
	}
	search, refusal := parseContributionSearch(query)
	if refusal != "" {
		return req, refusal
	}
	req.Q, req.Provider = search.Query, search.Provider
	req.ProjectHash = query.Get("project_hash")
	if req.ProjectHash != "" && !projectHashPattern.MatchString(req.ProjectHash) {
		return req, "project_hash is not a project identity (64 lowercase hexadecimal characters). Nothing was read. Copy the exact hash from the project's page."
	}
	if query.Has("limit") {
		limit, err := strconv.Atoi(query.Get("limit"))
		if err != nil || limit < schema.VillageContributionPageLimitMinimum || limit > schema.VillageContributionPageLimitMaximum {
			return req, fmt.Sprintf("limit must be a whole number of root sessions from %d to %d. Nothing was read. Ask for a page size in that range, or omit limit to read %d.",
				schema.VillageContributionPageLimitMinimum, schema.VillageContributionPageLimitMaximum, schema.VillageContributionPageLimitDefault)
		}
		req.Limit = limit
	}
	if query.Has("cursor") {
		cursor, ok := decodeContributionCursor(query.Get("cursor"), contributionCursorFingerprint(viewer, group, req))
		if !ok {
			return req, "cursor is not a position in this listing: it is malformed, or it was issued for another search, filter or collective. Nothing was read. Start from the first page by omitting cursor."
		}
		req.Cursor = cursor
	}
	return req, ""
}

// listContributablePaged serves one page of families for the caller's own
// library against one collective.
func (h *Handler) listContributablePaged(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := GetUser(ctx)

	groupID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"the collective id %q in this address is not a valid identifier, so there is no collective to answer for. "+
				"Nothing was read. Open the collective from its own page and retry from there.", chi.URLParam(r, "id")))
		return
	}
	pgGroupID := toPgUUID(groupID)

	if _, err := h.queries.GetGroupMember(ctx, sqlc.GetGroupMemberParams{GroupID: pgGroupID, UserID: user.PgID()}); err != nil {
		writeError(w, http.StatusForbidden, fmt.Sprintf(
			"you are not a member of collective %s, so Village will not list what you could contribute to it. Nothing "+
				"was read. Join the collective first, then open its contribute page.", groupID))
		return
	}

	viewer := uuid.UUID(user.PgID().Bytes).String()
	req, problem := contributionPageParams(r.URL.Query(), viewer, groupID.String())
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}

	queries := sqlc.New(h.pool)
	params := sqlc.ListContributionPageParams{
		GroupID:          pgGroupID,
		OwnerID:          user.PgID(),
		MatchingProjects: []string{},
		RootLimit:        int32(req.Limit + 1),
	}
	if req.Q != "" {
		params.Q = pgtype.Text{String: req.Q, Valid: true}
		params.TitlePattern = pgtype.Text{String: contributionTitlePattern(req.Q), Valid: true}
		// Only a root's own project name can select a family, so only the
		// owner's root projects are named.
		roots, err := h.readContributionRootProjects(ctx, queries, user.PgID(), req.Q)
		if err != nil {
			writeError(w, http.StatusInternalServerError,
				"your projects could not be read, so Village cannot search them. Nothing was changed. Retry in a moment.")
			return
		}
		if roots.Named != nil {
			params.MatchingProjects = roots.Named
		}
	}
	if req.Provider != "" {
		params.Provider = pgtype.Text{String: req.Provider, Valid: true}
	}
	if req.ProjectHash != "" {
		params.ProjectHash = pgtype.Text{String: req.ProjectHash, Valid: true}
	}
	if req.Cursor != nil {
		afterID, _ := uuid.Parse(req.Cursor.RootID)
		params.AfterID = toPgUUID(afterID)
		params.AfterTime = pgtype.Timestamptz{Time: time.UnixMicro(req.Cursor.SortMicros).UTC(), Valid: true}
	}

	rows, err := queries.ListContributionPage(ctx, params)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"your transcripts could not be read, so Village cannot say what you could contribute to this collective. "+
				"Nothing was changed. Retry in a moment.")
		return
	}

	// Rows arrive grouped by root, in page order. The statement was asked for one
	// root more than the page holds; that extra root only proves another page
	// exists and is not served.
	var roots []uuid.UUID
	byRoot := map[uuid.UUID][]sqlc.ListContributionPageRow{}
	for _, row := range rows {
		root := uuid.UUID(row.RootID.Bytes)
		if _, seen := byRoot[root]; !seen {
			roots = append(roots, root)
		}
		byRoot[root] = append(byRoot[root], row)
	}
	hasMore := len(roots) > req.Limit
	if hasMore {
		roots = roots[:req.Limit]
	}

	keys := make([]projectIdentityKey, 0)
	seenProject := map[string]struct{}{}
	for _, root := range roots {
		for _, row := range byRoot[root] {
			if _, ok := seenProject[row.ProjectHash]; ok {
				continue
			}
			seenProject[row.ProjectHash] = struct{}{}
			keys = append(keys, projectIdentityKey{OwnerID: user.PgID(), ProjectHash: row.ProjectHash})
		}
	}
	identities := h.resolveProjectIdentities(ctx, keys)

	response := pagedContributionResponse{
		GroupID: groupID.String(),
		Items:   make([]pagedContributionItem, 0, len(roots)),
	}
	var lastRoot sqlc.ListContributionPageRow
	for _, root := range roots {
		family := byRoot[root]
		localToID := make(map[string]string, len(family))
		for _, row := range family {
			localToID[row.LocalID] = uuid.UUID(row.ID.Bytes).String()
		}
		item := pagedContributionItem{Helpers: []pagedContributionMember{}}
		for _, row := range family {
			member := pagedContributionMember{Transcript: pagedTranscriptOf(row, localToID, identities[projectIdentityKey{OwnerID: user.PgID(), ProjectHash: row.ProjectHash}])}
			if req.Q != "" {
				matched := row.Matched
				member.Matched = &matched
			}
			if row.Depth == 0 {
				item.Root = member
				lastRoot = row
			} else {
				item.Helpers = append(item.Helpers, member)
			}
		}
		response.Items = append(response.Items, item)
	}
	if hasMore {
		response.HasMore = true
		cursor := encodeContributionCursor(contributionSortTime(lastRoot), uuid.UUID(lastRoot.ID.Bytes),
			contributionCursorFingerprint(viewer, groupID.String(), req))
		response.NextCursor = &cursor
	}
	writeJSON(w, http.StatusOK, response)
}

// contributionSortTime is the clock a root is ordered on: when the session
// began, else when it was published, else the epoch. It is the same expression
// the statement orders by.
func contributionSortTime(row sqlc.ListContributionPageRow) time.Time {
	switch {
	case row.SessionStart.Valid:
		return row.SessionStart.Time
	case row.PublishedAt.Valid:
		return row.PublishedAt.Time
	default:
		return time.Unix(0, 0).UTC()
	}
}

func pagedTranscriptOf(row sqlc.ListContributionPageRow, localToID map[string]string, identity projectname.Resolved) pagedContributableTranscript {
	out := pagedContributableTranscript{
		contributableTranscript: contributableTranscript{
			ID:                 uuid.UUID(row.ID.Bytes).String(),
			LocalID:            row.LocalID,
			Title:              pgTextPointer(row.Title),
			Visibility:         row.Visibility,
			ProjectHash:        row.ProjectHash,
			ProjectDisplayName: identity.DisplayName,
			ProjectNameSource:  identity.Source,
			GitBranch:          pgTextPointer(row.GitBranch),
			ParentSessionID:    pgTextPointer(row.ParentSessionID),
			SessionOrigin:      row.SessionOrigin,
			ModelProvider:      row.ModelProvider,
			PublishedAt:        row.PublishedAt,
			AlreadyShared:      row.AlreadyShared,
		},
	}
	if row.SessionStart.Valid {
		start := row.SessionStart.Time
		out.SessionStart = &start
	}
	if row.Depth > 0 && row.ParentSessionID.Valid {
		if parent, ok := localToID[row.ParentSessionID.String]; ok {
			out.ParentTranscriptID = &parent
		}
	}
	if row.LatestStatus != "" {
		status := row.LatestStatus
		out.LatestStatus = &status
	}
	return out
}
