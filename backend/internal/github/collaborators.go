package github

import (
	"context"
	"fmt"
	"net/http"
)

// GetUserLogin resolves a GitHub account id to the login that account has now.
//
// The account id never changes; a login can, and a freed one can later be taken
// by someone else. Callers that were handed a stored identity therefore key on
// the id and ask for the login only when GitHub has to be told who is asking,
// rather than storing a login that could come to name a different account.
func (c *Client) GetUserLogin(ctx context.Context, installationID int64, accountID string) (string, error) {
	var body struct {
		Login string `json:"login"`
	}
	if err := c.doInstallationJSON(ctx, installationID, "resolve a github account id to its login", http.MethodGet, "/user/"+accountID, nil, &body); err != nil {
		return "", err
	}
	if body.Login == "" {
		return "", fmt.Errorf("github: account %s resolved to an empty login", accountID)
	}
	return body.Login, nil
}

// GetCollaboratorPermission asks what one user may do in one repository. GitHub
// answers "admin", "write", "read", or "none" — the only place that fact exists,
// which is why a private repository's readers are asked for it rather than
// recorded.
func (c *Client) GetCollaboratorPermission(ctx context.Context, installationID int64, owner, name, login string) (string, error) {
	var body struct {
		Permission string `json:"permission"`
	}
	path := fmt.Sprintf("/repos/%s/%s/collaborators/%s/permission", owner, name, login)
	if err := c.doInstallationJSON(ctx, installationID, "get a repository permission", http.MethodGet, path, nil, &body); err != nil {
		return "", err
	}
	return body.Permission, nil
}
