package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// collectivePeople is the roster, contributor list, and (for an owner) the
// join requests of one collective, each person shown as the viewer may see
// them. The flat and grouped collective reads both build it here, so the two
// shapes of one collective cannot disagree about who is shown.
type collectivePeople struct {
	Members        []schema.VillageCollectiveMember
	Contributors   []schema.VillageCollectiveContributor
	PendingMembers []schema.VillageCollectiveMember // nil unless the viewer owns the collective
}

// collectivePeopleFor reads the three lists and resolves every person on them
// in one batch. The roster and contributor queries leave out a person who is
// not discoverable unless the viewer owns the collective or is that person,
// and join requests are read for an owner only.
func (h *Handler) collectivePeopleFor(ctx context.Context, viewer *AuthUser, groupID pgtype.UUID, viewerIsOwner bool) (collectivePeople, error) {
	people := collectivePeople{Members: []schema.VillageCollectiveMember{}, Contributors: []schema.VillageCollectiveContributor{}}
	members, err := h.queries.ListGroupMembers(ctx, sqlc.ListGroupMembersParams{GroupID: groupID, ViewerIsOwner: viewerIsOwner, ViewerID: viewerID(viewer)})
	if err != nil {
		return people, fmt.Errorf("handler.collectivePeopleFor: the roster could not be read: %w", err)
	}
	contributors, err := h.queries.ListGroupContributors(ctx, sqlc.ListGroupContributorsParams{GroupID: groupID, ViewerIsOwner: viewerIsOwner, ViewerID: viewerID(viewer)})
	if err != nil {
		return people, fmt.Errorf("handler.collectivePeopleFor: the contributors could not be read: %w", err)
	}
	var pending []sqlc.ListGroupPendingMembersRow
	if viewerIsOwner {
		pending, err = h.queries.ListGroupPendingMembers(ctx, groupID)
		if err != nil {
			return people, fmt.Errorf("handler.collectivePeopleFor: the join requests could not be read: %w", err)
		}
	}

	ids := make([]pgtype.UUID, 0, len(members)+len(contributors)+len(pending))
	for _, row := range members {
		ids = append(ids, row.ID)
	}
	for _, row := range contributors {
		ids = append(ids, row.ID)
	}
	for _, row := range pending {
		ids = append(ids, row.ID)
	}
	owners, err := h.resolvePublicOwners(ctx, viewer, ids)
	if err != nil {
		return people, err
	}

	for _, row := range members {
		people.Members = append(people.Members, collectiveMember(row.Role, row.JoinedAt, owners.of(row.ID), row.GithubOrgs))
	}
	for _, row := range contributors {
		people.Contributors = append(people.Contributors, schema.VillageCollectiveContributor{
			Contributor: owners.of(row.ID), TranscriptCount: row.TranscriptCount,
		})
	}
	if viewerIsOwner {
		people.PendingMembers = []schema.VillageCollectiveMember{}
		for _, row := range pending {
			people.PendingMembers = append(people.PendingMembers, collectiveMember(row.Role, row.JoinedAt, owners.of(row.ID), row.GithubOrgs))
		}
	}
	return people, nil
}

// collectiveMember builds one roster row. An anonymous member lists no
// organizations, because an organization login can identify a person.
func collectiveMember(role string, joinedAt pgtype.Timestamptz, member schema.VillagePublicOwner, orgs []string) schema.VillageCollectiveMember {
	visibleOrgs := []string{}
	if member.IsProfile() {
		visibleOrgs = append(visibleOrgs, orgs...)
	}
	return schema.VillageCollectiveMember{
		Role: schema.VillageCollectiveRole(role), JoinedAt: joinedAt.Time,
		Member: member, GithubOrgs: visibleOrgs,
	}
}

// collectiveWithCreator serves a stored collective row with its creator as the
// viewer may see them. The rows the collective routes read carry created_by,
// an account id; on the wire that column is replaced by creator, a public
// owner, and every other column is served exactly as the row encodes it.
type collectiveWithCreator[T any] struct {
	row     T
	creator schema.VillagePublicOwner
}

func (c collectiveWithCreator[T]) MarshalJSON() ([]byte, error) {
	encoded, err := json.Marshal(c.row)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	delete(fields, "created_by")
	creator, err := json.Marshal(c.creator)
	if err != nil {
		return nil, err
	}
	fields["creator"] = creator
	return json.Marshal(fields)
}

// collectivesWithCreators resolves the creators of a page of collectives in one
// read and pairs each row with its creator.
func collectivesWithCreators[T any](ctx context.Context, h *Handler, viewer *AuthUser, rows []T, createdBy func(T) pgtype.UUID) ([]collectiveWithCreator[T], error) {
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, createdBy(row))
	}
	creators, err := h.resolvePublicOwners(ctx, viewer, ids)
	if err != nil {
		return nil, err
	}
	out := make([]collectiveWithCreator[T], 0, len(rows))
	for _, row := range rows {
		out = append(out, collectiveWithCreator[T]{row: row, creator: creators.of(createdBy(row))})
	}
	return out, nil
}

// collectiveWithItsCreator is collectivesWithCreators for one row.
func collectiveWithItsCreator[T any](ctx context.Context, h *Handler, viewer *AuthUser, row T, createdBy func(T) pgtype.UUID) (collectiveWithCreator[T], error) {
	out, err := collectivesWithCreators(ctx, h, viewer, []T{row}, createdBy)
	if err != nil {
		return collectiveWithCreator[T]{}, err
	}
	return out[0], nil
}
