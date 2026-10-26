package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/schema"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
	"github.com/peasant-labs/village/backend/internal/digest"
	"github.com/peasant-labs/village/backend/internal/promptattach"
)

// attachmentResponseOf assembles the wire response for one attachment: its row
// with the pull request's title and head branch, the digest it stored, its
// transcripts, and whether the caller is its author. Transcripts is always a
// non-nil slice because the contract declares it non-nullable, so an attachment
// with none still serialises as [].
//
// Being admitted to the attachment is not being admitted to its transcripts.
// Attaching never changes who can read a transcript, so the response carries a
// transcript's title and prompts only to a viewer who can open that transcript
// on its own terms (canViewTranscript). Every other bound transcript is listed
// without its title or start time, and the digest is narrowed to the
// transcripts the viewer can open (digest.Restrict).
func (h *Handler) attachmentResponseOf(ctx context.Context, attachment sqlc.PullRequestAttachment, repo attachmentRepository, viewer *AuthUser) (schema.VillagePullRequestAttachmentResponse, error) {
	mapped, err := mapVillageAttachment(attachment, repo.isPrivate)
	if err != nil {
		return schema.VillagePullRequestAttachmentResponse{}, err
	}
	if err := h.resolveAttachmentPeople(ctx, attachment, viewer, &mapped); err != nil {
		return schema.VillagePullRequestAttachmentResponse{}, err
	}
	detail := h.pullRequestDetail(ctx, repo.installationID, attachment.RepoOwner, attachment.RepoName, int(attachment.Number))
	mapped.Title, mapped.HeadRef = detail.title, detail.headRef

	summaries, err := h.queries.ListPullRequestAttachmentTranscriptSummaries(ctx, attachment.ID)
	if err != nil {
		return schema.VillagePullRequestAttachmentResponse{}, fmt.Errorf("could not read the attachment's transcripts: %w", err)
	}
	transcripts := make([]schema.VillagePullRequestAttachedTranscript, 0, len(summaries))
	readable := make(map[schema.TranscriptID]bool, len(summaries))
	for _, row := range summaries {
		wire, err := mapVillageAttachedTranscript(row)
		if err != nil {
			return schema.VillagePullRequestAttachmentResponse{}, err
		}
		transcript := sqlc.Transcript{ID: row.TranscriptID, OwnerID: row.OwnerID, Visibility: row.Visibility}
		if h.canViewTranscript(ctx, viewer, transcript) {
			readable[wire.TranscriptID] = true
		} else {
			wire.Title = nil
			wire.SessionStart = nil
		}
		transcripts = append(transcripts, wire)
	}

	// The digest is prompt text. It is only served to the author, or once the
	// attachment is attached: a preview digest is the author's own review step,
	// and a detached one describes prompts that are no longer attached.
	viewerIsAuthor := viewer != nil && viewer.PgID() == attachment.AuthorID
	includeDigest := viewerIsAuthor || attachment.State == string(promptattach.Attached)

	var digestValue *schema.PromptDigest
	if includeDigest && len(attachment.Digest) > 0 {
		var parsed schema.PromptDigest
		if err := json.Unmarshal(attachment.Digest, &parsed); err != nil {
			return schema.VillagePullRequestAttachmentResponse{}, fmt.Errorf("the digest stored for this attachment is not readable, so it cannot be served: %w", err)
		}
		// A preview's digest can name a transcript that is not bound yet; it is
		// only ever served to the author, who can open their own.
		previewReadable := map[schema.TranscriptID]bool{}
		previewChecked := map[schema.TranscriptID]bool{}
		restricted, err := digest.Restrict(parsed, func(id schema.TranscriptID) bool {
			if readable[id] {
				return true
			}
			if !viewerIsAuthor || attachment.State != string(promptattach.Preview) {
				return false
			}
			if previewChecked[id] {
				return previewReadable[id]
			}
			previewChecked[id] = true
			// An unbound preview still requires a live transcript the author owns.
			parsedID, err := uuid.Parse(string(id))
			if err != nil {
				return false
			}
			transcript, err := h.queries.GetTranscriptByID(ctx, pgtype.UUID{Bytes: parsedID, Valid: true})
			previewReadable[id] = err == nil && transcript.OwnerID == viewer.PgID()
			return previewReadable[id]
		})
		if err != nil {
			return schema.VillagePullRequestAttachmentResponse{}, fmt.Errorf("the digest stored for this attachment could not be narrowed to what the viewer may read: %w", err)
		}
		digestValue = &restricted
	}

	return schema.VillagePullRequestAttachmentResponse{
		Attachment:     mapped,
		Digest:         digestValue,
		Transcripts:    transcripts,
		ViewerIsAuthor: viewerIsAuthor,
	}, nil
}

