package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/matcher"
	"github.com/peasant-labs/village/backend/internal/promptattach"
)

// The attachment lifecycle's non-HTTP entry points: a webhook-driven command
// (a `/peasant attach` comment or a check-run button), a pull request event, and
// the publish hook. They all funnel into the same acceptance result, the same
// widening, and the same state machine the routes use, so a click, a push, and a
// publish cannot diverge.

// attachmentPull is the pull request facts an attachment is scoped to. A comment
// or a check-run button does not carry them, so they are read from GitHub rather
// than guessed from whatever the payload happens to include.
type attachmentPull struct {
	repoOwner    string
	repoName     string
	githubRepoID int64
	number       int
	headSHA      string
	headRef      string
	baseRemote   string
	headRemote   string
	isFork       bool
	authorID     int64
	state        string
}

// resolveAttachmentPull reads a pull request's facts through the installation of
// the collective that linked its repository.
func (h *Handler) resolveAttachmentPull(ctx context.Context, repo attachmentRepository, owner, name string, number int) (attachmentPull, error) {
	if h.gh == nil {
		return attachmentPull{}, errAttachmentGitHubUnavailable
	}
	pr, err := h.gh.GetPullRequest(ctx, repo.installationID, owner, name, number)
	if err != nil {
		return attachmentPull{}, fmt.Errorf("%w: reading the pull request: %v", errAttachmentGitHub, err)
	}
	baseRemote := owner + "/" + name
	headRemote := baseRemote
	if pr.IsFork && pr.HeadRepoFullName != "" {
		headRemote = pr.HeadRepoFullName
	}
	return attachmentPull{
		repoOwner:    owner,
		repoName:     name,
		githubRepoID: pr.RepoID,
		number:       number,
		authorID:     pr.AuthorID,
		headSHA:      pr.HeadSHA,
		headRef:      pr.HeadRef,
		baseRemote:   baseRemote,
		headRemote:   headRemote,
		isFork:       pr.IsFork,
	}, nil
}

// ensureAttachment creates the attachment for a pull request if it does not
// exist, or returns the existing one. A re-observation refreshes the head and
// the fork remotes but never re-binds the collective or resets the state.
func (h *Handler) ensureAttachment(ctx context.Context, groupID, authorID pgtype.UUID, pull attachmentPull, requester pgtype.Int8) (sqlc.PullRequestAttachment, error) {
	existing, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{
		Lower:   pull.repoOwner,
		Lower_2: pull.repoName,
		Number:  int32(pull.number),
	})
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.PullRequestAttachment{}, fmt.Errorf("could not read the pull request attachment: %w", err)
	}
	created, err := h.queries.CreatePullRequestAttachment(ctx, sqlc.CreatePullRequestAttachmentParams{
		GroupID:           groupID,
		RepoOwner:         pull.repoOwner,
		RepoName:          pull.repoName,
		GithubRepoID:      pull.githubRepoID,
		Number:            int32(pull.number),
		HeadSha:           pull.headSHA,
		BaseRemote:        pull.baseRemote,
		HeadRemote:        pull.headRemote,
		AuthorID:          authorID,
		RequesterGithubID: requester,
	})
	if err != nil {
		return sqlc.PullRequestAttachment{}, fmt.Errorf("could not record the pull request attachment: %w", err)
	}
	return created, nil
}

// applyPromptCommand is the whole command policy for a webhook-driven click.
// It resolves the sender, decides with the matcher's authorization table who may
// act, and then does exactly what that actor is allowed to do. Unresolved
// senders, unknown commands, and repositories no collective linked are ignored.
func (h *Handler) applyPromptCommand(ctx context.Context, command matcher.Command, owner, name string, number int, senderID int64, association string) error {
	link, err := h.queries.GetCollectiveRepositoryByRepo(ctx, sqlc.GetCollectiveRepositoryByRepoParams{Owner: owner, Name: name})
	if err != nil {
		// Not linked to any collective: nothing to attach to. Not an error.
		return nil
	}

	repo, err := h.resolveAttachmentRepositoryForLink(ctx, h.queries, link)
	if err != nil {
		return err
	}
	pull, err := h.resolveAttachmentPull(ctx, repo, owner, name, number)
	if err != nil {
		return err
	}

	_, resolved := h.resolveGitHubActor(ctx, senderID)
	action := matcher.Authorize(matcher.Actor{
		SenderResolved:            resolved,
		SenderGitHubID:            senderID,
		PullRequestAuthorGitHubID: pull.authorID,
		AuthorAssociation:         association,
	}, command)
	if action == matcher.ActionIgnore {
		return nil
	}

	author, err := h.userByGitHubID(ctx, pull.authorID)
	if err != nil {
		return err
	}

	requester := pgtype.Int8{}
	if action == matcher.ActionRequest {
		requester = pgtype.Int8{Int64: senderID, Valid: true}
	}
	attachment, err := h.ensureAttachment(ctx, link.GroupID, author, pull, requester)
	if err != nil {
		return err
	}
	if attachment.AuthorID != author {
		// The stored attachment belongs to a different author than GitHub now
		// reports. Fail closed rather than act on someone else's attachment.
		return errors.New("the stored attachment's author does not match the pull request's author")
	}

	if action == matcher.ActionRequest {
		// A non-author with standing asked the author to attach. The row is
		// already 'requested' (or further along); record who asked and stop.
		return h.ensureRequestRecorded(ctx, attachment, senderID)
	}

	switch command {
	case matcher.CommandDetach:
		if !promptattach.CanTransition(promptattach.State(attachment.State), promptattach.Detached) {
			return nil
		}
		_, err := h.detachAttachment(ctx, attachment)
		return err
	case matcher.CommandAttach:
		return h.authorAttachOrPreview(ctx, attachment, repo)
	default:
		return nil
	}
}

