package handler

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// githubProvider is the provider name GitHub sign-in stores on the user row.
const githubProvider = "github"

// repositoryAccessThrottledMessage is what a viewer is told when they have asked
// too often in too short a time. It names what was limited — the access check —
// rather than pretending the pull request's attachment is not there, and it is a
// refusal either way: nothing is admitted on this path.
const repositoryAccessThrottledMessage = "Too many repository access checks; try again shortly"

// repositoryAdmitsViewer asks GitHub whether one signed-in viewer may read one
// repository. Only the pull request page asks it, to admit a private
// repository's readers to the page for its pull request, which they can already
// read on GitHub.
//
// It admits a reader to the page and to nothing else. It is not a grant to read
// a transcript: attaching never changes who can read one, so the page shows a
// transcript's title and prompts only to a viewer who can open that transcript
// on its own terms (canViewTranscript), and the transcript routes never ask
// this at all.
func (h *Handler) repositoryAdmitsViewer(ctx context.Context, viewerID pgtype.UUID, installationID int64, owner, name string) (admits bool, throttled bool) {
	if h.gh == nil || !viewerID.Valid {
		return false, false
	}
	// The viewer's GitHub identity, keyed on the immutable account id. A login
	// can be renamed and a freed one later taken by another account, so a stored
	// login is never what we ask about. A viewer who signed in through another
	// provider has no GitHub identity here and is refused.
	reader, err := h.queries.GetUserByID(ctx, viewerID)
	if err != nil || reader.Provider != githubProvider || reader.ProviderUserID == "" {
		return false, false
	}
	return h.githubAdmitsReader(ctx, installationID, owner, name, reader.ProviderUserID, viewerID)
}

// githubAdmitsReader resolves the reader's current login from their immutable
// account id and asks GitHub what that account may do in the repository. Any
// permission at or above read admits; an unrecognised, empty, or failed answer
// does not. The id-to-login step is not a formality: asking about a stored login
// that had since been renamed would answer for whichever account holds it now,
// and admitting on the wrong account's access is worse than a refusal.
func (h *Handler) githubAdmitsReader(ctx context.Context, installationID int64, owner, name, accountID string, viewerID pgtype.UUID) (admits bool, throttled bool) {
	// Keyed on the account id, which is what was asked about, and on the
	// repository. The answer is short-lived on purpose: the cache is bounded
	// staleness, never a stored grant.
	key := accountID + "\x00" + strings.ToLower(owner) + "/" + strings.ToLower(name)
	now := time.Now()
	if cached, ok := h.repoAccess.lookup(key, now); ok {
		// A remembered refusal is a repository's answer, not a throttle: the
		// viewer is told the same thing they were told the first time.
		return cached, false
	}
	if !h.repoAccessLimiter.allow(viewerID, now) {
		// Nothing is asked and nothing is remembered, so the viewer is not held
		// to a refusal beyond the limiter's own refill.
		return false, true
	}
	admits = h.askGitHubAboutRepository(ctx, installationID, owner, name, accountID)
	if !admits {
		// Only a refusal is remembered: an admission is asked live so that a
		// reader who loses repository access loses the page at once.
		h.repoAccess.store(key, admits, now)
	}
	return admits, false
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
