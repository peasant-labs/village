package handler

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// Organization membership is what authorizes a member to a collective that
// admits verified members, and it is read from the member's own record of
// GitHub organizations — never from whether they publish one.
//
// Publishing is a separate, opt-in act (`user_github_orgs.visible`, off by
// default): it decides whether an affiliation appears on the person's profile
// and in village's aggregates. Tying the two together made the profile switch
// an access-control switch, so a member who did not want an affiliation public
// could not join the collective of an organization they belonged to, and
// switching the profile toggle off silently withdrew their standing.
//
// Reading the record is also the honest signal available: the server never
// re-queries GitHub for a member's organizations, so a published affiliation
// was never fresher or better checked than an unpublished one.

// hasOrgMembership reports whether the person has this GitHub organization on
// record, published or not.
func hasOrgMembership(ctx context.Context, q Querier, userID pgtype.UUID, orgLogin string) (bool, error) {
	orgs, err := q.ListUserAllOrgs(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, org := range orgs {
		if strings.EqualFold(org.OrgLogin, orgLogin) {
			return true, nil
		}
	}
	return false, nil
}

// hasAnyOrgMembership reports whether the person has at least one GitHub
// organization on record. It answers the collective that names no organization
// but still admits verified members only.
func hasAnyOrgMembership(ctx context.Context, q Querier, userID pgtype.UUID) (bool, error) {
	orgs, err := q.ListUserAllOrgs(ctx, userID)
	if err != nil {
		return false, err
	}
	return len(orgs) > 0, nil
}
