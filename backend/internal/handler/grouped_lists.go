package handler

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// GroupedScopeError preserves a route's safe denial when replayed through the
// common member endpoint. Message must not reveal inaccessible identities.
type GroupedScopeError struct {
	Status  int
	Message string
}

func (e *GroupedScopeError) Error() string { return e.Message }

func WriteGroupedScopeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrGroupScopeExpired) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "group_scope_expired", "error": ErrGroupScopeExpired.Error()})
		return
	}
	var refusal *GroupedScopeError
	if errors.As(err, &refusal) {
		writeError(w, refusal.Status, refusal.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, "handler grouped listing failed while reading or projecting current route candidates; no list or members returned; retry the originating list, and if this persists check the deployed schema and database migrations")
}

func groupedViewerID(ctx context.Context) string {
	if user := GetUser(ctx); user != nil {
		return user.ID.String()
	}
	return ""
}

// GroupedList is the single grouping entry point for all registered routes.
// Resolvers receive current auth context, never an authority inferred from a token.
func (h *Handler) GroupedList(ctx context.Context, request GroupedScopeRequest, page, limit int) (schema.VillageSessionListPayload, error) {
	request.ViewerID = groupedViewerID(ctx)
	if err := validateGroupedScopeSize(request); err != nil {
		return schema.VillageSessionListPayload{}, err
	}
	resolve := h.groupedScopes.resolver(request.Variant)
	if resolve == nil {
		return schema.VillageSessionListPayload{}, ErrGroupScopeExpired
	}
	result, err := h.resolveGroupedCandidates(ctx, request, resolve)
	if err != nil {
		return schema.VillageSessionListPayload{}, err
	}
	items, groups, ordinary, err := groupVillageRows(result.Rows, result.cyclic)
	if err != nil {
		return schema.VillageSessionListPayload{}, err
	}
	page, limit = groupedPage(page, limit)
	payload := schema.VillageSessionListPayload{Items: []schema.VillageSessionListItem{}, Page: page, Limit: limit, TotalItems: len(items), OrdinarySessionTotal: ordinary}
	for _, members := range groups {
		payload.HelperThreadTotal += len(members)
	}
	start, end := groupedBounds(page, limit, len(items))
	for _, item := range items[start:end] {
		item, err = h.scopeGroupedItem(request, item)
		if err != nil {
			return schema.VillageSessionListPayload{}, err
		}
		payload.Items = append(payload.Items, item)
	}
	return payload, payload.Validate()
}

// ListHelperMembers is registered once, with AuthOptional, for both public and
// authenticated originating routes. Only the stored route resolver may read rows.
func (h *Handler) ListHelperMembers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for key, values := range q {
		if (key != "scope" && key != "page" && key != "limit") || len(values) != 1 {
			writeError(w, http.StatusBadRequest, "handler.ListHelperMembers rejected extra or repeated query parameters during expansion; the saved scope alone defines membership; remove filters and send only scope, page and limit, or refresh the originating list")
			return
		}
	}
	groupID := chi.URLParam(r, "groupId")
	request, err := h.groupedScopes.lookup(q.Get("scope"), groupID, groupedViewerID(r.Context()), time.Now())
	if err != nil {
		WriteGroupedScopeError(w, err)
		return
	}
	resolve := h.groupedScopes.resolver(request.Variant)
	if resolve == nil {
		WriteGroupedScopeError(w, ErrGroupScopeExpired)
		return
	}
	result, err := h.resolveGroupedCandidates(r.Context(), request, resolve)
	if err != nil {
		WriteGroupedScopeError(w, err)
		return
	}
	_, groups, _, err := groupVillageRows(result.Rows, result.cyclic)
	if err != nil {
		WriteGroupedScopeError(w, err)
		return
	}
	members := groups[groupID]
	page, limit := groupedQueryPage(q)
	start, end := groupedBounds(page, limit, len(members))
	payload := schema.VillageHelperMembersPayload{Members: []schema.VillageSessionListItem{}, Page: page, Limit: limit, Total: len(members)}
	for _, item := range members[start:end] {
		item, err = h.scopeGroupedItem(request, item)
		if err != nil {
			WriteGroupedScopeError(w, err)
			return
		}
		payload.Members = append(payload.Members, item)
	}
	if err := payload.Validate(); err != nil {
		WriteGroupedScopeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// Nested scopes retain the original route universe, not the outer group's
// direct-member restriction. Only the group binding changes at each disclosure.
func (h *Handler) scopeGroupedItem(request GroupedScopeRequest, item schema.VillageSessionListItem) (schema.VillageSessionListItem, error) {
	item.HelperGroups = append([]schema.HelperGroupSummary(nil), item.HelperGroups...)
	for i := range item.HelperGroups {
		token, err := h.groupedScopes.mint(request, item.HelperGroups[i].GroupID, time.Now())
		if err != nil {
			return schema.VillageSessionListItem{}, err
		}
		item.HelperGroups[i].MemberScope = token
	}
	return item, nil
}

func groupedQueryPage(q url.Values) (int, int) {
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	return groupedPage(page, limit)
}

func groupedPage(page, limit int) (int, int) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return page, limit
}

