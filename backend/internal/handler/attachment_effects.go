package handler

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/digest"
	"github.com/peasant-labs/village/backend/internal/github"
	"github.com/peasant-labs/village/backend/internal/matcher"
	"github.com/peasant-labs/village/backend/internal/promptattach"
)

// The sticky comment renders at the comment tier and the check summary at the
// check tier, so neither exceeds what GitHub accepts for its surface.
const attachmentCheckTitle = "peasant / prompts"

// attachAcceptedAndPost moves an attachment to attached: it re-applies #109's
// acceptance to the pull request as it is now, binds exactly the transcripts it
// accepts, posts the digest, and only then moves the state.
//
// Attaching never changes who can read a transcript. It binds, and every surface
// then shows a transcript's prompts only to readers of that transcript: the
// pull request's comment and check show the title and prompts only of the
// transcripts anyone can read, and the pull request page narrows the stored
// digest to what its viewer can open.
//
// Ordering is the contract. GitHub is called before the state moves, so a
// failure answers 502 and leaves the attachment in its state for a retry; the
// bindings this attempt made are removed, so the retry binds afresh. Bindings
// an earlier, detached cycle left are cleared before this one binds, and a
// failure does not bring them back.
func (h *Handler) attachAcceptedAndPost(ctx context.Context, attachment sqlc.PullRequestAttachment) (sqlc.PullRequestAttachment, error) {
	repo, err := h.resolveAttachmentRepository(ctx, h.queries, attachment)
	if err != nil {
		return attachment, err
	}
	match, commitSet, err := h.matchAttachmentCandidates(ctx, attachment, repo)
	if err != nil {
		return attachment, err
	}
	if len(match.Accepted) == 0 {
		// Nothing is accepted, so there is nothing to attach. The attachment
		// stays where it is rather than becoming an empty attachment.
		return attachment, errAttachmentNothingAccepted
	}
	acceptedIDs := acceptedTranscriptIDs(match)
	digests, err := h.buildAttachmentDigests(ctx, attachment, acceptedIDs, commitSet, match)
	if err != nil {
		return attachment, err
	}

	narrowed, err := h.clearEarlierBindings(ctx, attachment, repo)
	if err != nil {
		return attachment, err
	}
	// A cleared binding an older attach widened may have narrowed its
	// transcript; any other pull request listing it is reposted now, while this
	// attachment is not attached and so never among the ones the repost locks.
	h.repostAfterNarrowing(ctx, narrowed)
	if err := h.bindAcceptedTranscripts(ctx, attachment, match.Accepted, 0); err != nil {
		return attachment, err
	}
	commentID, checkRunID, err := h.postAttachment(ctx, attachment, repo, digests, false)
	if err != nil {
		// Posting failed, so the attachment never attached: remove what this
		// attempt bound, so the retry binds afresh.
		if unbindErr := h.unbindTranscripts(ctx, attachment.ID, acceptedIDs); unbindErr != nil {
			return attachment, fmt.Errorf("%w: and the bindings could not be removed, so a retry will bind again: %v", err, unbindErr)
		}
		return attachment, err
	}

	encoded, err := encodeDigest(digests.complete)
	if err != nil {
		return attachment, err
	}
	if err := h.storeAttachmentArtifacts(ctx, sqlc.SetPullRequestAttachmentArtifactsParams{
		ID:         attachment.ID,
		HeadSha:    attachment.HeadSha,
		CommentID:  optionalInt8(commentID),
		CheckRunID: optionalInt8(checkRunID),
		Digest:     encoded,
	}); err != nil {
		return attachment, fmt.Errorf("could not record what the attachment posted: %w", err)
	}

	updated, err := promptattach.Transition(ctx, h.queries, attachment.ID, promptattach.Attached)
	if err != nil {
		return attachment, err
	}

	// An owner who changed a transcript's visibility while this attach was
	// posting found no attached attachment to repost, so the listing is checked
	// here, still under the attachment lock the caller holds, and reposted when
	// it no longer matches. It never fails the attach, which has committed.
	if h.audienceMovedSince(ctx, digests) {
		if err := h.refreshAttachedAttachment(ctx, updated, repo, updated.HeadSha, true); err != nil {
			log.Printf("pull request attachment repost after a visibility change during attach failed: %v", err)
		}
	}
	return updated, nil
}