// authorAttachOrPreview applies the author's click: attach directly, stop at a
// preview, or record a wait when nothing is accepted yet.
func (h *Handler) authorAttachOrPreview(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository) error {
	match, commitSet, err := h.matchAttachmentCandidates(ctx, attachment, repo)
	if err != nil {
		return err
	}

	if len(match.Accepted) == 0 {
		// Nothing is accepted, so nothing may be exposed. A state that can wait
		// waits; one that cannot is left alone.
		state := promptattach.State(attachment.State)
		if state == promptattach.Waiting || !promptattach.CanTransition(state, promptattach.Waiting) {
			return nil
		}
		_, err := promptattach.Transition(ctx, h.queries, attachment.ID, promptattach.Waiting)
		return err
	}

	user, err := h.queries.GetUserByID(ctx, attachment.AuthorID)
	if err != nil {
		return fmt.Errorf("could not read the author's settings: %w", err)
	}
	if repo.isPrivate && !user.PreviewBeforeAttach {
		if _, err := h.attachAcceptedAndPost(ctx, attachment); err != nil {
			return err
		}
		return nil
	}
	return h.previewWithMatch(ctx, attachment, match, commitSet)
}

// previewWithMatch computes and stores the digest without sharing, posting, or
// widening anything, and moves the attachment to preview.
func (h *Handler) previewWithMatch(ctx context.Context, attachment sqlc.PullRequestAttachment, match matcher.Result, commitSet []string) error {
	acceptedIDs := acceptedTranscriptIDs(match)
	value, err := h.buildAttachmentDigest(ctx, acceptedIDs, commitSet, match)
	if err != nil {
		return err
	}
	encoded, err := encodeDigest(value)
	if err != nil {
		return err
	}
	if err := h.queries.SetPullRequestAttachmentDigest(ctx, sqlc.SetPullRequestAttachmentDigestParams{ID: attachment.ID, Digest: encoded}); err != nil {
		return fmt.Errorf("could not store the preview digest: %w", err)
	}
	_, err = promptattach.Transition(ctx, h.queries, attachment.ID, promptattach.Preview)
	return err
}

