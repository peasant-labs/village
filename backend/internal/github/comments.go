package github

import (
	"context"
	"fmt"
	"net/http"
)

// IssueComment is the comment Village created or edited. The REST API calls a
// pull request an issue, so these are issue comments.
type IssueComment struct {
	ID      int64
	HTMLURL string
	Body    string
}

type issueCommentResponse struct {
	ID      int64  `json:"id"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
}

func (r issueCommentResponse) comment() *IssueComment {
	return &IssueComment{ID: r.ID, HTMLURL: r.HTMLURL, Body: r.Body}
}

// CreateIssueComment posts a new comment on the pull request. It is called once
// per pull request; later updates edit that same comment through
// UpdateIssueComment, so a later push never posts a second comment.
func (c *Client) CreateIssueComment(ctx context.Context, installationID int64, owner, name string, number int, body string) (*IssueComment, error) {
	if number <= 0 {
		return nil, fmt.Errorf("github: create issue comment: pull request number must be positive")
	}

	var out issueCommentResponse
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, name, number)
	if err := c.doInstallationJSON(ctx, installationID, "create issue comment", http.MethodPost, path, map[string]string{"body": body}, &out); err != nil {
		return nil, err
	}
	return out.comment(), nil
}

// UpdateIssueComment edits an existing comment in place. The comment id is the
// stored one, so the same comment is updated across pushes.
func (c *Client) UpdateIssueComment(ctx context.Context, installationID int64, owner, name string, commentID int64, body string) (*IssueComment, error) {
	if commentID <= 0 {
		return nil, fmt.Errorf("github: update issue comment: comment id must be positive")
	}

	var out issueCommentResponse
	path := fmt.Sprintf("/repos/%s/%s/issues/comments/%d", owner, name, commentID)
	if err := c.doInstallationJSON(ctx, installationID, "update issue comment", http.MethodPatch, path, map[string]string{"body": body}, &out); err != nil {
		return nil, err
	}
	return out.comment(), nil
}

// DeleteIssueComment removes the comment, which is what detaching does.
func (c *Client) DeleteIssueComment(ctx context.Context, installationID int64, owner, name string, commentID int64) error {
	if commentID <= 0 {
		return fmt.Errorf("github: delete issue comment: comment id must be positive")
	}

	path := fmt.Sprintf("/repos/%s/%s/issues/comments/%d", owner, name, commentID)
	return c.doInstallationJSON(ctx, installationID, "delete issue comment", http.MethodDelete, path, nil, nil)
}

// UpsertIssueComment keeps one sticky comment per pull request: it edits the
// recorded comment when there is one, and creates the first one only when there
// is not. existingCommentID is the stored comment id, zero when none exists.
//
// The branch lives here rather than at the call site so no caller can post a
// second comment for a later push by forgetting to look up the id.
//
// The guarantee rests on the caller persisting the id the create returned. A
// stored id whose comment was deleted out of band (by a user, say) makes the
// edit fail with a 404 rather than silently posting a second comment; the
// caller decides whether to clear the id and create again.
func (c *Client) UpsertIssueComment(ctx context.Context, installationID int64, owner, name string, number int, existingCommentID int64, body string) (*IssueComment, error) {
	if existingCommentID > 0 {
		return c.UpdateIssueComment(ctx, installationID, owner, name, existingCommentID, body)
	}
	return c.CreateIssueComment(ctx, installationID, owner, name, number, body)
}