// audienceMovedSince reports whether any transcript the digests were built from
// now lists differently on the pull request, or changes whether anyone besides
// its author can read it. A read that fails counts as moved, so the repost
// recomputes rather than trusting a listing it could not check.
func (h *Handler) audienceMovedSince(ctx context.Context, digests attachmentDigests) bool {
	for id, before := range digests.visibility {
		parsed, err := uuid.Parse(string(id))
		if err != nil {
			return true
		}
		row, err := h.queries.GetTranscriptByID(ctx, pgtype.UUID{Bytes: parsed, Valid: true})
		if err != nil {
			return true
		}
		readable, err := h.readableBeyondItsAuthor(ctx, row)
		if err != nil || listedOnPullRequest(row.Visibility) != listedOnPullRequest(before) ||
			readable != digests.readable[id] {
			return true
		}
	}
	return false
}

// repostAfterNarrowing reposts every attached pull request that binds a
// transcript a release just narrowed, so none keeps listing it. Each attachment
// is taken under its own lock; failures are logged, never returned, because the
// change that narrowed the transcript has already committed.
func (h *Handler) repostAfterNarrowing(ctx context.Context, narrowed []pgtype.UUID) {
	if err := h.refreshAttachmentsForTranscripts(ctx, narrowed); err != nil {
		log.Printf("pull request attachment refresh after a release narrowed transcripts failed: %v", err)
	}
}

// errAttachmentNothingAccepted is returned when the acceptance policy accepts
// nothing: the caller answers 409 (the pull request is not in a state this
// action can complete) rather than attaching an empty set.
var errAttachmentNothingAccepted = errors.New("no transcript was accepted for this pull request, so there is nothing to attach")

