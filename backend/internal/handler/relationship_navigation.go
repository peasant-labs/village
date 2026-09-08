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

// relationshipNavigation reads only query projections, never captured content.
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
		navigation := schema.SessionRelationshipNavigation{Kind: relation.Kind}
		switch relation.TargetState {
		case schema.RelationshipTargetExplicitNone:
			continue
		case schema.RelationshipTargetUnknown:
			navigation.Status = schema.RelationshipNavigationUnknown
		case schema.RelationshipTargetConflictingCurrentNativeEvidence:
			navigation.Status = schema.RelationshipNavigationConflicting
		default:
			id, err := h.queries.GetTranscriptIDByOwnerAndLocalID(ctx, sqlc.GetTranscriptIDByOwnerAndLocalIDParams{OwnerID: child.OwnerID, LocalID: string(*relation.TargetLocalID)})
			if errors.Is(err, pgx.ErrNoRows) {
				navigation.Status = schema.RelationshipNavigationKnownUnavailable
			} else if err != nil {
				return nil, navigationReadError("look up owner-local target")
			} else {
				target, err := h.queries.GetTranscriptByID(ctx, id)
				if errors.Is(err, pgx.ErrNoRows) {
					navigation.Status = schema.RelationshipNavigationKnownUnavailable
				} else if err != nil {
					return nil, navigationReadError("read current target")
				} else if target.OwnerID != child.OwnerID || target.LocalID != string(*relation.TargetLocalID) {
					return nil, navigationReadError("verify target identity")
				} else if !h.canViewTranscript(ctx, user, target) {
					navigation.Status = schema.RelationshipNavigationInaccessible
				} else {
					publicID := schema.TranscriptID(uuidFromPg(target.ID).String())
					navigation.TranscriptID = &publicID
					navigation.Status = schema.RelationshipNavigationResolved
					if relation.Kind == schema.SessionRelationshipContextFrom {
						// No public revision authority is stored on transcripts. A
						// content hash cannot verify a historical public entry jump.
						navigation.Status = schema.RelationshipNavigationGeneralLinkOnly
						navigation.Anchor = &schema.PublicSourceAnchor{Kind: schema.PublicSourceAnchorGeneral}
					}
				}
			}
		}
		result = append(result, navigation)
	}
	return result, nil
}

func navigationReadError(step string) error {
	return fmt.Errorf("relationship navigation failed in handler.relationshipNavigation during %s because stored evidence or its database lookup could not be verified; metadata was withheld and captured content was not changed; restore the database and validated relationship projections, then retry", step)
}
