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
// binding, and the same state machine the routes use, so a click, a push, and a
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

	_, resolved, err := h.resolveGitHubActor(ctx, senderID)
	if err != nil {
		return err
	}
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
		return h.authorAttachOrPreview(ctx, attachment, repo, pull.headSHA, false)
	default:
		return nil
	}
}

// authorAttachOrPreview applies the author's click: stop at a preview for the
// author to confirm, or record a wait when nothing is accepted yet.
//
// consented marks the callers that are not a person acting on this pull
// request: the hook running the author's own publish, and the author's standing
// choice to link their transcripts when a pull request opens
// (users.auto_attach_pull_requests). Both complete a new or waiting request
// directly, because the author's publish or setting is the consent and neither
// has a page to confirm on. Everything a person clicks previews, on every
// repository, so the author sees which of their transcripts the pull request
// will name before it does.
func (h *Handler) authorAttachOrPreview(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository, headSHA string, consented bool) error {
	if promptattach.State(attachment.State) == promptattach.Attached {
		// The prompts are already attached, so there is nothing to ask. A repeat
		// action refreshes the digest for the current head instead of trying to
		// move an attached attachment back to preview, which the state table
		// refuses and the webhook handler swallows. This is the click a check run
		// created before the attach action was removed still offers.
		return h.refreshAttachedAttachment(ctx, attachment, repo, headSHA, false)
	}

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

	// A publish or the author's setting completes a new or WAITING request
	// without a click. A preview is different: the author has been asked and has
	// not answered, so neither answers for them.
	state := promptattach.State(attachment.State)
	if consented && (state == promptattach.Requested || state == promptattach.Waiting) {
		if _, err := h.attachAcceptedAndPost(ctx, attachment); err != nil {
			return err
		}
		return nil
	}
	return h.previewWithMatch(ctx, attachment, repo, match, commitSet)
}

// previewWithMatch computes and stores the digest without binding anything, and
// moves the attachment to preview. The pull request is told only that the
// author has matching transcripts to review on village: the preview comment
// names no transcript and no prompt, and a later attach edits that same comment
// in place (a detach deletes it). GitHub is called before anything is stored, so
// a failure leaves the attachment as it was for a retry.
func (h *Handler) previewWithMatch(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository, match matcher.Result, commitSet []string) error {
	acceptedIDs := acceptedTranscriptIDs(match)
	digests, err := h.buildAttachmentDigests(ctx, attachment, acceptedIDs, commitSet, match)
	if err != nil {
		return err
	}
	encoded, err := encodeDigest(digests.complete)
	if err != nil {
		return err
	}
	commentID, err := h.postPreviewComment(ctx, attachment, repo, len(acceptedIDs))
	if err != nil {
		return err
	}
	if err := h.storeAttachmentArtifacts(ctx, sqlc.SetPullRequestAttachmentArtifactsParams{
		ID:         attachment.ID,
		HeadSha:    attachment.HeadSha,
		CommentID:  optionalInt8(commentID),
		CheckRunID: attachment.CheckRunID,
		Digest:     encoded,
	}); err != nil {
		return fmt.Errorf("could not store the preview digest: %w", err)
	}
	if promptattach.State(attachment.State) != promptattach.Preview {
		if _, err := promptattach.Transition(ctx, h.queries, attachment.ID, promptattach.Preview); err != nil {
			return err
		}
	}
	return nil
}