// bindAcceptedTranscripts binds each accepted transcript to the attachment and
// records the visibility it holds now. It changes nothing about the transcript:
// its visibility and its shares are the owner's, and attaching is not a way to
// move them. Ownership is re-checked here, so the lifecycle never binds a
// transcript the attachment's author does not own.
// startPosition is where this batch's positions begin. A refresh passes the
// number of transcripts already bound, because positions are unique per
// attachment and re-using one would collide with an existing binding.
func (h *Handler) bindAcceptedTranscripts(ctx context.Context, attachment sqlc.PullRequestAttachment, accepted []matcher.AcceptedTranscript, startPosition int) error {
	for index, item := range accepted {
		transcriptID, err := uuid.Parse(string(item.TranscriptID))
		if err != nil {
			return fmt.Errorf("an accepted transcript id was not a uuid: %w", err)
		}
		if err := h.bindOneTranscript(ctx, attachment, pgtype.UUID{Bytes: transcriptID, Valid: true}, startPosition+index); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) bindOneTranscript(ctx context.Context, attachment sqlc.PullRequestAttachment, transcriptID pgtype.UUID, position int) error {
	transcript, err := h.queries.GetTranscriptByID(ctx, transcriptID)
	if err != nil {
		return fmt.Errorf("could not read an accepted transcript before binding it: %w", err)
	}
	if transcript.OwnerID != attachment.AuthorID {
		return errors.New("refusing to bind a transcript the attachment's author does not own")
	}

	// The visibility is read under the publish lock every writer of this
	// transcript's audience holds, so a republish's brief private window is
	// never what the binding records.
	return h.withPublishLocks(ctx, transcript.OwnerID, transcript.LocalID, nil, func(conn *pgxpool.Conn) error {
		return h.inTxAsOnConn(ctx, conn, transcript.OwnerID, func(q Querier) error {
			pre, err := q.GetTranscriptGovernanceForUpdate(ctx, transcriptID)
			if err != nil {
				return fmt.Errorf("could not lock an accepted transcript before binding it: %w", err)
			}
			if err := q.AttachPullRequestTranscript(ctx, sqlc.AttachPullRequestTranscriptParams{
				AttachmentID:       attachment.ID,
				TranscriptID:       transcriptID,
				Position:           int32(position),
				PreviousVisibility: pre.Visibility,
			}); err != nil {
				return fmt.Errorf("could not bind an accepted transcript to the attachment: %w", err)
			}
			return nil
		})
	})
}

// listedOnPullRequest reports whether a transcript's title and prompts may
// appear in the pull request's comment and check. Those are read by whoever can
// read the pull request, and Village cannot know who that is for a private
// repository, so only a transcript anyone can read is listed there. Every other
// attached transcript is a row with its author, the commits it traced, and a
// link to read it on village, where its own readers can open it.
func listedOnPullRequest(visibility string) bool {
	return visibility == dbVisibilityPublic
}

// readableBeyondItsAuthor asks the same grant sources as canViewTranscript.
// A shared label alone grants nothing: an accepted member or a collective owner
// with review access must actually exist besides the transcript's owner.
func (h *Handler) readableBeyondItsAuthor(ctx context.Context, transcript sqlc.Transcript) (bool, error) {
	if transcript.Visibility == dbVisibilityPublic {
		return true, nil
	}
	owners, err := h.queries.ListGroupOwnersForTranscript(ctx, transcript.ID)
	if err != nil {
		return false, fmt.Errorf("could not read collective review grants for a prompt check: %w", err)
	}
	for _, owner := range owners {
		if owner != transcript.OwnerID {
			return true, nil
		}
	}
	if transcript.Visibility != dbVisibilityShared {
		return false, nil
	}
	groups, err := h.queries.ListApprovedTranscriptShareGroups(ctx, transcript.ID)
	if err != nil {
		return false, fmt.Errorf("could not read accepted grants for a prompt check: %w", err)
	}
	for _, group := range groups {
		members, err := h.queries.ListGroupMembers(ctx, sqlc.ListGroupMembersParams{GroupID: group, ViewerIsOwner: true})
		if err != nil {
			return false, fmt.Errorf("could not read accepted members for a prompt check: %w", err)
		}
		for _, member := range members {
			if member.ID != transcript.OwnerID && canReadData(member.Role, "contributors") {
				return true, nil
			}
		}
	}
	return false, nil
}

// disclosureRank orders the visibility tiers by how widely they disclose:
// private, then shared with a collective, then public. Only the release of a
// binding an older attach widened still compares tiers.
func disclosureRank(value string) int {
	switch value {
	case dbVisibilityPrivate:
		return 0
	case dbVisibilityShared:
		return 1
	case dbVisibilityPublic:
		return 2
	default:
		// An unknown tier ranks above every known one, so a restore never
		// downgrades it. Adding a tier to the menu must update this ordering
		// deliberately.
		return 3
	}
}

// narrowestVisibility is the restore rule: the recorded tier is applied only
// when it is not wider than the tier now, so an owner who narrowed the
// transcript while it was attached keeps that narrowing. An unknown current tier
// is left alone for the same reason.
func narrowestVisibility(current, recorded string) string {
	if disclosureRank(current) >= 3 {
		return current
	}
	if disclosureRank(recorded) < disclosureRank(current) {
		return recorded
	}
	return current
}

// unbindTranscripts removes the bindings one attempt made, undoing an attach or
// refresh whose posting failed. Those bindings changed nothing about their
// transcripts, so removing them is the whole undo.
func (h *Handler) unbindTranscripts(ctx context.Context, attachmentID pgtype.UUID, transcripts []schema.TranscriptID) error {
	for _, transcriptID := range transcripts {
		parsed, err := uuid.Parse(string(transcriptID))
		if err != nil {
			return fmt.Errorf("a bound transcript id was not a uuid: %w", err)
		}
		if err := h.queries.DeletePullRequestAttachmentTranscript(ctx, sqlc.DeletePullRequestAttachmentTranscriptParams{
			AttachmentID: attachmentID,
			TranscriptID: pgtype.UUID{Bytes: parsed, Valid: true},
		}); err != nil {
			return fmt.Errorf("could not remove a binding an unposted attach made: %w", err)
		}
	}
	return nil
}

// clearEarlierBindings removes the bindings an earlier cycle left before a new
// attach binds. Detach keeps its bindings so a detached pull request still lists
// what it held; a new attach starts afresh, so each binding records the
// visibility that is true when it is made. A binding an older attach widened is
// released first, exactly as a detach releases it, and the transcripts that
// release narrowed are returned for the caller to repost.
func (h *Handler) clearEarlierBindings(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository) ([]pgtype.UUID, error) {
	bindings, err := h.queries.ListPullRequestAttachmentTranscripts(ctx, attachment.ID)
	if err != nil {
		return nil, fmt.Errorf("could not read the bindings an earlier attach left: %w", err)
	}
	if len(bindings) == 0 {
		return nil, nil
	}
	narrowed, err := h.releaseWidenedBindings(ctx, bindings, repo.groupID)
	if err != nil {
		return narrowed, err
	}
	if err := h.queries.DeletePullRequestAttachmentTranscripts(ctx, attachment.ID); err != nil {
		return narrowed, fmt.Errorf("could not clear the bindings an earlier attach left: %w", err)
	}
	return narrowed, nil
}

// releaseWidenedBindings releases every binding an older attach widened, and
// returns the transcripts a release narrowed. Bindings made since are skipped:
// attaching them changed nothing, so neither does letting them go.
func (h *Handler) releaseWidenedBindings(ctx context.Context, bindings []sqlc.PullRequestAttachmentTranscript, groupID pgtype.UUID) ([]pgtype.UUID, error) {
	var narrowed []pgtype.UUID
	for _, binding := range bindings {
		if !binding.AttachWidened {
			continue
		}
		changed, err := h.releaseWidenedBinding(ctx, binding, groupID)
		if err != nil {
			return narrowed, err
		}
		if changed {
			narrowed = append(narrowed, binding.TranscriptID)
		}
	}
	return narrowed, nil
}

// postAttachment posts the check and the one sticky comment, returning the ids
// to record. Both render the same rows by the same rule: every attached
// transcript is a row, and only a transcript anyone can read shows its title
// and prompts. The collective's post_prompts_check toggle gates both: off, nothing
// is posted or edited and the recorded ids are kept, so a later detach still
// removes what an earlier post left. A GitHub failure is returned unwrapped so
// the caller answers 502 with the state unchanged.
func (h *Handler) postAttachment(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository, digests attachmentDigests, headChanged bool) (commentID, checkRunID int64, err error) {
	if !repo.postCheck {
		return attachment.CommentID.Int64, attachment.CheckRunID.Int64, nil
	}
	if h.gh == nil {
		return 0, 0, errAttachmentGitHubUnavailable
	}

	comment, err := digest.Render(digests.forPullRequest(), digest.CommentTier)
	if err != nil {
		return 0, 0, fmt.Errorf("could not render the digest for the comment: %w", err)
	}
	summary, err := digest.Render(digests.forPullRequest(), digest.CheckRunTier)
	if err != nil {
		return 0, 0, fmt.Errorf("could not render the digest for the check: %w", err)
	}
	if len(digests.complete.Items) == 0 {
		comment = "No transcripts remain attached to this pull request."
		summary = comment
	}

	request := github.CheckRunRequest{
		HeadSHA:    attachment.HeadSha,
		ExternalID: attachmentExternalID(attachment),
		Conclusion: github.PromptCheckConclusion(repo.checkMode, true, digests.readableBeyondAuthor),
		Title:      attachmentCheckTitle,
		Summary:    summary,
		DetailsURL: h.pullRequestPageURL(attachment),
		Actions:    github.PromptCheckActions(),
	}
	// A check run's head SHA is fixed when it is created; an update cannot move
	// it. So a new head needs a NEW run, or the new commit has no check at all
	// and a required check never satisfies branch protection.
	createRun := !attachment.CheckRunID.Valid || headChanged
	if !createRun {
		run, readErr := h.gh.GetCheckRun(ctx, repo.installationID, attachment.RepoOwner, attachment.RepoName, attachment.CheckRunID.Int64)
		if readErr != nil && !github.IsNotFound(readErr) {
			return 0, 0, fmt.Errorf("%w: reading the recorded check run: %v", errAttachmentGitHub, readErr)
		}
		createRun = github.IsNotFound(readErr) || run == nil || run.HeadSHA != attachment.HeadSha
	}
	if createRun {
		created, createErr := h.gh.CreateCheckRun(ctx, repo.installationID, attachment.RepoOwner, attachment.RepoName, request)
		if createErr != nil {
			return 0, 0, fmt.Errorf("%w: creating the check run: %v", errAttachmentGitHub, createErr)
		}
		checkRunID = created.ID
	} else {
		updated, updateErr := h.gh.UpdateCheckRun(ctx, repo.installationID, attachment.RepoOwner, attachment.RepoName, attachment.CheckRunID.Int64, request)
		if updateErr != nil {
			return 0, 0, fmt.Errorf("%w: updating the check run: %v", errAttachmentGitHub, updateErr)
		}
		checkRunID = updated.ID
	}

	posted, err := h.gh.UpsertIssueComment(ctx, repo.installationID, attachment.RepoOwner, attachment.RepoName, int(attachment.Number), attachment.CommentID.Int64, comment)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: posting the comment: %v", errAttachmentGitHub, err)
	}
	return posted.ID, checkRunID, nil
}