func groupedBounds(page, limit, total int) (int, int) {
	// Compare before multiplying: a hostile page must not overflow an index.
	if total == 0 || page-1 > total/limit {
		return total, total
	}
	start := (page - 1) * limit
	end := start + limit
	if end > total {
		end = total
	}
	return start, end
}

func groupedSortTime(row schema.VillageSessionRow) time.Time {
	if row.Session.SessionStart != nil {
		return *row.Session.SessionStart
	}
	return row.Session.PublishedAt
}

func groupedIdentity(owner schema.VillageUUID, local schema.SessionID) string {
	return string(owner) + "\x00" + string(local)
}

func helperOwner(row schema.VillageSessionRow) (schema.SessionID, schema.RelationshipNavigationStatus) {
	for _, relation := range row.Session.Relationships {
		if relation.Kind != schema.SessionRelationshipStartedBy {
			continue
		}
		switch relation.TargetState {
		case schema.RelationshipTargetKnown, schema.RelationshipTargetKnownRetained:
			if relation.TargetLocalID != nil {
				return *relation.TargetLocalID, schema.RelationshipNavigationKnownUnavailable
			}
		case schema.RelationshipTargetConflictingCurrentNativeEvidence:
			return "", schema.RelationshipNavigationConflicting
		}
		return "", schema.RelationshipNavigationUnknown
	}
	// Legacy logical parent remains compatible only when no new started_by
	// authority is present. It is not inferred from an availability FK.
	if row.Session.ParentSessionID != nil {
		return *row.Session.ParentSessionID, schema.RelationshipNavigationKnownUnavailable
	}
	return "", schema.RelationshipNavigationUnknown
}

func helperGroupID(row schema.VillageSessionRow, owner schema.SessionID) string {
	parts := []string{"1", string(row.Session.OwnerID), string(owner), string(schema.SessionPurposeHelperReview)}
	if owner == "" {
		parts = []string{"1", string(row.Session.OwnerID), "unresolved", string(row.Session.LocalID), string(schema.SessionPurposeHelperReview)}
	}
	hash := sha256.New()
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		hash.Write(length[:])
		hash.Write([]byte(part))
	}
	return "hg_" + base64.RawURLEncoding.EncodeToString(hash.Sum(nil))
}

