package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/peasant-labs/schema"
	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// relationshipNavigation resolves a child transcript's stored relationship
// evidence into viewer-authorized current-target navigation for the metadata
// read. It reads only query projections and never captured content, so serving
// the child's metadata cannot change its durable blob.
//
// Targets resolve by the child's OWN owner-local identity
// (child.owner_id, target_local_id): another owner publishing the same local id
// is not the child's target, and a same-owner target the current viewer may not
// read stays a distinct, honest "inaccessible" result with no target title or
// public id. Unknown and conflicting evidence stays explicit; explicit-none
// leaves no link.
func (h *Handler) relationshipNavigation(ctx context.Context, user *AuthUser, child sqlc.Transcript) ([]schema.SessionRelationshipNavigation, error) {
	var relations []schema.SessionRelationship
	if len(child.SessionRelationships) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(child.SessionRelationships, &relations); err != nil {
		return nil, navigationReadError("decode stored relationships")
	}
	if err := schema.ValidateSessionRelationships(relations); err != nil {
		return nil, navigationReadError("validate stored relationships")
	}
	var result []schema.SessionRelationshipNavigation
	for _, relation := range relations {
		navigation, err := h.resolveRelationship(ctx, user, child, relation)
		if err != nil {
			return nil, err
		}
		if navigation == nil {
			continue
		}
		if err := navigation.Validate(); err != nil {
			return nil, navigationReadError("validate resolved navigation")
		}
		result = append(result, *navigation)
	}
	return result, nil
}

// resolveRelationship turns one stored relationship into navigation, or nil
// when the relationship intentionally has no link.
func (h *Handler) resolveRelationship(ctx context.Context, user *AuthUser, child sqlc.Transcript, relation schema.SessionRelationship) (*schema.SessionRelationshipNavigation, error) {
	navigation := &schema.SessionRelationshipNavigation{Kind: relation.Kind}
	switch relation.TargetState {
	case schema.RelationshipTargetExplicitNone:
		return nil, nil
	case schema.RelationshipTargetUnknown:
		navigation.Status = schema.RelationshipNavigationUnknown
		return navigation, nil
	case schema.RelationshipTargetConflictingCurrentNativeEvidence:
		navigation.Status = schema.RelationshipNavigationConflicting
		return navigation, nil
	}
	target, err := h.lookupOwnerLocalTarget(ctx, child, string(*relation.TargetLocalID))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The child's own owner-local target is absent (a target under any other
		// owner is a different identity and never substituted here).
		navigation.Status = schema.RelationshipNavigationKnownUnavailable
		return navigation, nil
	case err != nil:
		return nil, err
	}
	if allowed, _ := h.canReadTranscript(ctx, user, target); !allowed {
		navigation.Status = schema.RelationshipNavigationInaccessible
		return navigation, nil
	}
	publicID := schema.TranscriptID(uuidFromPg(target.ID).String())
	navigation.TranscriptID = &publicID
	if relation.Kind == schema.SessionRelationshipContextFrom {
		// A context source link is general by default. The exact branch-point
		// anchor is emitted only when the stored public revision ref still
		// identifies the target's current public representation; a republished
		// or otherwise changed source keeps the general link.
		if exactPublicAnchor(relation.Anchor, target) {
			navigation.Status = schema.RelationshipNavigationResolved
			navigation.Anchor = relation.Anchor
		} else {
			navigation.Status = schema.RelationshipNavigationGeneralLinkOnly
			navigation.Anchor = &schema.PublicSourceAnchor{Kind: schema.PublicSourceAnchorGeneral}
		}
		return navigation, nil
	}
	navigation.Status = schema.RelationshipNavigationResolved
	return navigation, nil
}

// lookupOwnerLocalTarget loads the child owner's transcript with localID and
// re-checks the identity the query was scoped by, so a stale or malformed read
// can never attach a different owner's same-local-id transcript.
func (h *Handler) lookupOwnerLocalTarget(ctx context.Context, child sqlc.Transcript, localID string) (sqlc.Transcript, error) {
	id, err := h.queries.GetTranscriptIDByOwnerAndLocalID(ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: child.OwnerID, LocalID: localID})
	if err != nil {
		return sqlc.Transcript{}, err
	}
	target, err := h.queries.GetTranscriptByID(ctx, id)
	if err != nil {
		return sqlc.Transcript{}, err
	}
	if target.OwnerID != child.OwnerID || target.LocalID != localID {
		return sqlc.Transcript{}, navigationReadError("verify owner-local target identity")
	}
	return target, nil
}

// exactPublicAnchor reports whether a stored context anchor still identifies the
// target's current public representation. The transcript content hash is the
// server-computed digest of the exact redacted public bytes, so it is the public
// revision authority — never a raw native source hash. An absent anchor, a
// general anchor, or a missing or mismatched revision keeps the link general.
func exactPublicAnchor(anchor *schema.PublicSourceAnchor, target sqlc.Transcript) bool {
	if anchor == nil || anchor.Kind == schema.PublicSourceAnchorGeneral {
		return false
	}
	if anchor.SourceEntryRef == "" || anchor.SourceRevisionRef == "" {
		return false
	}
	if !target.ContentHash.Valid || target.ContentHash.String == "" {
		return false
	}
	return string(anchor.SourceRevisionRef) == target.ContentHash.String
}

func navigationReadError(step string) error {
	return fmt.Errorf("relationship navigation failed in handler.relationshipNavigation during %s because stored evidence or its database lookup could not be verified; metadata was withheld and captured content was not changed; restore the database and validated relationship projections, then retry", step)
}