// postPreviewComment posts or edits the sticky comment a pull request carries
// while its author decides: how many of their transcripts match and a link to
// review them on village. It names no transcript and no prompt. It returns the
// comment id to record; with the collective's toggle off it posts nothing and
// returns the recorded id unchanged.
func (h *Handler) postPreviewComment(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository, matches int) (int64, error) {
	if !repo.postCheck {
		return attachment.CommentID.Int64, nil
	}
	if h.gh == nil {
		return 0, errAttachmentGitHubUnavailable
	}
	posted, err := h.gh.UpsertIssueComment(ctx, repo.installationID, attachment.RepoOwner, attachment.RepoName, int(attachment.Number), attachment.CommentID.Int64,
		digest.RenderPreview(matches, h.pullRequestPageURL(attachment)))
	if err != nil {
		return 0, fmt.Errorf("%w: posting the preview comment: %v", errAttachmentGitHub, err)
	}
	return posted.ID, nil
}

// attachmentExternalID is the stable reference GitHub shows for the run, so a
// reader (and a later lookup) can tie it back to the attachment.
// optionalInt8 is the GitHub id column's nullable form: zero means "no object",
// which is how a create is told from an edit.
func optionalInt8(value int64) pgtype.Int8 {
	if value == 0 {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: value, Valid: true}
}