// resolveAttachmentPeople fills the attachment's author and requester as the
// viewer may see them. The requester is recorded as a GitHub account id; it is
// resolved to the Village account that signed in with that GitHub identity,
// and a requester with no Village account is anonymous exactly like a hidden
// one, so the response never reveals whether a GitHub user has an account.
func (h *Handler) resolveAttachmentPeople(ctx context.Context, attachment sqlc.PullRequestAttachment, viewer *AuthUser, mapped *schema.VillagePullRequestAttachment) error {
	people := []pgtype.UUID{attachment.AuthorID}
	var requester pgtype.UUID
	if attachment.RequesterGithubID.Valid {
		account, err := h.queries.GetUserByProviderIdentity(ctx, sqlc.GetUserByProviderIdentityParams{
			Provider: "github", ProviderUserID: strconv.FormatInt(attachment.RequesterGithubID.Int64, 10),
		})
		switch {
		case err == nil:
			requester = account.ID
			people = append(people, requester)
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("could not read the attachment's requester: %w", err)
		}
	}
	owners, err := h.resolvePublicOwners(ctx, viewer, people)
	if err != nil {
		return fmt.Errorf("could not resolve the attachment's author and requester: %w", err)
	}
	mapped.Author = owners.of(attachment.AuthorID)
	if attachment.RequesterGithubID.Valid {
		requestedBy := owners.of(requester)
		mapped.RequestedBy = &requestedBy
	}
	return nil
}

// mapVillageAttachment maps a stored attachment to the wire type. The author
// starts anonymous and the requester absent; resolveAttachmentPeople fills
// them for the viewer. It fails
// closed on a state outside the menu rather than serving a value no reader can
// interpret. Title and HeadRef are left null: Village stores neither, and the
// caller that can ask GitHub fills them in.
func mapVillageAttachment(attachment sqlc.PullRequestAttachment, isPrivate bool) (schema.VillagePullRequestAttachment, error) {
	if _, err := promptattach.Parse(attachment.State); err != nil {
		return schema.VillagePullRequestAttachment{}, fmt.Errorf("the attachment's stored state %q is not one of the lifecycle menu: %w", attachment.State, err)
	}

	mapped := schema.VillagePullRequestAttachment{
		ID:                  schema.VillageUUID(uuidFromPg(attachment.ID).String()),
		Owner:               attachment.RepoOwner,
		Name:                attachment.RepoName,
		Number:              int(attachment.Number),
		HeadSHA:             attachment.HeadSha,
		IsPrivateRepository: isPrivate,
		State:               schema.VillagePullRequestAttachmentState(attachment.State),
		Author:              schema.AnonymousVillageOwner(),
		CreatedAt:           attachment.CreatedAt.Time,
		UpdatedAt:           attachment.UpdatedAt.Time,
	}
	if attachment.CommentID.Valid {
		value := attachment.CommentID.Int64
		mapped.CommentID = &value
	}
	if attachment.CheckRunID.Valid {
		value := attachment.CheckRunID.Int64
		mapped.CheckRunID = &value
	}
	// ConfirmedAt is when the author's confirmation attached the prompts, which
	// is the attached timestamp; there is no separate confirmation column.
	if attachment.AttachedAt.Valid {
		value := attachment.AttachedAt.Time
		mapped.ConfirmedAt = &value
	}
	if attachment.DetachedAt.Valid {
		value := attachment.DetachedAt.Time
		mapped.DetachedAt = &value
	}
	return mapped, nil
}

