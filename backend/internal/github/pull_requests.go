package github

import (
	"context"
	"fmt"
	"net/http"
)

// PullRequest is the pull request metadata the attachment lifecycle needs, in
// one shape for every event type: a comment or a check-run button does not carry
// the head SHA or the base/head repositories, so the lifecycle reads them here
// rather than guessing.
type PullRequest struct {
	Number   int
	RepoID   int64
	HeadSHA  string
	HeadRef  string
	HeadRepo string
	BaseRepo string
	AuthorID int64
	IsFork   bool
	State    string
	Merged   bool
}

// GetPullRequest reads one pull request through the installation token. It is
// what lets a `/peasant attach` comment and a check-run button address the same
// attachment a `pull_request` event would, without trusting anything the webhook
// payload happens to omit.
func (c *Client) GetPullRequest(ctx context.Context, installationID int64, owner, name string, number int) (*PullRequest, error) {
	if number <= 0 {
		return nil, fmt.Errorf("github: get pull request: pull request number must be positive")
	}

	var out struct {
		Number int `json:"number"`
		Head   struct {
			SHA  string `json:"sha"`
			Ref  string `json:"ref"`
			Repo struct {
				ID    int64  `json:"id"`
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Repo struct {
				ID    int64  `json:"id"`
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repo"`
		} `json:"base"`
		User struct {
			ID int64 `json:"id"`
		} `json:"user"`
		State  string `json:"state"`
		Merged bool   `json:"merged"`
	}

	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, name, number)
	if err := c.doInstallationJSON(ctx, installationID, "get pull request", http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}

	pr := &PullRequest{
		Number:   out.Number,
		RepoID:   out.Base.Repo.ID,
		HeadSHA:  out.Head.SHA,
		HeadRef:  out.Head.Ref,
		HeadRepo: out.Head.Repo.Name,
		BaseRepo: out.Base.Repo.Name,
		AuthorID: out.User.ID,
		IsFork:   out.Head.Repo.ID != 0 && out.Base.Repo.ID != 0 && out.Head.Repo.ID != out.Base.Repo.ID,
		State:    out.State,
		Merged:   out.Merged,
	}
	return pr, nil
}
