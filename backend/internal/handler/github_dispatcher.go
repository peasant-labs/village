package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/github"
	"github.com/peasant-labs/village/backend/internal/matcher"
)

// promptCommandDispatcher is the production handling point for the GitHub App's
// subscribed events. It is deliberately thin: it decodes one payload, works out
// which pull request the event is about, and hands the decision to the
// attachment lifecycle, so a click, a comment, and a push cannot diverge from
// what the routes do.
//
// Installation events need nothing: the install handshake's callback has already
// recorded the installation.
type promptCommandDispatcher struct{ h *Handler }

func (promptCommandDispatcher) Installation(context.Context, github.Event) error { return nil }

// PullRequest refreshes an already-attached pull request when a new push arrives.
// Opening or reopening a pull request that nobody has attached records nothing
// by itself, which is why the click is the only thing that starts an attachment.
func (d promptCommandDispatcher) PullRequest(ctx context.Context, event github.Event) error {
	var payload github.WebhookPullRequestPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("the pull_request payload could not be decoded: %w", err)
	}
	switch payload.Action {
	case "opened", "reopened", "synchronize":
	default:
		return nil
	}

	owner := payload.Repository.Owner.Login
	name := payload.Repository.Name
	link, err := d.h.queries.GetCollectiveRepositoryByRepo(ctx, sqlc.GetCollectiveRepositoryByRepoParams{Owner: owner, Name: name})
	if err != nil {
		// No collective linked this repository, so there is no attachment to
		// refresh and nothing to report.
		return nil
	}
	headRemote := owner + "/" + name
	if payload.IsFork() {
		headRemote = payload.PullRequest.Head.Repo.Name + "/" + name
	}
	return d.h.syncAttachmentForPullRequest(ctx, attachmentPull{
		repoOwner:    owner,
		repoName:     name,
		githubRepoID: payload.Repository.ID,
		number:       payload.PullRequest.Number,
		headSHA:      payload.PullRequest.Head.SHA,
		headRef:      payload.PullRequest.Head.Ref,
		baseRemote:   owner + "/" + name,
		headRemote:   headRemote,
		isFork:       payload.IsFork(),
		authorID:     payload.PullRequest.User.ID,
	}, link.GroupID)
}

// CheckRun handles a clicked check-run button. The button's identifier is the
// command, and the click is a command from the person who clicked.
func (d promptCommandDispatcher) CheckRun(ctx context.Context, event github.Event) error {
	var payload github.WebhookCheckRunPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("the check_run payload could not be decoded: %w", err)
	}
	identifier := payload.RequestedActionIdentifier()
	if identifier == "" {
		// created / completed / rerequested are not commands.
		return nil
	}
	command := matcher.Command(identifier)
	if !command.Valid() {
		return nil
	}
	number := payload.PullRequestNumber()
	if number <= 0 {
		return nil
	}
	owner := payload.Repository.Owner.Login
	return d.h.applyPromptCommand(ctx, command, owner, payload.Repository.Name, number, payload.Sender.ID, "")
}

// IssueComment handles a `/peasant attach` or `/peasant detach` comment on a
// pull request. The comment's author_association is what gives a repository
// owner, member, or collaborator their standing to ask, so it is passed through
// rather than inferred.
func (d promptCommandDispatcher) IssueComment(ctx context.Context, event github.Event) error {
	var payload github.WebhookIssueCommentPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("the issue_comment payload could not be decoded: %w", err)
	}
	if !payload.IsPullRequest() {
		return nil
	}
	command, ok := matcher.ParseCommand(payload.Comment.Body)
	if !ok {
		return nil
	}
	owner := payload.Repository.Owner.Login
	return d.h.applyPromptCommand(ctx, command, owner, payload.Repository.Name, payload.Issue.Number, payload.Sender.ID, payload.Comment.AuthorAssociation)
}
