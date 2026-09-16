package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

// confirmAttachment moves a preview to attached: it re-applies #109's acceptance
// to the pull request as it is now, widens exactly the transcripts it accepts,
// posts the digest, and only then moves the state.
//
// Ordering is the contract. GitHub is called before the state moves, so a
// failure answers 502 and leaves the attachment in preview for a retry; the
// per-transcript widening is idempotent (a binding preserves its first snapshot,
// an already-approved share is not reopened) so a retry completes rather than
// duplicates.
func (h *Handler) confirmAttachment(ctx context.Context, attachment sqlc.PullRequestAttachment) (sqlc.PullRequestAttachment, error) {
	repo, err := h.resolveAttachmentRepository(ctx, h.queries, attachment)
	if err != nil {
		return attachment, err
	}
	match, commitSet, err := h.matchAttachmentCandidates(ctx, attachment, repo)
	if err != nil {
		return attachment, err
	}
	if len(match.Accepted) == 0 {
		// Nothing is accepted, so there is nothing to attach or expose. The
		// preview stays a preview rather than becoming an empty attachment.
		return attachment, errAttachmentNothingAccepted
	}
	value, err := h.buildAttachmentDigest(ctx, match, commitSet)
	if err != nil {
		return attachment, err
	}

	if err := h.widenAttachedTranscripts(ctx, attachment, repo, match.Accepted); err != nil {
		return attachment, err
	}
	commentID, checkRunID, err := h.postAttachment(ctx, attachment, repo, value)
	if err != nil {
		// Posting failed, so undo the widening: an attachment that never
		// attached must not leave a transcript shared. The retry then starts
		// from exactly the state the caller saw.
		if compensateErr := h.unwidenAttachedTranscripts(ctx, attachment); compensateErr != nil {
			return attachment, fmt.Errorf("%w: and the widening could not be undone, so a retry will redo both: %v", err, compensateErr)
		}
		return attachment, err
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return attachment, fmt.Errorf("could not encode the digest for storage: %w", err)
	}
	if err := h.queries.SetPullRequestAttachmentArtifacts(ctx, sqlc.SetPullRequestAttachmentArtifactsParams{
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
	return updated, nil
}

// errAttachmentNothingAccepted is returned when the acceptance policy accepts
// nothing: the caller answers 409 (the pull request is not in a state this
// action can complete) rather than attaching an empty set.
var errAttachmentNothingAccepted = errors.New("no transcript was accepted for this pull request, so there is nothing to attach")

// widenAttachedTranscripts binds each accepted transcript to the attachment,
// records the visibility it held, and widens it: shared with the collective
// whose owner opted in by linking the repository, or public when the repository
// is public. Ownership is re-checked here, so the lifecycle never widens a
// transcript the attachment's author does not own.
func (h *Handler) widenAttachedTranscripts(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository, accepted []matcher.AcceptedTranscript) error {
	for position, item := range accepted {
		transcriptID, err := uuid.Parse(string(item.TranscriptID))
		if err != nil {
			return fmt.Errorf("an accepted transcript id was not a uuid: %w", err)
		}
		if err := h.widenOneTranscript(ctx, attachment, repo, pgtype.UUID{Bytes: transcriptID, Valid: true}, position); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) widenOneTranscript(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository, transcriptID pgtype.UUID, position int) error {
	transcript, err := h.queries.GetTranscriptByID(ctx, transcriptID)
	if err != nil {
		return fmt.Errorf("could not read an accepted transcript before widening it: %w", err)
	}
	if transcript.OwnerID != attachment.AuthorID {
		return errors.New("refusing to widen a transcript the attachment's author does not own")
	}

	desired := dbVisibilityPublic
	if repo.isPrivate {
		desired = dbVisibilityShared
	}

	return h.withPublishLocks(ctx, transcript.OwnerID, transcript.LocalID, nil, func(conn *pgxpool.Conn) error {
		return h.inTxAsOnConn(ctx, conn, transcript.OwnerID, func(q Querier) error {
			// Read the LOCKED pre-image: the visibility recorded on the binding
			// must be the one the transcript held immediately before this
			// widening, not one read before the lock.
			pre, err := q.GetTranscriptGovernanceForUpdate(ctx, transcriptID)
			if err != nil {
				return fmt.Errorf("could not lock an accepted transcript before widening it: %w", err)
			}
			if err := q.AttachPullRequestTranscript(ctx, sqlc.AttachPullRequestTranscriptParams{
				AttachmentID:       attachment.ID,
				TranscriptID:       transcriptID,
				Position:           int32(position),
				PreviousVisibility: pre.Visibility,
			}); err != nil {
				return fmt.Errorf("could not bind an accepted transcript to the attachment: %w", err)
			}
			// Attaching WIDENS; it never narrows content the owner had already
			// made more public than the repository requires.
			target := widestVisibility(pre.Visibility, desired)
			if repo.isPrivate {
				if err := ensureApprovedShare(ctx, q, transcriptID, repo.groupID); err != nil {
					return err
				}
			}
			if target != pre.Visibility {
				if _, err := applyMetadataPatch(ctx, q, transcriptID, metadataPatch{Visibility: &target}); err != nil {
					return fmt.Errorf("could not widen an accepted transcript's visibility: %w", err)
				}
			}
			return nil
		})
	})
}

// disclosureRank orders the visibility tiers by how widely they disclose:
// private, then shared with a collective, then public. Attaching takes the
// WIDER of the transcript's current tier and the repository's requirement, so a
// private repository cannot narrow a transcript the owner already published.
func disclosureRank(value string) int {
	switch value {
	case dbVisibilityPrivate:
		return 0
	case dbVisibilityShared:
		return 1
	case dbVisibilityPublic:
		return 2
	default:
		// An unknown tier ranks above every known one, so a widening decision
		// leaves it alone and a restore never downgrades it. Adding a tier to the
		// menu must update this ordering deliberately.
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

func widestVisibility(current, desired string) string {
	if disclosureRank(current) >= disclosureRank(desired) {
		return current
	}
	return desired
}

// ensureApprovedShare opens an approved share from the transcript to the
// collective, because the collective's owner opted in by linking the repository.
// It bypasses the collective's acceptance mode deliberately: the mode decides
// what a *contribution* becomes, and an attachment is not a contribution. An
// already-approved share is left alone, so a retry does not append a second
// event to the ledger.
func ensureApprovedShare(ctx context.Context, q Querier, transcriptID, groupID pgtype.UUID) error {
	latest, err := q.GetLatestShareAttempt(ctx, sqlc.GetLatestShareAttemptParams{
		TranscriptID: transcriptID,
		GroupID:      groupID,
	})
	if err == nil && latest.Status == string(ShareStatusApproved) {
		return nil
	}
	if err := q.ShareTranscriptWithStatus(ctx, sqlc.ShareTranscriptWithStatusParams{
		TranscriptID: transcriptID,
		GroupID:      groupID,
		Status:       string(ShareStatusApproved),
	}); err != nil {
		return fmt.Errorf("could not open the collective's share for an accepted transcript: %w", err)
	}
	return nil
}

// unwidenAttachedTranscripts restores each bound transcript from its recorded
// snapshot and clears the bindings, undoing a widening whose posting failed. The
// approved share is deliberately left in place: with the transcript private it
// grants nothing, and a retry finds it already approved instead of appending a
// second event to the share ledger.
func (h *Handler) unwidenAttachedTranscripts(ctx context.Context, attachment sqlc.PullRequestAttachment) error {
	bindings, err := h.queries.ListPullRequestAttachmentTranscripts(ctx, attachment.ID)
	if err != nil {
		return fmt.Errorf("could not read the bindings to undo a widening: %w", err)
	}
	for _, binding := range bindings {
		if err := h.restoreTranscriptVisibility(ctx, binding); err != nil {
			return err
		}
	}
	if err := h.queries.DeletePullRequestAttachmentTranscripts(ctx, attachment.ID); err != nil {
		return fmt.Errorf("could not clear the bindings to undo a widening: %w", err)
	}
	return nil
}

// postAttachment posts the check (when the collective asked for one) and the one
// sticky comment, returning the ids to record. A GitHub failure is returned
// unwrapped so the caller answers 502 with the state unchanged.
func (h *Handler) postAttachment(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository, value schema.PromptDigest) (commentID, checkRunID int64, err error) {
	if h.gh == nil {
		return 0, 0, errAttachmentGitHubUnavailable
	}

	comment, err := digest.Render(value, digest.CommentTier)
	if err != nil {
		return 0, 0, fmt.Errorf("could not render the digest for the comment: %w", err)
	}
	summary, err := digest.Render(value, digest.CheckRunTier)
	if err != nil {
		return 0, 0, fmt.Errorf("could not render the digest for the check: %w", err)
	}

	if repo.postCheck {
		request := github.CheckRunRequest{
			HeadSHA:    attachment.HeadSha,
			ExternalID: attachmentExternalID(attachment),
			Conclusion: github.PromptCheckConclusion(repo.checkMode, true),
			Title:      attachmentCheckTitle,
			Summary:    summary,
			Actions:    github.PromptCheckActions(),
		}
		if attachment.CheckRunID.Valid {
			updated, updateErr := h.gh.UpdateCheckRun(ctx, repo.installationID, attachment.RepoOwner, attachment.RepoName, attachment.CheckRunID.Int64, request)
			if updateErr != nil {
				return 0, 0, fmt.Errorf("%w: updating the check run: %v", errAttachmentGitHub, updateErr)
			}
			checkRunID = updated.ID
		} else {
			created, createErr := h.gh.CreateCheckRun(ctx, repo.installationID, attachment.RepoOwner, attachment.RepoName, request)
			if createErr != nil {
				return 0, 0, fmt.Errorf("%w: creating the check run: %v", errAttachmentGitHub, createErr)
			}
			checkRunID = created.ID
		}
	}

	existing := int64(0)
	if attachment.CommentID.Valid {
		existing = attachment.CommentID.Int64
	}
	posted, err := h.gh.UpsertIssueComment(ctx, repo.installationID, attachment.RepoOwner, attachment.RepoName, int(attachment.Number), existing, comment)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: posting the comment: %v", errAttachmentGitHub, err)
	}
	return posted.ID, checkRunID, nil
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

// detachAttachment deletes the comment, resets the check, restores every bound
// transcript from the visibility recorded when it was widened, and moves the
// attachment to detached. GitHub is called first: a failure leaves the state and
// every transcript untouched for a retry.
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
	if repo.postCheck && attachment.CheckRunID.Valid {
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
		return attachment, fmt.Errorf("could not read the attachment's transcripts before restoring them: %w", err)
	}
	for _, binding := range bindings {
		// Retract the grant BEFORE restoring visibility: detaching ends the
		// collective's access, so the share the attach opened goes with it. If a
		// restore then fails, the transcript is shared with nobody and the access
		// is already gone, which is the safe direction.
		if err := h.retractAttachmentShare(ctx, binding.TranscriptID, repo.groupID); err != nil {
			return attachment, err
		}
		if err := h.restoreTranscriptVisibility(ctx, binding); err != nil {
			return attachment, err
		}
	}
	if err := h.queries.DeletePullRequestAttachmentTranscripts(ctx, attachment.ID); err != nil {
		return attachment, fmt.Errorf("could not clear the attachment's transcript bindings: %w", err)
	}

	// The posted objects are gone or reset; keep the digest but drop the ids so
	// a later attach creates rather than edits a deleted comment.
	if err := h.queries.SetPullRequestAttachmentArtifacts(ctx, sqlc.SetPullRequestAttachmentArtifactsParams{
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
	return updated, nil
}

// restoreTranscriptVisibility puts one transcript back to the value recorded on
// its binding, attributed to the transcript's owner so the governance audit
// records who moved the axis.
func (h *Handler) restoreTranscriptVisibility(ctx context.Context, binding sqlc.PullRequestAttachmentTranscript) error {
	transcript, err := h.queries.GetTranscriptByID(ctx, binding.TranscriptID)
	if err != nil {
		return fmt.Errorf("could not read a bound transcript before restoring it: %w", err)
	}
	return h.withPublishLocks(ctx, transcript.OwnerID, transcript.LocalID, nil, func(conn *pgxpool.Conn) error {
		return h.inTxAsOnConn(ctx, conn, transcript.OwnerID, func(q Querier) error {
			// Restore the recorded value only when it is not WIDER than the tier
			// now: if the owner widened the transcript while it was attached,
			// detaching must not undo their later choice.
			pre, err := q.GetTranscriptGovernanceForUpdate(ctx, binding.TranscriptID)
			if err != nil {
				return fmt.Errorf("could not lock a bound transcript before restoring it: %w", err)
			}
			target := narrowestVisibility(pre.Visibility, binding.PreviousVisibility)
			if target == pre.Visibility {
				return nil
			}
			if _, err := applyMetadataPatch(ctx, q, binding.TranscriptID, metadataPatch{Visibility: &target}); err != nil {
				return fmt.Errorf("could not restore a transcript's recorded visibility: %w", err)
			}
			return nil
		})
	})
}

// retractAttachmentShare withdraws the approved share the attach opened to the
// linking collective, so detaching ends the collective's access instead of
// leaving a live grant on a transcript that may be private again. The share
// ledger appends a retraction, so the acceptance's history is preserved rather
// than rewritten. A pair with no live attempt is already retracted.
func (h *Handler) retractAttachmentShare(ctx context.Context, transcriptID, groupID pgtype.UUID) error {
	latest, err := h.queries.GetLatestShareAttempt(ctx, sqlc.GetLatestShareAttemptParams{TranscriptID: transcriptID, GroupID: groupID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("could not read a transcript's share before retracting it: %w", err)
	}
	if latest.Status != string(ShareStatusApproved) && latest.Status != string(ShareStatusPending) {
		return nil
	}
	if err := h.queries.UnshareTranscript(ctx, sqlc.UnshareTranscriptParams{TranscriptID: transcriptID, GroupID: groupID}); err != nil {
		return fmt.Errorf("could not retract the collective's share: %w", err)
	}
	return nil
}
