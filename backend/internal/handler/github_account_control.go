package handler

import (
	"context"
)

// viewerIdentity is the GitHub identity a caller's account carries, resolved
// once per request: the account id they signed in as, and the ids of the
// organisations that account belongs to.
//
// Ids, never logins. A login can be renamed, and a freed one can later be taken
// by another account, so a stored login is not what a permission is decided on.
// The private-repository check for transcripts resolves its viewer the same way,
// for the same reason.
type viewerIdentity struct {
	githubID int64
	orgIDs   map[int64]struct{}
}

// loadViewerIdentity reads the caller's GitHub account id and the ids of the
// organisations their account belongs to. A caller who signed in through another
// provider has no GitHub identity here, so they control no installation account.
func (h *Handler) loadViewerIdentity(ctx context.Context, user *AuthUser) (*viewerIdentity, error) {
	identity := &viewerIdentity{orgIDs: make(map[int64]struct{})}
	row, err := h.queries.GetUserByID(ctx, user.PgID())
	if err != nil {
		return nil, err
	}
	if row.Provider == githubProvider {
		identity.githubID = row.GithubID
	}
	orgs, err := h.queries.ListUserAllOrgs(ctx, user.PgID())
	if err != nil {
		return nil, err
	}
	for _, org := range orgs {
		identity.orgIDs[org.OrgID] = struct{}{}
	}
	return identity, nil
}

// controlsAccount reports whether the caller's account is the account an
// installation belongs to, or an organisation that account belongs to. An
// installation whose account id is unknown is never controlled.
//
// The bar is control, not membership of a collective and not a matching handle:
// a GitHub App installation belongs to the organisation or person that installed
// it, and the data behind it is theirs to hand out.
func (v *viewerIdentity) controlsAccount(accountID int64) bool {
	if accountID == 0 {
		return false
	}
	if v.githubID != 0 && v.githubID == accountID {
		return true
	}
	_, ok := v.orgIDs[accountID]
	return ok
}
