package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/peasant-labs/schema"
)

// GroupedRouteVariant is the closed set of Village list predicates that may
// mint and replay a helper-member scope. It is deliberately not a query AST:
// each variant has one production callback that reapplies its existing route
// authorization and eligibility rules.
type GroupedRouteVariant string

const (
	GroupedRouteTranscripts   GroupedRouteVariant = "transcripts"
	GroupedRouteProfile       GroupedRouteVariant = "profile"
	GroupedRouteProject       GroupedRouteVariant = "project"
	GroupedRouteCollective    GroupedRouteVariant = "collective"
	GroupedRouteContributable GroupedRouteVariant = "contributable"
	GroupedRoutePending       GroupedRouteVariant = "pending"
	GroupedRouteMyShares      GroupedRouteVariant = "my_shares"
)

// GroupedScopeRequest is the complete parsed scope needed to rerun one list.
// Values are normalized at the original handler boundary; member requests may
// supply only the opaque scope, group ID, page, and limit.
type GroupedScopeRequest struct {
	Variant           GroupedRouteVariant
	ViewerID          string
	Owner             string
	ProjectHash       string
	Project           string
	Collective        string
	Query             string
	Provider          string
	Repository        string
	Org               string
	Tags              []string
	Origin            string
	Sort              string
	SelectionRevision string
}

// GroupedScopeResult is the unpaged selected candidate set. Rows are concrete
// schema.VillageSessionRow values so every registered variant preserves its
// existing route-specific projection without a second wire model.
type GroupedScopeResult struct {
	Rows   []schema.VillageSessionRow
	cyclic map[schema.TranscriptID]bool
}

// GroupedScopeResolver replays one fixed route variant using current
// authorization and eligibility. Implementations must return the same selected
// row domain the original grouped list used, before grouping and pagination.
type GroupedScopeResolver func(context.Context, GroupedScopeRequest) (GroupedScopeResult, error)

// GroupedScopeRegistration is the only extension seam for grouped list
// variants. Collective handlers register their fixed callbacks here; they do
// not implement another token cache or member endpoint.
type GroupedScopeRegistration interface {
	RegisterGroupedScope(GroupedRouteVariant, GroupedScopeResolver) error
}

const groupedScopeLifetime = 15 * time.Minute

const groupedScopeCapacity = 4096

const groupedScopeMaxFilterBytes = 16 * 1024

func validateGroupedScopeSize(request GroupedScopeRequest) error {
	bytes := len(request.ViewerID) + len(request.Owner) + len(request.ProjectHash) + len(request.Project) + len(request.Collective) + len(request.Query) + len(request.Provider) + len(request.Repository) + len(request.Org) + len(request.Origin) + len(request.Sort) + len(request.SelectionRevision)
	for _, tag := range request.Tags {
		bytes += len(tag)
	}
	if bytes > groupedScopeMaxFilterBytes || len(request.Tags) > 128 {
		return &GroupedScopeError{Status: 400, Message: "handler.GroupedList refused oversized filters while saving a member scope; no grouped list was returned because the bounded scope cache cannot retain this request; shorten the filters to 16 KiB and at most 128 tags, then retry"}
	}
	return nil
}

// ErrGroupScopeExpired never permits a fallback to an unscoped member read.
var ErrGroupScopeExpired = errors.New("group_scope_expired: handler helper-member replay cannot recover this viewer's original list scope during expansion; no members were returned; refresh the originating list and expand again")

type groupedScopeEntry struct {
	request GroupedScopeRequest
	groupID string
	expires time.Time
}

// The registry and token store are local to one Handler/server. A restart or
// another server instance intentionally requires refreshing the original list.
type groupedScopeService struct {
	mu        sync.Mutex
	resolvers map[GroupedRouteVariant]GroupedScopeResolver
	entries   map[string]groupedScopeEntry
}

func validGroupedRoute(v GroupedRouteVariant) bool {
	switch v {
	case GroupedRouteTranscripts, GroupedRouteProfile, GroupedRouteProject,
		GroupedRouteCollective, GroupedRouteContributable, GroupedRoutePending, GroupedRouteMyShares:
		return true
	}
	return false
}

// RegisterGroupedScope binds a fixed route to its current-rights predicate.
// Duplicate registration is an error, never an override of another route owner.
func (h *Handler) RegisterGroupedScope(v GroupedRouteVariant, resolve GroupedScopeResolver) error {
	if !validGroupedRoute(v) || resolve == nil {
		return fmt.Errorf("handler.RegisterGroupedScope: route or resolver is invalid during server composition; grouped reads are unavailable; register a supported route with its production predicate")
	}
	s := &h.groupedScopes
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolvers == nil {
		s.resolvers = make(map[GroupedRouteVariant]GroupedScopeResolver)
	}
	if s.resolvers[v] != nil {
		return fmt.Errorf("handler.RegisterGroupedScope: route %s already registered during server composition; keep one predicate owner", v)
	}
	s.resolvers[v] = resolve
	return nil
}

func (s *groupedScopeService) resolver(v GroupedRouteVariant) GroupedScopeResolver {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolvers[v]
}

func (s *groupedScopeService) mint(request GroupedScopeRequest, groupID string, now time.Time) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("handler helper scope allocation failed during list response; no scope issued; retry after restoring system randomness: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	request.Tags = append([]string(nil), request.Tags...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]groupedScopeEntry)
	}
	for key, entry := range s.entries {
		if !now.Before(entry.expires) {
			delete(s.entries, key)
		}
	}
	if len(s.entries) >= groupedScopeCapacity {
		var oldest string
		var expiry time.Time
		for key, entry := range s.entries {
			if oldest == "" || entry.expires.Before(expiry) {
				oldest, expiry = key, entry.expires
			}
		}
		delete(s.entries, oldest)
	}
	s.entries[token] = groupedScopeEntry{request: request, groupID: groupID, expires: now.Add(groupedScopeLifetime)}
	return token, nil
}

func (s *groupedScopeService) lookup(token, groupID, viewerID string, now time.Time) (GroupedScopeRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[token]
	if !ok || !now.Before(entry.expires) {
		delete(s.entries, token)
		return GroupedScopeRequest{}, ErrGroupScopeExpired
	}
	if entry.groupID != groupID || entry.request.ViewerID != viewerID {
		return GroupedScopeRequest{}, ErrGroupScopeExpired
	}
	request := entry.request
	request.Tags = append([]string(nil), request.Tags...)
	return request, nil
}