func attachmentExternalID(attachment sqlc.PullRequestAttachment) string {
	return "village-attachment-" + uuid.UUID(attachment.ID.Bytes).String()
}

// detachAttachment deletes the comment, resets the check, releases every
// binding an older attach widened, and moves the attachment to detached. GitHub
// is called first: a failure leaves the state and every transcript untouched for
// a retry.
//
// A binding made since attaching stopped widening changed nothing about its
// transcript, so detaching it changes nothing either: the owner's shares and
// visibility, including a widening they chose while it was attached, stay as
// they are. The bindings themselves are kept, so the detached pull request still
// lists the transcripts it held.
func (h *Handler) detachAttachment(ctx context.Context, attachment sqlc.PullRequestAttachment) (sqlc.PullRequestAttachment, error) {
	repo, err := h.resolveAttachmentRepository(ctx, h.queries, attachment)
	if err != nil {
		return attachment, err
	}
	if h.gh == nil {
		return attachment, errAttachmentGitHubUnavailable
	}

	if attachment.CommentID.Valid {
		if err := h.gh.DeleteIssueComment(ctx, repo.installationID, attachment.RepoOwner, attachment.RepoName, attachment.CommentID.Int64); err != nil && !github.IsNotFound(err) {
			return attachment, fmt.Errorf("%w: deleting the comment: %v", errAttachmentGitHub, err)
		}
		// A 404 means the comment is already gone, which is the outcome detach
		// wants; failing here would leave a partial detach stuck forever.
	}
	if attachment.CheckRunID.Valid {
		if _, err := h.gh.UpdateCheckRun(ctx, repo.installationID, attachment.RepoOwner, attachment.RepoName, attachment.CheckRunID.Int64, github.CheckRunRequest{
			Conclusion: github.CheckConclusionNeutral,
			Title:      attachmentCheckTitle,
			Summary:    "Prompts are no longer attached to this pull request.",
		}); err != nil && !github.IsNotFound(err) {
			return attachment, fmt.Errorf("%w: resetting the check run: %v", errAttachmentGitHub, err)
		}
	}

	bindings, err := h.queries.ListPullRequestAttachmentTranscripts(ctx, attachment.ID)
	if err != nil {
		return attachment, fmt.Errorf("could not read the attachment's transcripts before releasing them: %w", err)
	}
	narrowed, err := h.releaseWidenedBindings(ctx, bindings, repo.groupID)
	if err != nil {
		return attachment, err
	}

	// The posted objects are gone or reset; keep the digest but drop the ids so
	// a later attach creates rather than edits a deleted comment.
	if err := h.storeAttachmentArtifacts(ctx, sqlc.SetPullRequestAttachmentArtifactsParams{
		ID:         attachment.ID,
		HeadSha:    attachment.HeadSha,
		CommentID:  pgtype.Int8{},
		CheckRunID: pgtype.Int8{},
		Digest:     attachment.Digest,
	}); err != nil {
		return attachment, fmt.Errorf("could not clear the attachment's posted ids: %w", err)
	}

	updated, err := promptattach.Transition(ctx, h.queries, attachment.ID, promptattach.Detached)
	if err != nil {
		return attachment, err
	}

	// A released binding narrowed its transcript, so any other pull request that
	// lists it must stop. This attachment is detached by now, so the refresh
	// never takes its lock, and it never fails the detach the author asked for.
	h.repostAfterNarrowing(ctx, narrowed)
	return updated, nil
}

