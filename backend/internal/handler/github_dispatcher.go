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
	if _, err := d.h.queries.GetCollectiveRepositoryByRepo(ctx, sqlc.GetCollectiveRepositoryByRepoParams{Owner: owner, Name: name}); err != nil {
		// No collective linked this repository, so there is no attachment to
		// refresh and nothing to report.
		return nil
	}
	headRemote := owner + "/" + name
	if payload.IsFork() {
		headRemote = payload.PullRequest.Head.Repo.FullName
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
	})
}

// CheckRun handles a clicked check-run button. The button's identifier is the
// command, and the click is a command from the person who clicked.
//
// A check_run payload carries no author_association, so a button click can only
// ever be the author's: a non-author who clicks is ignored, and the comment path
// is what records a repository member's request. A pull request from a fork also
// reports no pull_requests on its check run, so buttons do not resolve there
// either; the comment path still works.
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
	number := payload.PullRequestNumber()
	if number <= 0 {
		// A pull request from a fork reports no pull_requests on its check run,
		// so the button cannot address an attachment from here. The comment path
		// still works for a fork; this is a known limitation, pinned by a fixture.
		return nil
	}
	owner := payload.Repository.Owner.Login
	name := payload.Repository.Name
	senderID := payload.Sender.ID

	switch identifier {
	case github.CheckActionIdentifierAttach:
		return d.h.applyPromptCommand(ctx, matcher.CommandAttach, owner, name, number, senderID, "")
	case github.CheckActionIdentifierDetach:
		return d.h.applyPromptCommand(ctx, matcher.CommandDetach, owner, name, number, senderID, "")
	case github.CheckActionIdentifierRefresh:
		return d.h.refreshAttachmentForCommand(ctx, owner, name, number, senderID)
	default:
		return nil
	}
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
	if payload.Action != "created" {
		// An edit or a deletion carries the body too; acting on those would
		// re-execute a command the author has already withdrawn.
		return nil
	}
	command, ok := matcher.ParseCommand(payload.Comment.Body)
	if !ok {
		return nil
	}
	owner := payload.Repository.Owner.Login
	return d.h.applyPromptCommand(ctx, command, owner, payload.Repository.Name, payload.Issue.Number, payload.Sender.ID, payload.Comment.AuthorAssociation)
}
