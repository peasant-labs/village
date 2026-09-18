package handler

import (
	"context"
	"strings"
	"time"

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
//
// The grant lives only as long as the share the attach opened. The binding
// outlives an owner narrowing the transcript, or retracting the collective's
// share, because detach still has to restore what the attach recorded — so
// neither change may leave a repository's readers holding access the owner has
// withdrawn. Each binding is therefore checked against the tier the repository
// requires and against a live approved share, the same rule the digest uses
// before it advertises a row.
//
// Reads ask this. Writes do not: it is a grant to read the prompts, not to
// label somebody else's transcript.
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
		if disclosureRank(t.Visibility) < disclosureRank(requiredAttachmentVisibility(repo)) {
			continue
		}
		if !h.transcriptHasApprovedShareWith(ctx, t.ID, repo.groupID) {
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

// transcriptHasApprovedShareWith reports whether a transcript is currently
// shared, approved, with one collective. It is the second half of the grant's
// liveness: an owner who retracts the share has withdrawn the attachment's
// reason to admit the repository's readers, even though the binding remains.
func (h *Handler) transcriptHasApprovedShareWith(ctx context.Context, transcriptID, groupID pgtype.UUID) bool {
	if !groupID.Valid {
		return false
	}
	groups, err := h.queries.ListApprovedTranscriptShareGroups(ctx, transcriptID)
	if err != nil {
		return false
	}
	for _, shared := range groups {
		if shared == groupID {
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
	// Keyed on the account id, which is what was asked about, and on the
	// repository. The answer is short-lived on purpose: the cache is bounded
	// staleness, never a stored grant.
	key := accountID + "\x00" + strings.ToLower(owner) + "/" + strings.ToLower(name)
	now := time.Now()
	if admits, ok := h.repoAccess.lookup(key, now); ok {
		return admits
	}
	admits := h.askGitHubAboutRepository(ctx, installationID, owner, name, accountID)
	if !admits {
		// Only a refusal is remembered: an admission is asked live so that an
		// owner withdrawing a transcript's share takes effect at once.
		h.repoAccess.store(key, admits, now)
	}
	return admits
}

// askGitHubAboutRepository makes the two calls the answer costs, and denies on
// anything but a permission at or above read.
func (h *Handler) askGitHubAboutRepository(ctx context.Context, installationID int64, owner, name, accountID string) bool {
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