// releaseWidenedBinding undoes what an older attach did to one transcript, and
// reports whether its visibility narrowed. Attaching used to widen: it opened an
// approved share to the linking collective and raised the transcript to the
// tier the repository required, recording the tier it had before. Only a
// binding marked attach_widened (every binding that existed when migration 044
// ran) is released; nothing made since is.
//
// One transaction, under the publish lock every writer of the transcript's
// audience holds, and attributed to the owner:
//   - the share the attach opened is withdrawn, so a concurrent unshare of the
//     same pair waits instead of racing it to the next ledger ordinal;
//   - the recorded tier is restored when it is narrower than the tier now, so an
//     owner who narrowed the transcript while it was attached keeps that. A
//     restore to private becomes shared when a collective still holds a live
//     submission the owner made, so detaching never takes away their own grant;
//   - the binding is marked released, so a later detach restores nothing twice.
func (h *Handler) releaseWidenedBinding(ctx context.Context, binding sqlc.PullRequestAttachmentTranscript, groupID pgtype.UUID) (bool, error) {
	transcript, err := h.queries.GetTranscriptByID(ctx, binding.TranscriptID)
	if err != nil {
		return false, fmt.Errorf("could not read a bound transcript before releasing it: %w", err)
	}
	var narrowed bool
	err = h.withPublishLocks(ctx, transcript.OwnerID, transcript.LocalID, nil, func(conn *pgxpool.Conn) error {
		return h.inTxAsOnConn(ctx, conn, transcript.OwnerID, func(q Querier) error {
			narrowed = false
			// The binding is re-read under the lock: a concurrent detach or attach
			// may have released or cleared it since the caller listed it, and a
			// released binding is never restored a second time.
			current, err := q.GetPullRequestAttachmentTranscript(ctx, sqlc.GetPullRequestAttachmentTranscriptParams{
				AttachmentID: binding.AttachmentID,
				TranscriptID: binding.TranscriptID,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("could not re-read a bound transcript before releasing it: %w", err)
			}
			if !current.AttachWidened {
				return nil
			}
			if groupID.Valid {
				if _, err := withdrawLiveShare(ctx, q, binding.TranscriptID, groupID); err != nil {
					return fmt.Errorf("could not retract the collective's share: %w", err)
				}
			}
			pre, err := q.GetTranscriptGovernanceForUpdate(ctx, binding.TranscriptID)
			if err != nil {
				return fmt.Errorf("could not lock a bound transcript before restoring it: %w", err)
			}
			target := narrowestVisibility(pre.Visibility, current.PreviousVisibility)
			if target == dbVisibilityPrivate && pre.Visibility != dbVisibilityPrivate {
				live, err := q.TranscriptHasLiveShareAttempt(ctx, binding.TranscriptID)
				if err != nil {
					return fmt.Errorf("could not read the transcript's remaining submissions: %w", err)
				}
				if live {
					target = dbVisibilityShared
				}
			}
			if target != pre.Visibility {
				if _, err := applyMetadataPatch(ctx, q, binding.TranscriptID, metadataPatch{Visibility: &target}); err != nil {
					return fmt.Errorf("could not restore a transcript's recorded visibility: %w", err)
				}
				narrowed = true
			}
			if err := q.ReleasePullRequestAttachmentTranscript(ctx, sqlc.ReleasePullRequestAttachmentTranscriptParams{
				AttachmentID: binding.AttachmentID,
				TranscriptID: binding.TranscriptID,
			}); err != nil {
				return fmt.Errorf("could not record that a widened binding was released: %w", err)
			}
			return nil
		})
	})
	if err != nil {
		return false, err
	}
	return narrowed, nil
}