// refreshAttachedAttachment recomputes an attached attachment for a new head:
// newly accepted transcripts are widened, the digest is rebuilt from every bound
// transcript (so a transcript whose recorded commits no longer resolve stays as
// history rather than disappearing), and the comment is edited and the check
// updated for the new head SHA. An acceptance result that adds nothing leaves
// the digest as it was rather than posting a changed one.
func (h *Handler) refreshAttachedAttachment(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository, headSHA string) error {
	match, commitSet, err := h.matchAttachmentCandidates(ctx, attachment, repo)
	if err != nil {
		return err
	}

	bound, err := h.queries.ListPullRequestAttachmentTranscripts(ctx, attachment.ID)
	if err != nil {
		return fmt.Errorf("could not read the attachment's transcripts before refreshing: %w", err)
	}
	boundIDs := map[schema.TranscriptID]bool{}
	ordered := make([]schema.TranscriptID, 0, len(bound))
	for _, binding := range bound {
		id := schema.TranscriptID(uuidFromPg(binding.TranscriptID).String())
		boundIDs[id] = true
		ordered = append(ordered, id)
	}

	var newlyAccepted []matcher.AcceptedTranscript
	for _, accepted := range match.Accepted {
		if !boundIDs[accepted.TranscriptID] {
			newlyAccepted = append(newlyAccepted, accepted)
		}
	}
	if len(newlyAccepted) == 0 && (headSHA == "" || headSHA == attachment.HeadSha) {
		// Nothing new is accepted and the head did not move: an unrelated
		// publish must not edit the comment or post the digest again.
		return nil
	}
	nextPosition := 0
	for _, binding := range bound {
		if int(binding.Position)+1 > nextPosition {
			nextPosition = int(binding.Position) + 1
		}
	}
	if len(newlyAccepted) > 0 {
		if err := h.widenAttachedTranscripts(ctx, attachment, repo, newlyAccepted, nextPosition); err != nil {
			return err
		}
		for _, accepted := range newlyAccepted {
			ordered = append(ordered, accepted.TranscriptID)
		}
	}
	if len(ordered) == 0 {
		// Nothing is bound and nothing was accepted: leave the attachment as it
		// is rather than posting an empty digest.
		return nil
	}

	value, err := h.buildAttachmentDigest(ctx, ordered, commitSet, match)
	if err != nil {
		return err
	}
	if headSHA == "" {
		headSHA = attachment.HeadSha
	}
	scoped := attachment
	scoped.HeadSha = headSHA

	commentID, checkRunID, err := h.postAttachment(ctx, scoped, repo, value, headSHA != attachment.HeadSha)
	if err != nil {
		if compensateErr := h.undoWidening(ctx, attachment.ID, acceptedTranscriptIDs(matcher.Result{Accepted: newlyAccepted})); compensateErr != nil {
			return fmt.Errorf("%w: and the widening could not be undone, so a retry will redo both: %v", err, compensateErr)
		}
		return err
	}

	encoded, err := encodeDigest(value)
	if err != nil {
		return err
	}
	if err := h.queries.SetPullRequestAttachmentArtifacts(ctx, sqlc.SetPullRequestAttachmentArtifactsParams{
		ID:         attachment.ID,
		HeadSha:    headSHA,
		CommentID:  optionalInt8(commentID),
		CheckRunID: optionalInt8(checkRunID),
		Digest:     encoded,
	}); err != nil {
		return fmt.Errorf("could not record the refreshed attachment: %w", err)
	}
	_, err = promptattach.Transition(ctx, h.queries, attachment.ID, promptattach.Attached)
	return err
}

// completeAttachmentsForPublishedTranscript is the publish hook: after a
// transcript is stored, the owner's attachments for its repository are re-read
// and the new transcript is put through the same acceptance policy. A waiting
// attachment completes when the acceptance accepts; an attached one is
// refreshed. An unrelated transcript accepts nothing and therefore changes
// nothing.
func (h *Handler) completeAttachmentsForPublishedTranscript(ctx context.Context, owner pgtype.UUID, repoName string) error {
	if repoName == "" {
		return nil
	}
	candidates, err := h.queries.ListAuthorAttachmentsForRepo(ctx, sqlc.ListAuthorAttachmentsForRepoParams{
		AuthorID: owner,
		RepoName: repoName,
		States:   []string{string(promptattach.Waiting), string(promptattach.Attached)},
	})
	if err != nil {
		return fmt.Errorf("could not read the owner's attachments for a published transcript: %w", err)
	}

	// The hook runs inside the publish request, so it must not inherit a context
	// a client disconnect cancels, and it must be bounded so an unreachable
	// GitHub cannot hold the publish open. One attachment failing must not stop
	// the others.
	hookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), attachmentHookTimeout)
	defer cancel()
	ctx = hookCtx

	var failures []error
	for _, attachment := range candidates {
		repo, err := h.resolveAttachmentRepository(ctx, h.queries, attachment)
		if err != nil {
			// Unbound or unlinked: nothing to complete or refresh.
			continue
		}
		err = h.withAttachmentLock(ctx, attachment.ID, func() error {
			fresh, err := h.queries.GetPullRequestAttachment(ctx, attachment.ID)
			if err != nil {
				return err
			}
			if promptattach.State(fresh.State) == promptattach.Attached {
				return h.refreshAttachedAttachment(ctx, fresh, repo, fresh.HeadSha)
			}
			return h.authorAttachOrPreview(ctx, fresh, repo)
		})
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// syncAttachmentForPullRequest handles a pull_request event: a push to a pull
// request that already attached refreshes it for the new head. Opening a pull
// request that no click has touched records nothing and posts nothing.
func (h *Handler) syncAttachmentForPullRequest(ctx context.Context, pull attachmentPull) error {
	if pull.state != "" && pull.state != "open" {
		// A closed or merged pull request has nothing to keep current.
		return nil
	}
	attachment, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{
		Lower:   pull.repoOwner,
		Lower_2: pull.repoName,
		Number:  int32(pull.number),
	})
	if err != nil {
		return nil
	}
	if promptattach.State(attachment.State) != promptattach.Attached {
		return nil
	}
	repo, err := h.resolveAttachmentRepository(ctx, h.queries, attachment)
	if err != nil {
		return err
	}
	return h.refreshAttachedAttachment(ctx, attachment, repo, pull.headSHA)
}

