package github

// Webhook payloads, decoded far enough to answer two questions and no further:
// who sent this command, and what did they ask for. Only the fields the
// attachment flow reads are modelled; the rest of GitHub's payload is ignored
// rather than mirrored, so a provider addition cannot break decoding.

// WebhookUser is a GitHub account reference: its numeric id and its login. The
// numeric id is the identity that matters — a login can be renamed and re-used,
// while the id is stable — so callers resolve users by id and never by login.
type WebhookUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

// WebhookRepository is the repository a webhook is about.
type WebhookRepository struct {
	ID            int64       `json:"id"`
	Name          string      `json:"name"`
	FullName      string      `json:"full_name"`
	Owner         WebhookUser `json:"owner"`
	DefaultBranch string      `json:"default_branch"`
	Fork          bool        `json:"fork"`
}

// WebhookInstallation identifies the App installation that delivered an event.
type WebhookInstallation struct {
	ID int64 `json:"id"`
}

// WebhookIssue is the issue object of an issue_comment event. When the issue is
// a pull request, PullRequest is non-nil and User is the pull request's author.
type WebhookIssue struct {
	Number      int            `json:"number"`
	User        WebhookUser    `json:"user"`
	PullRequest *WebhookPRLink `json:"pull_request"`
}

// WebhookPRLink marks an issue as a pull request. Its value is a URL object; only
// its presence is used.
type WebhookPRLink struct {
	URL     string `json:"url"`
	HTMLURL string `json:"html_url"`
}

// WebhookComment is the comment that carried a command. AuthorAssociation is
// GitHub's own classification of the commenter's relationship to the
// repository, and Body is the raw markdown the command is read out of.
type WebhookComment struct {
	ID                int64       `json:"id"`
	Body              string      `json:"body"`
	AuthorAssociation string      `json:"author_association"`
	User              WebhookUser `json:"user"`
}

// WebhookIssueCommentPayload is the issue_comment event: the shape a
// `/peasant attach` or `/peasant detach` command arrives in.
type WebhookIssueCommentPayload struct {
	Action       string               `json:"action"`
	Issue        WebhookIssue         `json:"issue"`
	Comment      WebhookComment       `json:"comment"`
	Repository   WebhookRepository    `json:"repository"`
	Sender       WebhookUser          `json:"sender"`
	Installation *WebhookInstallation `json:"installation"`
}

// IsPullRequest reports whether the issue the comment is on is a pull request.
// A comment on a plain issue is not an attachment command, whatever it says.
func (p WebhookIssueCommentPayload) IsPullRequest() bool {
	return p.Issue.PullRequest != nil
}

// WebhookRequestedAction is the action a person triggered on a check run. Its
// identifier is the action's stable name (`attach`, `detach`, `refresh`), which
// is what a click reports back.
type WebhookRequestedAction struct {
	Identifier string `json:"identifier"`
}

// WebhookCheckRunPullRequest is the minimal pull request reference a check run
// carries, enough to address the attachment the button belongs to.
type WebhookCheckRunPullRequest struct {
	Number int `json:"number"`
}

// WebhookCheckRunObject is the check run a click acted on.
type WebhookCheckRunObject struct {
	ID           int64                        `json:"id"`
	HeadSHA      string                       `json:"head_sha"`
	PullRequests []WebhookCheckRunPullRequest `json:"pull_requests"`
}

// WebhookCheckRunPayload is the check_run event. A button click arrives as the
// `requested_action` action, with the clicked action's identifier and no other
// change; every other action (created, completed, rerequested) is not a command.
type WebhookCheckRunPayload struct {
	Action          string                  `json:"action"`
	RequestedAction *WebhookRequestedAction `json:"requested_action"`
	CheckRun        WebhookCheckRunObject   `json:"check_run"`
	Repository      WebhookRepository       `json:"repository"`
	Sender          WebhookUser             `json:"sender"`
	Installation    *WebhookInstallation    `json:"installation"`
}

// RequestedActionIdentifier returns the clicked action's identifier, or "" when
// this event is not a button click.
func (p WebhookCheckRunPayload) RequestedActionIdentifier() string {
	if p.Action != "requested_action" || p.RequestedAction == nil {
		return ""
	}
	return p.RequestedAction.Identifier
}

// PullRequestNumber returns the pull request the check run belongs to, or 0 when
// the event carries none.
func (p WebhookCheckRunPayload) PullRequestNumber() int {
	if len(p.CheckRun.PullRequests) == 0 {
		return 0
	}
	return p.CheckRun.PullRequests[0].Number
}

// WebhookPullRequestRef is one side of a pull request: its ref name, the commit
// it points at, and the repository it lives in.
type WebhookPullRequestRef struct {
	Ref  string            `json:"ref"`
	SHA  string            `json:"sha"`
	Repo WebhookRepository `json:"repo"`
}

// WebhookPullRequestObject is the pull_request object.
type WebhookPullRequestObject struct {
	Number int                   `json:"number"`
	User   WebhookUser           `json:"user"`
	Head   WebhookPullRequestRef `json:"head"`
	Base   WebhookPullRequestRef `json:"base"`
	Merged bool                  `json:"merged"`
}

// WebhookPullRequestPayload is the pull_request event: the shape that tells us a
// pull request opened, was updated, or merged.
type WebhookPullRequestPayload struct {
	Action       string                   `json:"action"`
	Number       int                      `json:"number"`
	PullRequest  WebhookPullRequestObject `json:"pull_request"`
	Repository   WebhookRepository        `json:"repository"`
	Sender       WebhookUser              `json:"sender"`
	Installation *WebhookInstallation     `json:"installation"`
}

// IsFork reports whether the pull request's commits come from a repository
// other than the one it targets, which is what lets the matcher also consider
// the head repository when it decides which repository a transcript named.
//
// It fails closed: both repository ids must be present and different. A payload
// missing its base repository is not classified as a fork, because treating an
// absent target as "some other repository" would widen matching on a malformed
// event.
func (p WebhookPullRequestPayload) IsFork() bool {
	if p.PullRequest.Head.Repo.ID <= 0 || p.PullRequest.Base.Repo.ID <= 0 {
		return false
	}
	return p.PullRequest.Head.Repo.ID != p.PullRequest.Base.Repo.ID
}
