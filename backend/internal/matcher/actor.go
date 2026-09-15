package matcher

import "strings"

// Command is the prompt action a person asks Village to take on a pull request.
type Command string

const (
	// CommandAttach asks to attach the author's matching transcripts.
	CommandAttach Command = "attach"
	// CommandDetach asks to detach whatever is attached.
	CommandDetach Command = "detach"
)

// commandName is the one word that introduces a Village prompt command in a
// comment body.
const commandName = "/peasant"

// ParseCommand reads the command from a comment body, if it carries one. The
// body must start with the command word as its own token; the command token
// after it decides the action. Anything after the command token is ignored, so
// a person may add a sentence of their own without breaking the command.
func ParseCommand(body string) (Command, bool) {
	fields := strings.Fields(body)
	if len(fields) < 2 || !strings.EqualFold(fields[0], commandName) {
		return "", false
	}
	switch strings.ToLower(fields[1]) {
	case string(CommandAttach):
		return CommandAttach, true
	case string(CommandDetach):
		return CommandDetach, true
	}
	return "", false
}

// Action is what Village does with a command, once it knows who sent it.
type Action string

const (
	// ActionIgnore does nothing. It is the answer for anyone with no standing,
	// and for a sender Village cannot resolve to a user.
	ActionIgnore Action = "ignore"
	// ActionProceed proceeds to matching. Only the pull request's author gets
	// it, and only accepted matches may then attach.
	ActionProceed Action = "proceed"
	// ActionRequest records a request for the author to act on. A repository
	// owner, member, or collaborator gets it when they ask to attach.
	ActionRequest Action = "request"
)

// The author_association values GitHub reports that carry standing on an attach
// request. Every other value — CONTRIBUTOR, FIRST_TIMER, FIRST_TIME_CONTRIBUTOR,
// MANNEQUIN, NONE — has no standing here.
const (
	AssociationOwner        = "OWNER"
	AssociationMember       = "MEMBER"
	AssociationCollaborator = "COLLABORATOR"
)

// Actor is who sent a command, in the terms the decision needs: whether the
// sender resolved to a Village user, the numeric GitHub ids that decide
// authorship, and the association GitHub reported.
type Actor struct {
	// SenderResolved is false when the sender's numeric id resolved to no
	// Village user. An unresolved sender may never act, whatever else is true.
	SenderResolved bool
	// SenderGitHubID is the webhook sender's numeric GitHub id. Zero means
	// unknown, which never equals an author id.
	SenderGitHubID int64
	// PullRequestAuthorGitHubID is the pull request author's numeric GitHub id.
	PullRequestAuthorGitHubID int64
	// AuthorAssociation is the comment's author_association, as GitHub sent it.
	AuthorAssociation string
}

// Authorize decides what to do with a command from an actor. The result is the
// whole authorization policy: the author may act, a repository owner, member,
// or collaborator may record an attach request for the author, and nobody else
// may do anything. Detaching is the author's alone.
func Authorize(actor Actor, command Command) Action {
	if !actor.SenderResolved {
		return ActionIgnore
	}
	if isAuthor(actor) {
		return ActionProceed
	}
	if command == CommandAttach && hasRepositoryStanding(actor.AuthorAssociation) {
		return ActionRequest
	}
	return ActionIgnore
}

// isAuthor reports whether the sender is the person who opened the pull
// request. It compares numeric GitHub ids, which are stable, rather than
// logins, which can be renamed and re-used.
func isAuthor(actor Actor) bool {
	return actor.SenderGitHubID != 0 && actor.SenderGitHubID == actor.PullRequestAuthorGitHubID
}

// hasRepositoryStanding reports whether GitHub's author_association for the
// comment carries owner, member, or collaborator standing on the repository.
func hasRepositoryStanding(association string) bool {
	switch strings.ToUpper(strings.TrimSpace(association)) {
	case AssociationOwner, AssociationMember, AssociationCollaborator:
		return true
	}
	return false
}
