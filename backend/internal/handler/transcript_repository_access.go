package handler

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/peasant-labs/village/backend/internal/database/sqlc"
)

// githubProvider is the provider name GitHub sign-in stores on the user row.
const githubProvider = "github"

// canReadThroughAttachedRepository answers the one question only GitHub can
// answer: may this signed-in reader read a private repository?
//
// Village cannot learn that on its own, so the reader's access is asked for at
// read time instead of stored. Nothing is recorded: the answer IS the grant, so
// a reader who loses repository access loses the transcript on their next read,
// and no row can go stale or need revoking.
//
// It is a narrow fallback, and only ever consulted after every collected check
// has already refused: for a signed-in reader, for a transcript bound to an
// ATTACHED attachment, and only when that attachment's linked repository is
// private. A detached or waiting attachment grants nothing, because the prompts
// are not attached to anything a reader could have been admitted through.
// A GitHub failure denies rather than admits.
func (h *Handler) canReadThroughAttachedRepository(ctx context.Context, user *AuthUser, t sqlc.Transcript) bool {
	if user == nil || h.gh == nil {
		return false
	}
	attachments, err := h.queries.ListAttachmentsBindingTranscript(ctx, t.ID)
	if err != nil || len(attachments) == 0 {
		return false
	}

	// One lookup per repository, not per attachment: a pull request with several
	// bound transcripts asks the same question each time.
	asked := make(map[string]bool)
	for _, attachment := range attachments {
		repo, err := h.resolveAttachmentRepository(ctx, h.queries, attachment)
		if err != nil || !repo.isPrivate {
			continue
		}
		key := strings.ToLower(attachment.RepoOwner) + "/" + strings.ToLower(attachment.RepoName)
		if asked[key] {
			continue
		}
		asked[key] = true
		if h.repositoryAdmitsViewer(ctx, user.PgID(), repo.installationID, attachment.RepoOwner, attachment.RepoName) {
			return true
		}
	}
	return false
}

// repositoryAdmitsViewer asks GitHub whether one signed-in viewer may read one
// repository. It is the whole of the private path's decision, and the pull
// request page asks it as well as the transcripts, so a repository's readers can
// find the prompts they were admitted to rather than only open one they already
// had the link to.
func (h *Handler) repositoryAdmitsViewer(ctx context.Context, viewerID pgtype.UUID, installationID int64, owner, name string) bool {
	if h.gh == nil || !viewerID.Valid {
		return false
	}
	// The viewer's GitHub identity, keyed on the immutable account id. A login
	// can be renamed and a freed one later taken by another account, so a stored
	// login is never what we ask about. A viewer who signed in through another
	// provider has no GitHub identity here and is refused.
	reader, err := h.queries.GetUserByID(ctx, viewerID)
	if err != nil || reader.Provider != githubProvider || reader.ProviderUserID == "" {
		return false
	}
	return h.githubAdmitsReader(ctx, installationID, owner, name, reader.ProviderUserID)
}

// githubAdmitsReader resolves the reader's current login from their immutable
// account id and asks GitHub what that account may do in the repository. Any
// permission at or above read admits; an unrecognised, empty, or failed answer
// does not. The id-to-login step is not a formality: asking about a stored login
// that had since been renamed would answer for whichever account holds it now,
// and admitting on the wrong account's access is worse than a refusal.
func (h *Handler) githubAdmitsReader(ctx context.Context, installationID int64, owner, name, accountID string) bool {
	login, err := h.gh.GetUserLogin(ctx, installationID, accountID)
	if err != nil {
		return false
	}
	permission, err := h.gh.GetCollaboratorPermission(ctx, installationID, owner, name, login)
	if err != nil {
		return false
	}
	switch permission {
	case "admin", "write", "read":
		return true
	default:
		return false
	}
}