// mapVillageAttachedTranscript maps one binding with its transcript summary. The
// recorded previous visibility is a stored private|shared|public value and the
// wire menu uses the same three tokens, so no second mapping is introduced here.
func mapVillageAttachedTranscript(row sqlc.ListPullRequestAttachmentTranscriptSummariesRow) (schema.VillagePullRequestAttachedTranscript, error) {
	visibility := schema.VillageTranscriptVisibility(row.PreviousVisibility)
	switch visibility {
	case schema.VillageTranscriptVisibilityPrivate, schema.VillageTranscriptVisibilityShared, schema.VillageTranscriptVisibilityPublic:
	default:
		return schema.VillagePullRequestAttachedTranscript{}, fmt.Errorf("a bound transcript recorded previous visibility %q, which is not one of private, shared, public", row.PreviousVisibility)
	}

	mapped := schema.VillagePullRequestAttachedTranscript{
		TranscriptID:       schema.TranscriptID(uuidFromPg(row.TranscriptID).String()),
		Position:           int(row.Position),
		PreviousVisibility: visibility,
	}
	if row.Title.Valid {
		value := row.Title.String
		mapped.Title = &value
	}
	if row.SessionStart.Valid {
		value := row.SessionStart.Time
		mapped.SessionStart = &value
	}
	return mapped, nil
}

// mapVillagePromptRequests maps an author's waiting attachments to the prompt
// request list.
func mapVillagePromptRequests(attachments []sqlc.PullRequestAttachment) schema.VillagePromptRequestsResponse {
	requests := make([]schema.VillagePromptRequest, 0, len(attachments))
	for _, attachment := range attachments {
		requestedAt := attachment.RequestedAt.Time
		if !attachment.RequestedAt.Valid {
			requestedAt = attachment.UpdatedAt.Time
		}
		requests = append(requests, schema.VillagePromptRequest{
			Owner:       attachment.RepoOwner,
			Name:        attachment.RepoName,
			Number:      int(attachment.Number),
			State:       schema.VillagePullRequestAttachmentState(attachment.State),
			Remote:      attachment.BaseRemote,
			HeadRemote:  attachment.HeadRemote,
			RequestedAt: requestedAt,
		})
	}
	return schema.VillagePromptRequestsResponse{Requests: requests}
}

// isCollectiveMember reports whether a caller belongs to the collective. With
// the author, that is what may read a private repository's attachment, so an
// unattached or unknown collective is not a member.
func (h *Handler) isCollectiveMember(ctx context.Context, userID, groupID pgtype.UUID) bool {
	if !groupID.Valid {
		return false
	}
	member, err := h.queries.GetGroupMember(ctx, sqlc.GetGroupMemberParams{GroupID: groupID, UserID: userID})
	// A pending join request is not membership: it grants no read access.
	return err == nil && member.Role != "pending"
}

// mapVillageUserSettings maps the stored user settings to the wire type.
func mapVillageUserSettings(user sqlc.User) schema.VillageUserSettings {
	return schema.VillageUserSettings{
		PreviewBeforeAttach:    user.PreviewBeforeAttach,
		AutoAttachPullRequests: user.AutoAttachPullRequests,
	}
}

// optionalBool is a PATCH field's nullable column form: an omitted field is
// NULL, which the settings update reads as "leave it as it is".
func optionalBool(value *bool) pgtype.Bool {
	if value == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *value, Valid: true}
}
