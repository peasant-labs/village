package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// anonymousAnnotatorName replaces a human annotator's name when the viewer may
// not see that person. It is the label the web client renders for an anonymous
// person, so a pulled or listed annotation reads the same way everywhere.
const anonymousAnnotatorName = "anon"

// publicOwners is the resolved answer for every person one response shows,
// keyed by the internal account id. An id that was not resolved reads as
// anonymous, so a missing row can never surface an identity.
type publicOwners map[pgtype.UUID]schema.VillagePublicOwner

// of returns the person as the viewer may see them.
func (p publicOwners) of(id pgtype.UUID) schema.VillagePublicOwner {
	if owner, ok := p[id]; ok {
		return owner
	}
	return schema.AnonymousVillageOwner()
}

// resolvePublicOwners is the one place Village decides how a person is shown to
// a viewer: their public profile, or anonymous. Every handler that sends an
// owner, member, contributor, or annotation author calls it, and the rule
// itself lives in one SQL statement (ListPublicOwnersForViewer) that a page
// reads once for all its people.
//
// A failed read is returned as an error, never as an anonymous or a profile
// guess: the caller refuses the response instead of serving one built on an
// unknown answer.
func (h *Handler) resolvePublicOwners(ctx context.Context, viewer *AuthUser, ids []pgtype.UUID) (publicOwners, error) {
	resolved := publicOwners{}
	unique := make([]pgtype.UUID, 0, len(ids))
	seen := make(map[pgtype.UUID]bool, len(ids))
	for _, id := range ids {
		if !id.Valid || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return resolved, nil
	}
	rows, err := h.queries.ListPublicOwnersForViewer(ctx, sqlc.ListPublicOwnersForViewerParams{
		ViewerID:  viewerID(viewer),
		PersonIds: unique,
	})
	if err != nil {
		return nil, fmt.Errorf("handler.resolvePublicOwners: the profile visibility of %d people could not be read; no response was built from a guessed identity; retry the request: %w", len(unique), err)
	}
	for _, row := range rows {
		resolved[row.ID] = publicOwnerFromRow(row)
	}
	return resolved, nil
}

// resolvePublicOwner resolves a single person.
func (h *Handler) resolvePublicOwner(ctx context.Context, viewer *AuthUser, id pgtype.UUID) (schema.VillagePublicOwner, error) {
	owners, err := h.resolvePublicOwners(ctx, viewer, []pgtype.UUID{id})
	if err != nil {
		return schema.VillagePublicOwner{}, err
	}
	return owners.of(id), nil
}

func publicOwnerFromRow(row sqlc.ListPublicOwnersForViewerRow) schema.VillagePublicOwner {
	if !row.ProfileVisible {
		return schema.AnonymousVillageOwner()
	}
	return publicProfile(row.ID, row.GithubUsername, row.DisplayName, row.AvatarUrl)
}

// publicProfile builds the profile shape from stored account columns. A row
// that cannot form a valid profile (no username) is shown as anonymous rather
// than emitted half-formed.
func publicProfile(id pgtype.UUID, username string, displayName, avatarURL pgtype.Text) schema.VillagePublicOwner {
	if !id.Valid || strings.TrimSpace(username) == "" {
		return schema.AnonymousVillageOwner()
	}
	return schema.NewVillagePublicProfile(
		schema.VillageUUID(uuid.UUID(id.Bytes).String()),
		username,
		pgTextPointer(displayName),
		pgTextPointer(avatarURL),
	)
}

// publicProfileOfUser is the profile shape of an account the viewer may
// already see in full (a discoverable person, or the viewer themselves). It
// narrows the stored row to the four public fields.
func publicProfileOfUser(user sqlc.User) schema.VillagePublicOwner {
	return publicProfile(user.ID, user.GithubUsername, user.DisplayName, user.AvatarUrl)
}

// withholdSessionRepository is withholdRepository for a grouped session row:
// every repository field of an anonymous owner's session becomes null.
func withholdSessionRepository(session *schema.VillageTranscript, owner schema.VillagePublicOwner) {
	if owner.IsProfile() {
		return
	}
	session.GitBranch, session.GitRemote, session.ProjectHash, session.ProjectName = nil, nil, nil, nil
	session.ProjectDisplayName, session.ProjectNameSource, session.ProjectRemoteLabel = nil, nil, nil
}

// annotatorNameFor applies the viewer's answer to a human annotation's
// annotator name. An agent or rule annotator names a tool, not a person, and
// is left as stored.
func annotatorNameFor(summary schema.AnnotationSummary, author schema.VillagePublicOwner) string {
	if summary.AnnotatorKind == schema.AnnotatorHuman && !author.IsProfile() {
		return anonymousAnnotatorName
	}
	return summary.AnnotatorName
}

// ownerFilter is the owner and organization filters of a transcript list,
// narrowed to the accounts the viewer may see as a profile. Active is false
// when the request names neither filter. An active filter with no accounts
// matches nothing: a list filtered by an owner the viewer may not see is the
// same empty answer as a filter by a name nobody holds, so the filter cannot
// tie anonymous transcripts to a person or an organization.
type ownerFilter struct {
	Active   bool
	OwnerIDs []pgtype.UUID
}

func (h *Handler) resolveOwnerFilter(ctx context.Context, viewer *AuthUser, ownerName, org string) (ownerFilter, error) {
	filter := ownerFilter{OwnerIDs: []pgtype.UUID{}}
	if ownerName == "" && org == "" {
		return filter, nil
	}
	filter.Active = true
	var candidates []pgtype.UUID
	if ownerName != "" {
		user, err := h.queries.GetUserByUsername(ctx, ownerName)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return filter, nil
			}
			return filter, fmt.Errorf("handler.resolveOwnerFilter: the owner filter could not be read; no list was returned; retry the request: %w", err)
		}
		// The filter has always matched the handle exactly; the lookup is
		// case-insensitive, so keep the exact comparison.
		if user.GithubUsername != ownerName {
			return filter, nil
		}
		candidates = []pgtype.UUID{user.ID}
	}
	if org != "" {
		members, err := h.queries.ListUserIDsWithVisibleOrg(ctx, org)
		if err != nil {
			return filter, fmt.Errorf("handler.resolveOwnerFilter: the organization filter could not be read; no list was returned; retry the request: %w", err)
		}
		if candidates == nil {
			candidates = members
		} else {
			inOrg := make(map[pgtype.UUID]bool, len(members))
			for _, id := range members {
				inOrg[id] = true
			}
			kept := []pgtype.UUID{}
			for _, id := range candidates {
				if inOrg[id] {
					kept = append(kept, id)
				}
			}
			candidates = kept
		}
	}
	owners, err := h.resolvePublicOwners(ctx, viewer, candidates)
	if err != nil {
		return filter, err
	}
	for _, id := range candidates {
		if owners.of(id).IsProfile() {
			filter.OwnerIDs = append(filter.OwnerIDs, id)
		}
	}
	return filter, nil
}