// refreshAttachmentForCommand recomputes an attached attachment and reposts it:
// the check run's Refresh button, and the author's recovery path when a
// publish-driven refresh failed. It is author-only, like every other change to
// an attachment, and a no-op unless something is attached.
func (h *Handler) refreshAttachmentForCommand(ctx context.Context, owner, name string, number int, senderID int64) error {
	attachment, err := h.queries.GetPullRequestAttachmentForPull(ctx, sqlc.GetPullRequestAttachmentForPullParams{
		Lower:   owner,
		Lower_2: name,
		Number:  int32(number),
	})
	if err != nil {
		return nil
	}
	if promptattach.State(attachment.State) != promptattach.Attached {
		return nil
	}
	sender, resolved := h.resolveGitHubActor(ctx, senderID)
	if !resolved || sender != attachment.AuthorID {
		return nil
	}
	repo, err := h.resolveAttachmentRepository(ctx, h.queries, attachment)
	if err != nil {
		return err
	}
	pull, err := h.resolveAttachmentPull(ctx, repo, owner, name, number)
	if err != nil {
		return err
	}
	return h.withAttachmentLock(ctx, attachment.ID, func() error {
		fresh, err := h.queries.GetPullRequestAttachment(ctx, attachment.ID)
		if err != nil {
			return err
		}
		if promptattach.State(fresh.State) != promptattach.Attached {
			return nil
		}
		return h.refreshAttachedAttachment(ctx, fresh, repo, pull.headSHA)
	})
}

// ensureRequestRecorded records who asked, leaving a request that has already
// moved further along untouched.
func (h *Handler) ensureRequestRecorded(ctx context.Context, attachment sqlc.PullRequestAttachment, requesterID int64) error {
	if attachment.RequesterGithubID.Valid && attachment.RequesterGithubID.Int64 == requesterID {
		return nil
	}
	_, err := h.queries.SetPullRequestAttachmentRequester(ctx, sqlc.SetPullRequestAttachmentRequesterParams{
		ID:                attachment.ID,
		RequesterGithubID: pgtype.Int8{Int64: requesterID, Valid: true},
	})
	return err
}

// resolveGitHubActor resolves a numeric GitHub id to a Village user, returning
// false when nobody signed in with it.
func (h *Handler) resolveGitHubActor(ctx context.Context, githubID int64) (pgtype.UUID, bool) {
	row, err := h.queries.GetUserByProviderIdentity(ctx, sqlc.GetUserByProviderIdentityParams{
		Provider:       "github",
		ProviderUserID: strconv.FormatInt(githubID, 10),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Nobody signed in with this GitHub id: an unresolved sender, not a
			// failure. Any other error is a real one and is reported by the
			// caller's own read, which fails closed the same way.
			return pgtype.UUID{}, false
		}
		return pgtype.UUID{}, false
	}
	return row.ID, true
}

// userByGitHubID resolves the pull request author to the user the attachment
// belongs to. A pull request whose author never signed in cannot own an
// attachment, so this fails closed.
func (h *Handler) userByGitHubID(ctx context.Context, githubID int64) (pgtype.UUID, error) {
	id, ok := h.resolveGitHubActor(ctx, githubID)
	if !ok {
		return pgtype.UUID{}, errors.New("the pull request's author has no Village account, so no attachment can be created for them")
	}
	return id, nil
}

// resolveAttachmentRepositoryForLink adapts a repository link to the repository
// context the effects use, reading the collective's check settings.
func (h *Handler) resolveAttachmentRepositoryForLink(ctx context.Context, q Querier, link sqlc.CollectiveRepository) (attachmentRepository, error) {
	group, err := q.GetGroupByID(ctx, link.GroupID)
	if err != nil {
		return attachmentRepository{}, fmt.Errorf("%w: %v", errAttachmentUnbound, err)
	}
	mode := promptattach.CheckMode(group.PromptsCheckMode)
	if !mode.Valid() {
		return attachmentRepository{}, fmt.Errorf("the collective's prompts check mode %q is not one of %s, so no check conclusion can be chosen",
			group.PromptsCheckMode, promptattach.CheckModeMenu())
	}
	return attachmentRepository{
		groupID:        link.GroupID,
		installationID: link.InstallationID,
		isPrivate:      link.IsPrivate,
		checkMode:      mode,
		postCheck:      group.PostPromptsCheck,
	}, nil
}

func acceptedTranscriptIDs(match matcher.Result) []schema.TranscriptID {
	ids := make([]schema.TranscriptID, 0, len(match.Accepted))
	for _, accepted := range match.Accepted {
		ids = append(ids, accepted.TranscriptID)
	}
	return ids
}

func encodeDigest(value schema.PromptDigest) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("could not encode the digest for storage: %w", err)
	}
	return encoded, nil
}