// refreshAttachedAttachment recomputes an attached attachment for a new head:
// newly accepted transcripts are bound, the digest is rebuilt from every bound
// transcript (so a transcript whose recorded commits no longer resolve stays as
// history rather than disappearing), and the comment is edited and the check
// updated for the new head SHA. An acceptance result that adds nothing leaves
// the digest as it was rather than posting a changed one.
// forceRepost re-renders and reposts even when nothing new was accepted and the
// head has not moved. The publish and webhook paths leave it false, because an
// unrelated publish must not edit the comment; the visibility change sets it,
// because there the edit IS the point.
func (h *Handler) refreshAttachedAttachment(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository, headSHA string, forceRepost bool) error {
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
	if len(newlyAccepted) == 0 && !forceRepost && (headSHA == "" || headSHA == attachment.HeadSha) {
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
		if err := h.bindAcceptedTranscripts(ctx, attachment, newlyAccepted, nextPosition); err != nil {
			return err
		}
		for _, accepted := range newlyAccepted {
			ordered = append(ordered, accepted.TranscriptID)
		}
	}
	if len(ordered) == 0 && !forceRepost {
		return nil
	}

	digests, err := h.buildAttachmentDigests(ctx, attachment, ordered, commitSet, match)
	if err != nil {
		return err
	}
	if headSHA == "" {
		headSHA = attachment.HeadSha
	}
	scoped := attachment
	scoped.HeadSha = headSHA

	commentID, checkRunID, err := h.postAttachment(ctx, scoped, repo, digests, headSHA != attachment.HeadSha)
	if err != nil {
		if unbindErr := h.unbindTranscripts(ctx, attachment.ID, acceptedTranscriptIDs(matcher.Result{Accepted: newlyAccepted})); unbindErr != nil {
			return fmt.Errorf("%w: and the new bindings could not be removed, so a retry will bind again: %v", err, unbindErr)
		}
		return err
	}

	encoded, err := encodeDigest(digests.complete)
	if err != nil {
		return err
	}
	if err := h.storeAttachmentArtifacts(ctx, sqlc.SetPullRequestAttachmentArtifactsParams{
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

// refreshAttachmentsForTranscriptVisibility reposts the attachments that bind a
// transcript whose visibility just changed, so a pull request stops listing
// prompts that are no longer public, lists them when they become public, and
// its check says whether reviewers can read what is attached.
//
// It is the publish hook's sibling and follows the same discipline: the work
// runs on a detached, bounded context, each attachment is taken under its own
// lock and re-read inside it, and failures are collected rather than raised. The
// owner's update must not fail because GitHub was unreachable; the next refresh
// retries, exactly as the publish path assumes.
func (h *Handler) refreshAttachmentsForTranscriptVisibility(ctx context.Context, transcriptID pgtype.UUID) error {
	return h.refreshAttachmentsForTranscripts(ctx, []pgtype.UUID{transcriptID})
}

// One completion budget covers the whole batch, including its database reads.
// An attachment binding several affected transcripts is refreshed just once.
func (h *Handler) refreshAttachmentsForTranscripts(ctx context.Context, transcriptIDs []pgtype.UUID) error {
	ctx, cancel := attachmentWorkContext(ctx)
	defer cancel()
	var attachments []sqlc.PullRequestAttachment
	var failures []error
	seen := map[pgtype.UUID]bool{}
	for _, id := range transcriptIDs {
		rows, err := h.queries.ListAttachmentsBindingTranscript(ctx, id)
		if err != nil {
			failures = append(failures, fmt.Errorf("could not read affected prompt attachments: %w", err))
			continue
		}
		for _, row := range rows {
			if !seen[row.ID] {
				seen[row.ID] = true
				attachments = append(attachments, row)
			}
		}
	}
	if err := h.refreshKnownAttachments(ctx, attachments); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

// refreshKnownAttachments takes an attachment snapshot from before a grant or
// binding disappeared, then re-reads each row under its lock before posting.
func (h *Handler) refreshKnownAttachments(ctx context.Context, attachments []sqlc.PullRequestAttachment) error {
	var failures []error
	for _, attachment := range attachments {
		if attachment.State != string(promptattach.Attached) {
			continue
		}
		repo, err := h.resolveAttachmentRepository(ctx, h.queries, attachment)
		if err != nil {
			// Unbound or unlinked: nothing is advertising anything.
			continue
		}
		err = h.withAttachmentLock(ctx, attachment.ID, func() error {
			fresh, err := h.queries.GetPullRequestAttachment(ctx, attachment.ID)
			if err != nil {
				return err
			}
			if promptattach.State(fresh.State) != promptattach.Attached {
				return nil
			}
			return h.refreshAttachedAttachment(ctx, fresh, repo, fresh.HeadSha, true)
		})
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
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
	hookCtx, cancel := attachmentWorkContext(ctx)
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
				return h.refreshAttachedAttachment(ctx, fresh, repo, fresh.HeadSha, false)
			}
			return h.authorAttachOrPreview(ctx, fresh, repo, fresh.HeadSha, true)
		})
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// autoAttachOpenedPullRequest links the author's transcripts to a pull request
// that just opened, when the author chose that (users.auto_attach_pull_requests)
// and belongs to the collective that linked the repository. It reports whether
// it took the pull request on; anything else records nothing and posts nothing,
// exactly as an unclicked pull request always has.
//
// It runs the author's own attach path without a preview: an accepted match is
// attached and posted, and a pull request nothing matches yet waits for the
// publish that completes it. An attachment the author already previewed or
// detached is theirs to decide and is left alone; one already attached (a
// redelivery of the same event) is refreshed, which posts nothing new when
// nothing changed. Attaching never changes who can read a transcript, so no
// click is needed to consent to that.
func (h *Handler) autoAttachOpenedPullRequest(ctx context.Context, link sqlc.CollectiveRepository, pull attachmentPull) (bool, error) {
	// The webhook's own request is cancelled if GitHub stops waiting, and an
	// attach that stops after posting its comment but before recording it would
	// post a second one on the next delivery. So the work runs detached and
	// bounded, like the publish hook it shares a path with.
	hookCtx, cancel := attachmentWorkContext(ctx)
	defer cancel()
	ctx = hookCtx

	authorID, known, err := h.resolveGitHubActor(ctx, pull.authorID)
	if err != nil {
		return false, err
	}
	if !known {
		return false, nil
	}
	author, err := h.queries.GetUserByID(ctx, authorID)
	if err != nil {
		return false, fmt.Errorf("could not read the pull request author's settings: %w", err)
	}
	if !author.AutoAttachPullRequests {
		return false, nil
	}
	if !h.isCollectiveMember(ctx, authorID, link.GroupID) {
		// The setting reaches repositories the author's own collectives link.
		return false, nil
	}
	repo, err := h.resolveAttachmentRepositoryForLink(ctx, h.queries, link)
	if err != nil {
		return true, err
	}
	attachment, err := h.ensureAttachment(ctx, link.GroupID, authorID, pull, pgtype.Int8{})
	if err != nil {
		return true, err
	}
	if attachment.AuthorID != authorID {
		return true, errors.New("the stored attachment's author does not match the pull request's author")
	}
	return true, h.withAttachmentLock(ctx, attachment.ID, func() error {
		fresh, err := h.queries.GetPullRequestAttachment(ctx, attachment.ID)
		if err != nil {
			return err
		}
		switch promptattach.State(fresh.State) {
		case promptattach.Preview, promptattach.Detached:
			return nil
		}
		return h.authorAttachOrPreview(ctx, fresh, repo, pull.headSHA, true)
	})
}

// syncAttachmentForPullRequest handles a pull_request event: a push to a pull
// request that already attached refreshes it for the new head. Opening a pull
// request that no click and no author setting has touched records nothing and
// posts nothing.
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
	return h.refreshAttachedAttachment(ctx, attachment, repo, pull.headSHA, false)
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
	sender, resolved, err := h.resolveGitHubActor(ctx, senderID)
	if err != nil {
		return err
	}
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
		return h.refreshAttachedAttachment(ctx, fresh, repo, pull.headSHA, true)
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
func (h *Handler) resolveGitHubActor(ctx context.Context, githubID int64) (pgtype.UUID, bool, error) {
	row, err := h.queries.GetUserByProviderIdentity(ctx, sqlc.GetUserByProviderIdentityParams{
		Provider:       "github",
		ProviderUserID: strconv.FormatInt(githubID, 10),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Nobody signed in with this GitHub id: an unresolved sender.
			return pgtype.UUID{}, false, nil
		}
		return pgtype.UUID{}, false, fmt.Errorf("could not resolve the GitHub actor's Village account: %w", err)
	}
	return row.ID, true, nil
}

// userByGitHubID resolves the pull request author to the user the attachment
// belongs to. A pull request whose author never signed in cannot own an
// attachment, so this fails closed.
func (h *Handler) userByGitHubID(ctx context.Context, githubID int64) (pgtype.UUID, error) {
	id, ok, err := h.resolveGitHubActor(ctx, githubID)
	if err != nil {
		return pgtype.UUID{}, err
	}
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