// groupVillageRows only sees the selected candidate domain. No lookup of a
// hidden owner's title, time or usage is needed to construct a context container.
func groupVillageRows(rows []schema.VillageSessionRow, cyclic map[schema.TranscriptID]bool) ([]schema.VillageSessionListItem, map[string][]schema.VillageSessionListItem, int, error) {
	byIdentity := make(map[string]schema.VillageSessionRow, len(rows))
	seenIDs := make(map[schema.TranscriptID]bool, len(rows))
	for _, row := range rows {
		if err := row.Validate(); err != nil {
			return nil, nil, 0, err
		}
		if !row.Session.Purpose.IsValid() {
			return nil, nil, 0, fmt.Errorf("handler helper grouping: invalid stored purpose during projection; no rows returned; repair the publishing validation and explicitly republish")
		}
		if err := schema.ValidateSessionRelationships(row.Session.Relationships); err != nil {
			return nil, nil, 0, err
		}
		key := groupedIdentity(row.Session.OwnerID, row.Session.LocalID)
		if _, exists := byIdentity[key]; exists || seenIDs[row.Session.ID] {
			return nil, nil, 0, fmt.Errorf("handler helper grouping: duplicate candidate identity during projection; no rows returned; correct the originating SQL to return distinct transcripts")
		}
		byIdentity[key] = row
		seenIDs[row.Session.ID] = true
	}
	groups := map[string][]schema.VillageSessionRow{}
	owners := map[string]string{}
	statuses := map[string]schema.RelationshipNavigationStatus{}
	for _, row := range rows {
		if row.Session.Purpose != schema.SessionPurposeHelperReview {
			continue
		}
		owner, status := helperOwner(row)
		if cyclic[row.Session.ID] {
			owner, status = "", schema.RelationshipNavigationConflicting
		}
		// Only a cycle actually containing this helper invalidates its owner.
		visited := map[string]bool{groupedIdentity(row.Session.OwnerID, row.Session.LocalID): true}
		next := owner
		for next != "" {
			key := groupedIdentity(row.Session.OwnerID, next)
			if visited[key] {
				owner, status = "", schema.RelationshipNavigationConflicting
				break
			}
			visited[key] = true
			parent, exists := byIdentity[key]
			if !exists {
				break
			}
			next, _ = helperOwner(parent)
		}
		id := helperGroupID(row, owner)
		groups[id] = append(groups[id], row)
		if owner != "" {
			owners[id] = groupedIdentity(row.Session.OwnerID, owner)
		}
		statuses[id] = status
	}
	items := []schema.VillageSessionListItem{}
	times := map[string]time.Time{}
	byItemIdentity := map[string]*schema.VillageSessionListItem{}
	for i := range rows {
		byItemIdentity[groupedIdentity(rows[i].Session.OwnerID, rows[i].Session.LocalID)] = &schema.VillageSessionListItem{Kind: schema.SessionListItemTranscript, Transcript: &rows[i]}
		if rows[i].Session.Purpose == schema.SessionPurposeHelperReview {
			continue
		}
		times[string(rows[i].Session.ID)] = groupedSortTime(rows[i])
	}
	ordinary := len(times)
	for id, members := range groups {
		sort.Slice(members, func(i, j int) bool {
			a, b := groupedSortTime(members[i]), groupedSortTime(members[j])
			if !a.Equal(b) {
				return a.Before(b)
			}
			return members[i].Session.ID < members[j].Session.ID
		})
		group := schema.HelperGroupSummary{GroupID: id, Purpose: schema.SessionPurposeHelperReview, HelperThreadCount: len(members)}
		if owner, ok := byItemIdentity[owners[id]]; ok {
			owner.HelperGroups = append(owner.HelperGroups, group)
		} else {
			items = append(items, schema.VillageSessionListItem{Kind: schema.SessionListItemContextContainer, Context: &schema.HelperContextSummary{GroupID: id, OwnerStatus: statuses[id]}, HelperGroups: []schema.HelperGroupSummary{group}})
			times[id] = groupedSortTime(members[len(members)-1])
		}
	}
	// Every admitted helper is represented in exactly one direct-member group.
	// Attach summaries before copying items into member pages so helper owners
	// carry their own disclosures without redundant top-level containers.
	for _, item := range byItemIdentity {
		sort.Slice(item.HelperGroups, func(a, b int) bool { return item.HelperGroups[a].GroupID < item.HelperGroups[b].GroupID })
		if item.Transcript.Session.Purpose != schema.SessionPurposeHelperReview {
			items = append(items, *item)
		}
	}
	memberItems := make(map[string][]schema.VillageSessionListItem, len(groups))
	for id, members := range groups {
		for _, row := range members {
			memberItems[id] = append(memberItems[id], *byItemIdentity[groupedIdentity(row.Session.OwnerID, row.Session.LocalID)])
		}
	}
	itemKey := func(item schema.VillageSessionListItem) string {
		if item.Transcript != nil {
			return string(item.Transcript.Session.ID)
		}
		return item.Context.GroupID
	}
	for i := range items {
		sort.Slice(items[i].HelperGroups, func(a, b int) bool { return items[i].HelperGroups[a].GroupID < items[i].HelperGroups[b].GroupID })
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := itemKey(items[i]), itemKey(items[j])
		if !times[a].Equal(times[b]) {
			return times[a].After(times[b])
		}
		return a < b
	})
	return items, memberItems, ordinary, nil
}

func (h *Handler) resolveGroupedCandidates(ctx context.Context, request GroupedScopeRequest, resolve GroupedScopeResolver) (GroupedScopeResult, error) {
	result, err := resolve(ctx, request)
	if err != nil {
		return GroupedScopeResult{}, err
	}
	ids := []pgtype.UUID{}
	for _, row := range result.Rows {
		if row.Session.Purpose != schema.SessionPurposeHelperReview {
			continue
		}
		id, err := uuid.Parse(string(row.Session.ID))
		if err != nil {
			return GroupedScopeResult{}, fmt.Errorf("handler grouped candidates: malformed published identity during cycle resolution; no rows returned; correct the route projection: %w", err)
		}
		ids = append(ids, toPgUUID(id))
	}
	if len(ids) == 0 {
		return result, nil
	}
	if h.pool == nil {
		return GroupedScopeResult{}, fmt.Errorf("handler grouped candidates: database unavailable during owner-cycle resolution; no members returned; restore the database and refresh the list")
	}
	cycles, err := sqlc.New(h.pool).ListGroupedCyclicHelpers(ctx, ids)
	if err != nil {
		return GroupedScopeResult{}, err
	}
	result.cyclic = make(map[schema.TranscriptID]bool, len(cycles))
	for _, id := range cycles {
		result.cyclic[schema.TranscriptID(uuid.UUID(id.Bytes).String())] = true
	}
	return result, nil
}
